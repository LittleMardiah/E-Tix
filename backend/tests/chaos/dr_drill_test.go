//go:build chaos

// Package chaos contains the Disaster Recovery drill probes for DRP §7.2
// ("DISASTER RECOVERY DRILLS").
//
// FASE 9 Epic B / P9a-11 — the drills are Tabletop / dry-run simulations run
// against the local Docker stack (docker-compose.yml), because no live
// staging environment (Render / Supabase) is available.
//
// The `chaos` build tag keeps these probes out of the normal unit and
// integration CI lanes. They are executed explicitly by scripts/dr_drill.sh:
//
//	go test -tags chaos -count=1 -v -run TestDrill_Redis ./tests/chaos/...
//
// Two env gates:
//   - DR_DRILL=1      the probes only run inside an actual drill run.
//   - DR_DRILL_PHASE  "down" -> the fault is injected and degraded behaviour
//     is expected; "up" -> the fault is cleared and recovery
//     behaviour is expected.
//
// The probes deliberately drive the *application* seams
// (internal/redis.Client.Health, internal/queue.Connection.Health) instead of
// raw TCP checks, because those are the exact seams /healthz/readiness
// (cmd/api/main.go) uses to decide whether the service is degraded.
package chaos

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	defaultRedisAddr = "localhost:16379"
	defaultAMQPURL   = "amqp://guest:guest@localhost:5673/"
	defaultDBURL     = "postgresql://postgres:postgres@localhost:15433/etix_flash_dr?sslmode=disable"
)

// criticalTables mirrors DRP Playbook #1 Phase 2 Step 4 — "Verify critical
// tables exist (12 tabel)".
var criticalTables = []string{
	"users",
	"events",
	"sections",
	"bookings",
	"payment_obligations",
	"tickets",
	"ticket_scans",
	"resale_transactions",
	"admin_actions",
	"insights",
	"blacklist",
	"system_config",
}

// drillPhase gates the probes and returns the phase they must assert
// against. Without DR_DRILL=1 the probe is skipped, so a plain `go test ./...`
// never touches the Docker stack.
func drillPhase(t *testing.T) string {
	t.Helper()
	if os.Getenv("DR_DRILL") != "1" {
		t.Skip("DR_DRILL != 1: skipping (probe only runs inside a DR drill)")
	}
	phase := os.Getenv("DR_DRILL_PHASE")
	if phase != "down" && phase != "up" {
		t.Fatalf(`DR_DRILL_PHASE must be "down" or "up", got %q`, phase)
	}
	return phase
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// TestDrill_Redis covers DRP Playbook #2 (Redis Cluster Total Failure).
//
// down: the Redis container is stopped, so the readiness probe must fail and
//
//	the service must be considered degraded.
//
// up:   the container is back, so the readiness probe must succeed again.
func TestDrill_Redis(t *testing.T) {
	phase := drillPhase(t)

	rdb, err := redis.NewClient("redis://" + envOr("DR_REDIS_ADDR", defaultRedisAddr))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rdb.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err = rdb.Health(ctx)
	if phase == "down" {
		require.Error(t, err, "Playbook #2: Redis must be unreachable while the container is down")
		t.Logf("Playbook #2 [down]: redis unreachable as expected (err=%v)", err)
		return
	}
	require.NoError(t, err, "Playbook #2: Redis must be reachable after recovery")
	t.Log("Playbook #2 [up]: redis healthy as expected")
}

// TestDrill_RabbitMQ covers DRP Playbook #3 (RabbitMQ / CloudAMQP Failure).
//
// down: the broker container is stopped, so the connection must fail and the
//
//	service must fall back to the degraded path.
//
// up:   the broker is back, so the connection must succeed again.
func TestDrill_RabbitMQ(t *testing.T) {
	phase := drillPhase(t)

	conn, err := queue.NewConnection(envOr("DR_AMQP_URL", defaultAMQPURL))
	if phase == "down" {
		require.Error(t, err, "Playbook #3: RabbitMQ must be unreachable while the container is down")
		t.Logf("Playbook #3 [down]: rabbitmq unreachable as expected (err=%v)", err)
		return
	}
	require.NoError(t, err, "Playbook #3: RabbitMQ must be reachable after recovery")
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, conn.Health(context.Background()),
		"Playbook #3: broker connection must report healthy after recovery")
	t.Log("Playbook #3 [up]: rabbitmq healthy as expected")
}

