//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
	"github.com/LittleMardiah/E-Tix/backend/internal/ticket"
	"github.com/LittleMardiah/E-Tix/backend/internal/worker"
)

type ticketEnv struct {
	svc     *ticket.TicketService
	pool    *pgxpool.Pool
	rdb     *redisclient.Client
	qrStore *redisclient.QRStore
	mr      *miniredis.Miniredis
}

func setupTicketEnv(t *testing.T) *ticketEnv {
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

	seed, err := os.ReadFile("../fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)

	mr := miniredis.RunT(t)
	goredisClient := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	rdb := &redisclient.Client{Client: goredisClient}
	t.Cleanup(func() { _ = rdb.Close() })

	qrStore := redisclient.NewQRStore(rdb.Client)
	repo := ticket.NewPostgresTicketRepository(pool)
	svc := ticket.NewTicketService(repo, qrStore, pool)

	return &ticketEnv{svc: svc, pool: pool, rdb: rdb, qrStore: qrStore, mr: mr}
}

// insertTestTicket creates a minimal CONFIRMED ticket row for the given owner.
// Booking FK is satisfied with a throwaway booking row; the ticket itself is
// the unit under test.
func insertTestTicket(t *testing.T, pool *pgxpool.Pool, ownerID, sectionID uuid.UUID, seat int) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var bookingID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO bookings (
			event_id, section_id, leader_user_id,
			seat_positions, total_seats, total_amount,
			payment_mode, payment_status, expires_at
		) VALUES (
			$1, $2, $3, $4::jsonb, 1, 1000000, 'BULK', 'PAID', NOW() + INTERVAL '1 day'
		) RETURNING id`,
		seedEventID, sectionID, ownerID, `[`+itoa(seat)+`]`,
	).Scan(&bookingID)
	require.NoError(t, err)

	var tid uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO tickets (
			booking_id, owner_user_id, section_id, seat_position,
			status, is_assigned, qr_secret_key, fallback_pin
		) VALUES (
			$1, $2, $3, $4, 'CONFIRMED', FALSE,
			gen_random_uuid()::text, UPPER(SUBSTRING(REPLACE(gen_random_uuid()::text, '-', ''), 1, 12))
		) RETURNING id`,
		bookingID, ownerID, sectionID, seat,
	).Scan(&tid)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tickets WHERE id = $1`, tid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM bookings WHERE id = $1`, bookingID)
	})

	return tid
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// -------- IT-TICK-001: ListByOwner returns only own tickets --------

func TestIntegration_TicketsList_RLSIsolation(t *testing.T) {
	env := setupTicketEnv(t)
	ctx := context.Background()

	ownerA := seedCustomerID
	ownerB := insertFreshUser(t, env.pool)

	tidA := insertTestTicket(t, env.pool, ownerA, seedVIPSectID, 101)
	tidB := insertTestTicket(t, env.pool, ownerB, seedVIPSectID, 102)

	items, _, err := env.svc.ListByOwner(ctx, ownerA, "", 1, 20)
	require.NoError(t, err)

	foundA := false
	for _, it := range items {
		assert.NotEqual(t, tidB, it.ID, "ownerB ticket leaked into ownerA list")
		if it.ID == tidA {
			foundA = true
		}
	}
	assert.True(t, foundA, "ownerA ticket missing from owner list")
}

// -------- IT-TICK-002: GetByID ownership --------

func TestIntegration_TicketDetail_ForbiddenForNonOwner(t *testing.T) {
	env := setupTicketEnv(t)
	ctx := context.Background()

	tid := insertTestTicket(t, env.pool, seedCustomerID, seedVIPSectID, 103)
	otherUser := insertFreshUser(t, env.pool)

	_, err := env.svc.GetByID(ctx, otherUser, tid)
	assert.ErrorIs(t, err, ticket.ErrTicketForbidden)

	td, err := env.svc.GetByID(ctx, seedCustomerID, tid)
	require.NoError(t, err)
	assert.Equal(t, seedCustomerID, td.OwnerUserID)
}

// -------- IT-TICK-003: GetQR cache miss -> regen -> cache hit --------

func TestIntegration_GetQR_CacheMissThenHit(t *testing.T) {
	env := setupTicketEnv(t)
	ctx := context.Background()

	tid := insertTestTicket(t, env.pool, seedCustomerID, seedVIPSectID, 104)

	r1, err := env.svc.GetQR(ctx, seedCustomerID, tid)
	require.NoError(t, err)
	require.NotEmpty(t, r1.QRData)
	require.Greater(t, r1.RefreshIn, 0)

	cacheKey := "qr:" + tid.String()
	require.True(t, env.mr.Exists(cacheKey), "expected Redis key %s to be set", cacheKey)

	r2, err := env.svc.GetQR(ctx, seedCustomerID, tid)
	require.NoError(t, err)
	assert.Equal(t, r1.QRData, r2.QRData)
}

// -------- IT-TICK-004: QR regeneration worker writes qr:{tid} --------

func TestIntegration_QRRegenWorker_WritesActiveTickets(t *testing.T) {
	env := setupTicketEnv(t)
	ctx := context.Background()

	tid := insertTestTicket(t, env.pool, seedCustomerID, seedVIPSectID, 105)

	w := worker.NewQRRegenerationWorker(env.pool, env.rdb, newTestLogger(), 0)
	require.NoError(t, w.RunOnce(ctx))

	cacheKey := "qr:" + tid.String()
	require.True(t, env.mr.Exists(cacheKey), "worker did not populate %s", cacheKey)
	val, err := env.rdb.GetString(ctx, cacheKey)
	require.NoError(t, err)
	assert.NotEmpty(t, val)
}

// insertFreshUser inserts a minimal ACTIVE user and returns its ID.
// Used for cross-owner isolation tests.
func insertFreshUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var uid uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO users (email, phone, name, password_hash, role, status)
		VALUES (
			'it-ticket-' || gen_random_uuid()::text || '@test.local',
			'+628' || LPAD((FLOOR(RANDOM() * 10000000000))::BIGINT::TEXT, 10, '0'),
			'IT Ticket User',
			'!disabled-it-ticket!',
			'customer',
			'ACTIVE'
		) RETURNING id`,
	).Scan(&uid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, uid)
	})
	return uid
}

func newTestLogger() *zap.Logger {
	return zap.NewNop()
}
