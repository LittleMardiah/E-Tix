//go:build integration

package ticket

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRepoPool connects to TEST_DB_URL and loads fixtures/seed.sql.
func setupRepoPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping integration")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))

	seed, err := os.ReadFile("../../tests/fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)
	return pool
}

// insertRepoFixture creates a user + booking + ticket; returns IDs.
func insertRepoFixture(t *testing.T, pool *pgxpool.Pool) (ticketID, ownerID, sectionID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	// user
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, phone, name, password_hash, role, status)
		VALUES ('repo-' || gen_random_uuid()::text || '@test.local',
		        '+628' || LPAD((FLOOR(RANDOM() * 10000000000))::BIGINT::TEXT, 10, '0'),
		        'Repo Test', '!disabled!', 'customer', 'ACTIVE')
		RETURNING id`).Scan(&ownerID)
	require.NoError(t, err)

	// section from seed (assume seed has sections; else insert one)
	err = pool.QueryRow(ctx, `SELECT id FROM sections LIMIT 1`).Scan(&sectionID)
	if err != nil {
		// no seed section: create event + section
		var eventID uuid.UUID
		err = pool.QueryRow(ctx, `
			INSERT INTO events (organizer_id, name, venue_name, venue_address,
			                    event_date, event_start_time, status)
			VALUES ($1, 'Repo Test Event', 'V', 'A', CURRENT_DATE + 30, '19:00', 'ON_SALE')
			RETURNING id`, ownerID).Scan(&eventID)
		require.NoError(t, err)

		err = pool.QueryRow(ctx, `
			INSERT INTO sections (event_id, name, price, total_seats, bitmask_key, layout_version)
			VALUES ($1, 'R-TEST', 100000, 100, 'test', 1)
			RETURNING id`, eventID).Scan(&sectionID)
		require.NoError(t, err)
	}

	// booking
	var bookingID uuid.UUID
	var eventID uuid.UUID
	err = pool.QueryRow(ctx, `SELECT event_id FROM sections WHERE id = $1`, sectionID).Scan(&eventID)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		INSERT INTO bookings (event_id, section_id, leader_user_id,
		                      seat_positions, total_seats, total_amount,
		                      payment_mode, payment_status, expires_at)
		VALUES ($1, $2, $3, '[1]'::jsonb, 1, 100000, 'BULK', 'PAID', NOW() + INTERVAL '1 day')
		RETURNING id`, eventID, sectionID, ownerID).Scan(&bookingID)
	require.NoError(t, err)

	// ticket
	err = pool.QueryRow(ctx, `
		INSERT INTO tickets (booking_id, owner_user_id, section_id, seat_position,
		                     status, is_assigned, qr_secret_key, fallback_pin)
		VALUES ($1, $2, $3, 1, 'CONFIRMED', FALSE,
		        gen_random_uuid()::text,
		        UPPER(SUBSTRING(REPLACE(gen_random_uuid()::text, '-', ''), 1, 12)))
		RETURNING id`, bookingID, ownerID, sectionID).Scan(&ticketID)
	require.NoError(t, err)

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM tickets WHERE id = $1`, ticketID)
		_, _ = pool.Exec(c, `DELETE FROM bookings WHERE id = $1`, bookingID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE id = $1`, ownerID)
	})

	return ticketID, ownerID, sectionID
}

// ---------- GetByID ----------

func TestRepo_GetByID_Success(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)
	tid, ownerID, _ := insertRepoFixture(t, pool)

	td, err := r.GetByID(context.Background(), tid)
	require.NoError(t, err)
	assert.Equal(t, tid, td.ID)
	assert.Equal(t, ownerID, td.OwnerUserID)
}

func TestRepo_GetByID_NotFound(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)

	_, err := r.GetByID(context.Background(), uuid.New())
	assert.ErrorIs(t, err, ErrTicketNotFound)
}

// ---------- ListByOwner ----------

