//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/analytics"
	"github.com/LittleMardiah/E-Tix/backend/internal/worker"
)

func TestIntegration_FeedbackLoop_Idempotent(t *testing.T) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))

	seed, err := os.ReadFile("../fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)

	owner := insertFreshUser(t, pool)
	eid := insertEventForAnalytics(t, pool, owner)

	_, err = pool.Exec(ctx, `UPDATE events SET status='COMPLETED', updated_at=NOW() WHERE id=$1`, eid)
	require.NoError(t, err)

	anRepo := analytics.NewPostgresAnalyticsRepository(pool)
	w := worker.NewFeedbackLoopWorker(pool, anRepo, zap.NewNop())

	require.NoError(t, w.RunOnce(ctx))

	// Trigger generate_post_event_report inserts 1 PEAK_TIME on COMPLETED transition
	// (migration 003 fix). Worker inserts 3 more types. Total = 4 expected.
	// Per-type verification (robust to future worker changes).
	expectedTypes := []string{"GATE_STAFFING", "BOT_BLACKLIST", "PRICE_RECOMMENDATION", "PEAK_TIME"}
	for _, itype := range expectedTypes {
		var exists bool
		err = pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM insights WHERE event_id=$1 AND insight_type::text=$2)`,
			eid, itype).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "missing insight type %s", itype)
	}

	var c1 int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM insights WHERE event_id=$1`, eid).Scan(&c1)
	require.NoError(t, err)
	assert.Equal(t, 4, c1, "exactly 4 insights (3 worker + 1 PEAK_TIME)")

	require.NoError(t, w.RunOnce(ctx))
	var c2 int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM insights WHERE event_id=$1`, eid).Scan(&c2)
	require.NoError(t, err)
	assert.Equal(t, c1, c2, "idempotent re-run: no duplicates")

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM insights WHERE event_id=$1`, eid)
	})
}
