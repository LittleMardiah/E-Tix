package integration

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRLSPool returns a pool connected to TEST_DB_URL. Skips if TEST_DB_URL
// is unset (same pattern as other integration tests).
func setupRLSPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping RLS integration tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	t.Cleanup(pool.Close)
	return pool
}

// RLS test strategy (Opsi A): create a non-owner role "rls_authenticated",
// SET LOCAL role = rls_authenticated, then set the GUC that the users RLS
// policy consults (auth.uid()). This simulates the Supabase pattern locally
// where the app role is NOT the table owner.
//
// If the RLS policies are missing/incorrect, the test fails because the
// "rls_authenticated" role would see rows it shouldn't (or vice versa).

const rlsTestRole = "rls_test_authenticated"

// setupRLSRole provisions a non-owner role inside the test DB. Idempotent.
func setupRLSRole(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Create role if missing (guard with DO block for idempotency).
	_, err := pool.Exec(ctx, `
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+rlsTestRole+`') THEN
				CREATE ROLE `+rlsTestRole+` NOLOGIN;
			END IF;
		END
		$$;`)
	require.NoError(t, err, "create rls_test_authenticated role")

	// Grant minimal privileges needed for SELECT on users.
	_, err = pool.Exec(ctx, `GRANT USAGE ON SCHEMA public TO `+rlsTestRole)
	require.NoError(t, err, "grant USAGE on schema public")
	_, err = pool.Exec(ctx, `GRANT SELECT ON users TO `+rlsTestRole)
	require.NoError(t, err, "grant SELECT on users")
}

// TestRLS_UserCannotSeeOtherUser verifies the users RLS policy enforces
// id = auth.uid() when the active role is NOT the table owner.
func TestRLS_UserCannotSeeOtherUser(t *testing.T) {
	pool := setupRLSPool(t)
	setupRLSRole(t, pool)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()

	// Insert two users as the owner role (bypasses RLS).
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, phone, name, password_hash, role, status)
		VALUES
			($1, $2, $3, 'RLS User A', 'x', 'customer', 'ACTIVE'),
			($4, $5, $6, 'RLS User B', 'x', 'customer', 'ACTIVE')
		ON CONFLICT (id) DO NOTHING`,
		userA, "rls-a-"+userA.String()+"@test.local", "+6281111111101",
		userB, "rls-b-"+userB.String()+"@test.local", "+6281111111102",
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM users WHERE id IN ($1, $2)`, userA, userB)
	})

	// Acquire a dedicated connection: SET LOCAL only applies for the tx.
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// Simulate authenticated user A.
	_, err = tx.Exec(ctx, `SET LOCAL ROLE `+rlsTestRole)
	require.NoError(t, err, "SET LOCAL ROLE")

	// auth.uid() reads request.jwt.claim.sub (Supabase-compatible shim).
	_, err = tx.Exec(ctx, `SELECT set_config('request.jwt.claim.sub', $1, true)`, userA.String())
	require.NoError(t, err, "set auth uid GUC")

	// Query as authenticated user A: should see only own row.
	var seen int
	err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM public.users WHERE id IN ($1, $2)`, userA, userB).Scan(&seen)
	require.NoError(t, err)
	assert.Equal(t, 1, seen, "RLS should restrict to own user only (id = auth.uid())")

	// Direct fetch of userB should yield 0 rows for user A.
	var name string
	err = tx.QueryRow(ctx, `SELECT name FROM public.users WHERE id = $1`, userB).Scan(&name)
	assert.Error(t, err, "user A must not see user B's row (RLS enforced)")
}

// TestRLS_AdminSeesAll verifies the admin bypass policy works.
func TestRLS_AdminSeesAll(t *testing.T) {
	pool := setupRLSPool(t)
	setupRLSRole(t, pool)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, phone, name, password_hash, role, status)
		VALUES
			($1, $2, $3, 'RLS User A', 'x', 'customer', 'ACTIVE'),
			($4, $5, $6, 'RLS User B', 'x', 'customer', 'ACTIVE')
		ON CONFLICT (id) DO NOTHING`,
		userA, "rls-a2-"+userA.String()+"@test.local", "+6281111111201",
		userB, "rls-b2-"+userB.String()+"@test.local", "+6281111111202",
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM users WHERE id IN ($1, $2)`, userA, userB)
	})

	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL ROLE `+rlsTestRole)
	require.NoError(t, err)

	// Set JWT claims as admin via set_config (true = tx-local).
	// auth.jwt() reads request.jwt.claims (JSONB); the policy checks
	// `(auth.jwt() ->> 'role')`.
	_, err = tx.Exec(ctx, `SELECT set_config('request.jwt.claims', '{"role":"admin"}', true)`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('request.jwt.claim.sub', $1, true)`, userA.String())
	require.NoError(t, err)

	var seen int
	err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM public.users WHERE id IN ($1, $2)`, userA, userB).Scan(&seen)
	require.NoError(t, err)
	assert.Equal(t, 2, seen, "admin should see both users via policy")
}