func TestRepo_ListByOwner_ReturnsOwnTickets(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)
	_, ownerID, _ := insertRepoFixture(t, pool)

	items, total, err := r.ListByOwner(context.Background(), ownerID, "", 1, 20)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	assert.NotEmpty(t, items)
}

func TestRepo_ListByOwner_EmptyForUnknown(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)

	items, total, err := r.ListByOwner(context.Background(), uuid.New(), "", 1, 20)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.Equal(t, 0, total)
}

// ---------- UpdateAssign ----------

func TestRepo_UpdateAssign_Success(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)
	tid, _, _ := insertRepoFixture(t, pool)
	target := insertRepoUser(t, pool)

	tx, err := pool.Begin(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	rows, err := r.UpdateAssign(context.Background(), tx, tid, target)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows)

	require.NoError(t, tx.Commit(context.Background()))
}

func TestRepo_UpdateAssign_AlreadyAssigned(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)
	tid, _, _ := insertRepoFixture(t, pool)
	target1 := insertRepoUser(t, pool)
	target2 := insertRepoUser(t, pool)

	ctx := context.Background()
	// First assign
	tx1, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = r.UpdateAssign(ctx, tx1, tid, target1)
	require.NoError(t, err)
	require.NoError(t, tx1.Commit(ctx))

	// Second assign → 0 rows
	tx2, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback(ctx) }()
	rows, err := r.UpdateAssign(ctx, tx2, tid, target2)
	require.NoError(t, err)
	assert.Equal(t, int64(0), rows)
}

// ---------- MarkFallbackPINUsed ----------

func TestRepo_MarkFallbackPINUsed_SingleUse(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)
	tid, _, _ := insertRepoFixture(t, pool)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// First
	rows, err := r.MarkFallbackPINUsed(ctx, tx, tid)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows)

	// Second (same tx) → 0
	rows, err = r.MarkFallbackPINUsed(ctx, tx, tid)
	require.NoError(t, err)
	assert.Equal(t, int64(0), rows)
}

// helper
func insertRepoUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var uid uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, phone, name, password_hash, role, status)
		VALUES ('repo2-' || gen_random_uuid()::text || '@test.local',
		        '+628' || LPAD((FLOOR(RANDOM() * 10000000000))::BIGINT::TEXT, 10, '0'),
		        'Repo2', '!disabled!', 'customer', 'ACTIVE')
		RETURNING id`).Scan(&uid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, uid)
	})
	return uid
}

// ---------- GetByIDForUpdate ----------

func TestRepo_GetByIDForUpdate_Success(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)
	tid, ownerID, _ := insertRepoFixture(t, pool)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	td, err := r.GetByIDForUpdate(ctx, tx, tid)
	require.NoError(t, err)
	assert.Equal(t, tid, td.ID)
	assert.Equal(t, ownerID, td.OwnerUserID)
}

func TestRepo_GetByIDForUpdate_NotFound(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = r.GetByIDForUpdate(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, ErrTicketNotFound)
}

// ---------- GetUserStatusAndBlacklist ----------

func TestRepo_GetUserStatusAndBlacklist_ActiveUser(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)
	_, ownerID, _ := insertRepoFixture(t, pool)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	status, blacklisted, err := r.GetUserStatusAndBlacklist(ctx, tx, ownerID)
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", status)
	assert.False(t, blacklisted)
}

func TestRepo_GetUserStatusAndBlacklist_UnknownUser(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, _, err = r.GetUserStatusAndBlacklist(ctx, tx, uuid.New())
	require.Error(t, err)
}

// ---------- ListByOwner status filter ----------

func TestRepo_ListByOwner_StatusFilter(t *testing.T) {
	pool := setupRepoPool(t)
	r := NewPostgresTicketRepository(pool)
	_, ownerID, _ := insertRepoFixture(t, pool)

	items, total, err := r.ListByOwner(context.Background(), ownerID, "CONFIRMED", 1, 20)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	assert.NotEmpty(t, items)
}