// pgSnapshot is the pre-corruption fingerprint of the drill database written
// by scripts/dr_drill.sh before the simulated corruption.
type pgSnapshot struct {
	CapturedAt string           `json:"captured_at"`
	Tables     map[string]int64 `json:"tables"`
	RLS        map[string]bool  `json:"rls"`
}

// TestDrill_Postgres_CorruptionDetected covers DRP Playbook #1 Phase 1 —
// "Identifikasi apakah korupsi data atau hardware failure". The drill breaks
// the schema on purpose, so this probe proves the corruption is *detectable*
// before the restore runs (otherwise the restore probe would prove nothing).
func TestDrill_Postgres_CorruptionDetected(t *testing.T) {
	drillPhase(t)

	pool := drillPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	existing := tableNames(t, pool, ctx)
	missing := 0
	for _, name := range criticalTables {
		if !slices.Contains(existing, name) {
			missing++
		}
		assert.NotContains(t, existing, name,
			"Playbook #1: critical table %q must be missing after the simulated corruption", name)
	}
	t.Logf("Playbook #1 [corrupted]: %d of %d critical tables missing, %d public table(s) left",
		missing, len(criticalTables), len(existing))
}

// TestDrill_Postgres_RestoreIntegrity covers DRP Playbook #1 Phase 2 Step 4-6
// plus Phase 3 Validation: after the restore the critical tables must exist
// again, the row counts must match the pre-corruption snapshot and the RLS
// flags must be unchanged.
func TestDrill_Postgres_RestoreIntegrity(t *testing.T) {
	drillPhase(t)

	pool := drillPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	snap := readSnapshot(t)

	existing := tableNames(t, pool, ctx)
	for _, name := range criticalTables {
		require.Contains(t, existing, name,
			"Playbook #1: critical table %q must exist after the restore", name)
	}

	rowDrift, rlsDrift := 0, 0
	for _, name := range criticalTables {
		want, ok := snap.Tables[name]
		require.True(t, ok, "Playbook #1: snapshot carries no row count for %q", name)

		var got int64
		err := pool.QueryRow(ctx, "SELECT count(*) FROM public."+quoteIdent(name)).Scan(&got)
		require.NoError(t, err, "Playbook #1: cannot count rows of %q after the restore", name)
		if !assert.Equal(t, want, got, "Playbook #1: row count drift on %q after the restore", name) {
			rowDrift++
		}
	}

	for name, want := range snap.RLS {
		var got bool
		err := pool.QueryRow(ctx,
			"SELECT rowsecurity FROM pg_tables WHERE schemaname='public' AND tablename=$1", name).Scan(&got)
		require.NoError(t, err, "Playbook #1: cannot read the RLS flag of %q", name)
		if !assert.Equal(t, want, got, "Playbook #1: RLS flag drift on %q after the restore", name) {
			rlsDrift++
		}
	}

	t.Logf("Playbook #1 [restored]: %d critical tables present, %d row-count mismatch(es), %d RLS-flag mismatch(es)",
		len(criticalTables), rowDrift, rlsDrift)
}

func drillPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), envOr("DR_DATABASE_URL", defaultDBURL))
	require.NoError(t, err, "drill database must be reachable")
	t.Cleanup(pool.Close)
	return pool
}

func tableNames(t *testing.T, pool *pgxpool.Pool, ctx context.Context) []string {
	t.Helper()
	rows, err := pool.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='public'")
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, rows.Err())
	return names
}

func readSnapshot(t *testing.T) pgSnapshot {
	t.Helper()
	path := os.Getenv("DR_SNAPSHOT_FILE")
	require.NotEmpty(t, path, "DR_SNAPSHOT_FILE must point at the pre-corruption snapshot")

	raw, err := os.ReadFile(resolvePath(path))
	require.NoError(t, err, "snapshot file %q must be readable", path)

	var snap pgSnapshot
	require.NoError(t, json.Unmarshal(raw, &snap), "snapshot file %q must contain valid JSON", path)
	require.NotEmpty(t, snap.Tables, "snapshot must carry the row counts")
	require.NotEmpty(t, snap.RLS, "snapshot must carry the RLS flags")
	return snap
}

// resolvePath turns a relative path into an absolute one, anchored at the
// module root. `go test` executes the test binary inside the package
// directory (tests/chaos), so a relative DR_SNAPSHOT_FILE would otherwise be
// resolved against the wrong directory.
func resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	dir, err := os.Getwd()
	if err != nil {
		return path
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		dir = parent
	}
}

// quoteIdent quotes a SQL identifier. Only names coming from the fixed
// criticalTables list reach this helper.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
