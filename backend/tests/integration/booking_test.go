package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/booking"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// Fixed seed UUIDs (from tests/fixtures/seed.sql)
var (
	seedEventID    = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	seedVIPSectID  = uuid.MustParse("44444444-4444-4444-4444-444444444444") // 100 seats, price 2000000
	seedCustomerID = uuid.MustParse("11111111-1111-1111-1111-111111111111") // from seed.sql
)

// fakeDelayedPublisher captures PublishDelayed calls. Thread-safe for concurrency test.
type fakeDelayedPublisher struct {
	mu    sync.Mutex
	calls []delayedCall
}

type delayedCall struct {
	queue string
	delay time.Duration
}

func (f *fakeDelayedPublisher) PublishDelayed(_ context.Context, queueName string, _ []byte, delay time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, delayedCall{queue: queueName, delay: delay})
	return nil
}

// setupBookingEnv wires real PG + miniredis + real Lua scripts + real queue token.
func setupBookingEnv(t *testing.T) (*booking.BookingService, *queue.QueueTokenService, *pgxpool.Pool, *miniredis.Miniredis) {
	t.Helper()

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping integration tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	t.Cleanup(pool.Close)

	// Seed (idempotent via ON CONFLICT)
	seedBytes, err := os.ReadFile("../fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seedBytes))
	require.NoError(t, err)

	// Cleanup bookings created by this test.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM bookings WHERE section_id IN ($1, $2, $3)`,
			seedVIPSectID,
			uuid.MustParse("55555555-5555-5555-5555-555555555555"),
			uuid.MustParse("66666666-6666-6666-6666-666666666666"),
		)
	})

	// Redis (miniredis) — fresh per test.
	mr := miniredis.RunT(t)
	rdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = rdb.Close() })

	// Load Lua scripts from disk (exact source of truth).
	lockLua, err := os.ReadFile("../../internal/booking/scripts/lock_seats.lua")
	require.NoError(t, err)
	releaseLua, err := os.ReadFile("../../internal/booking/scripts/release_seats.lua")
	require.NoError(t, err)
	scripts := &booking.LuaScripts{Lock: string(lockLua), Release: string(releaseLua)}

	// Queue token service (fresh RSA per test).
	qt := newIntegrationQueueToken(t)

	pub := &fakeDelayedPublisher{}
	repo := booking.NewPostgresBookingRepository(pool)
	svc := booking.NewBookingService(repo, rdb, scripts, qt, pub)

	return svc, qt, pool, mr
}

func newIntegrationQueueToken(t *testing.T) *queue.QueueTokenService {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	dir := t.TempDir()

	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	privPath := filepath.Join(dir, "priv.pem")
	require.NoError(t, os.WriteFile(privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600))

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPath := filepath.Join(dir, "pub.pem")
	require.NoError(t, os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o600))

	svc, err := queue.NewQueueTokenService(privPath, pubPath, "etix-integration-booking")
	require.NoError(t, err)
	return svc
}

func newIntegrationToken(t *testing.T, qt *queue.QueueTokenService, uid uuid.UUID) string {
	t.Helper()
	tok, err := qt.Generate(uid, "TIER_2", 80)
	require.NoError(t, err)
	return tok
}

// ============================================================
// IT-BOOK-001: happy path full flow
// ============================================================

func TestIntegration_LockSeats_HappyPath(t *testing.T) {
	svc, qt, pool, _ := setupBookingEnv(t)
	ctx := context.Background()
	uid := seedCustomerID

	tok := newIntegrationToken(t, qt, uid)
	resp, err := svc.LockSeats(ctx, uid, seedEventID, seedVIPSectID,
		&booking.LockSeatsRequest{SeatPositions: []int{10, 11, 12, 13}, PaymentMode: "SPLIT"}, tok)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 4, resp.TotalSeats)
	assert.Equal(t, "8000000", resp.TotalAmount) // 2000000 * 4
	assert.Equal(t, "PENDING", resp.PaymentStatus)
	assert.Equal(t, "SPLIT", resp.PaymentMode)

	// Verify DB row exists
	var dbCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE id = $1`, resp.BookingID).Scan(&dbCount)
	require.NoError(t, err)
	assert.Equal(t, 1, dbCount)

	// Verify booking row content
	var dbSeats, dbAmount, dbStatus string
	err = pool.QueryRow(ctx,
		`SELECT seat_positions::text, total_amount::text, payment_status::text
		 FROM bookings WHERE id = $1`, resp.BookingID,
	).Scan(&dbSeats, &dbAmount, &dbStatus)
	require.NoError(t, err)
	assert.Contains(t, dbSeats, "10")
	assert.Contains(t, dbSeats, "13")
	assert.Equal(t, "8000000.00", dbAmount) // NUMERIC(15,2) renders with 2 decimals
	assert.Equal(t, "PENDING", dbStatus)
}

// ============================================================
// IT-BOOK-002: concurrency — N users race for same seat, 1 winner
// ============================================================

func TestIntegration_LockSeats_Concurrency_OneWinner(t *testing.T) {
	svc, qt, pool, _ := setupBookingEnv(t)
	ctx := context.Background()

	const N = 10
	seat := 42 // any single seat within 1..100

	// Pre-generate tokens (each user gets its own).
	// All goroutines share the same seed user (only 2 users in seed.sql).
	// Race is on the seat bitmap, not on user identity.
	tokens := make([]string, N)
	userIDs := make([]uuid.UUID, N)
	for i := 0; i < N; i++ {
		userIDs[i] = seedCustomerID
		tokens[i] = newIntegrationToken(t, qt, userIDs[i])
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		conflic int
		other   int
	)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.LockSeats(ctx, userIDs[i], seedEventID, seedVIPSectID,
				&booking.LockSeatsRequest{SeatPositions: []int{seat}, PaymentMode: "SPLIT"},
				tokens[i])
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, booking.ErrSeatsNotAvailable):
				conflic++
			default:
				other++
				t.Logf("unexpected err: %v", err)
			}
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 1, wins, "exactly 1 winner expected")
	assert.Equal(t, N-1, conflic, "N-1 conflicts expected")
	assert.Equal(t, 0, other, "no unexpected errors")

	// Verify DB: exactly 1 booking for this seat.
	var dbCount int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM bookings
		 WHERE section_id = $1 AND seat_positions @> $2::jsonb`,
		seedVIPSectID, fmt.Sprintf("[%d]", seat),
	).Scan(&dbCount)
	require.NoError(t, err)
	assert.Equal(t, 1, dbCount)
}

// ============================================================
// IT-BOOK-003: partial conflict rollback — pre-locked bit forces full abort
// ============================================================

func TestIntegration_LockSeats_PartialConflict_RollbackAll(t *testing.T) {
	svc, qt, pool, mr := setupBookingEnv(t)
	ctx := context.Background()
	uid := seedCustomerID

	// Pre-lock seat 5 (offset 4) directly in Redis bitmap — simulate another booking.
	key := fmt.Sprintf("seats:%s:%s:v1", seedEventID, seedVIPSectID)
	rdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	defer func() { _ = rdb.Close() }()
	require.NoError(t, rdb.SetBit(ctx, key, 4, 1).Err())

	// Now try to lock [4, 5, 6] — seat 5 (offset 4) already taken → must FAIL and leave [4, 6] untouched.
	tok := newIntegrationToken(t, qt, uid)
	_, err := svc.LockSeats(ctx, uid, seedEventID, seedVIPSectID,
		&booking.LockSeatsRequest{SeatPositions: []int{4, 5, 6}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, booking.ErrSeatsNotAvailable)

	// Verify no DB row was created.
	var dbCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM bookings WHERE leader_user_id = $1`, uid,
	).Scan(&dbCount)
	require.NoError(t, err)
	assert.Equal(t, 0, dbCount, "no booking row should exist on conflict")

	// Verify bits [3] and [5] (seats 4 and 6) remain 0 — Lua must not have set them.
	b3, _ := rdb.GetBit(ctx, key, 3).Result()
	b5, _ := rdb.GetBit(ctx, key, 5).Result()
	assert.Equal(t, int64(0), b3, "seat 4 bit must remain 0 (no partial lock)")
	assert.Equal(t, int64(0), b5, "seat 6 bit must remain 0 (no partial lock)")

	// Bit 4 (seat 5) should still be 1 (pre-locked, untouched).
	b4, _ := rdb.GetBit(ctx, key, 4).Result()
	assert.Equal(t, int64(1), b4, "pre-locked seat 5 must remain locked")
}

// ============================================================
// Audit fix: assert fakeDelayedPublisher.calls after LockSeats
// ============================================================

// TestIntegration_LockSeats_PublishesDelayedMessage verifies LockSeats
// publishes exactly one RELEASE_SEAT_CHECK message with the 180s TTL.
// Audit gap: previous tests injected fakeDelayedPublisher but never
// inspected its calls.
func TestIntegration_LockSeats_PublishesDelayedMessage(t *testing.T) {
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
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM bookings WHERE leader_user_id = $1`, seedCustomerID)
	})

	mr := miniredis.RunT(t)
	rdb := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = rdb.Close() })

	lockLua, err := os.ReadFile("../../internal/booking/scripts/lock_seats.lua")
	require.NoError(t, err)
	releaseLua, err := os.ReadFile("../../internal/booking/scripts/release_seats.lua")
	require.NoError(t, err)
	scripts := &booking.LuaScripts{Lock: string(lockLua), Release: string(releaseLua)}

	qt := newIntegrationQueueToken(t)
	pub := &fakeDelayedPublisher{}
	repo := booking.NewPostgresBookingRepository(pool)
	svc := booking.NewBookingService(repo, rdb, scripts, qt, pub)

	tok := newIntegrationToken(t, qt, seedCustomerID)
	_, err = svc.LockSeats(ctx, seedCustomerID, seedEventID, seedVIPSectID,
		&booking.LockSeatsRequest{SeatPositions: []int{20, 21}, PaymentMode: "SPLIT"}, tok)
	require.NoError(t, err)

	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.Len(t, pub.calls, 1, "exactly one delayed publish expected")
	assert.Equal(t, "release_seat_check", pub.calls[0].queue)
	assert.Equal(t, 180*time.Second, pub.calls[0].delay)
}

// ============================================================
// Audit fix: 409 TOKEN_ALREADY_USED end-to-end via handler
// ============================================================

// TestIntegration_LockSeats_TokenReplay409 verifies that replaying the same
// queue token through the booking HTTP handler returns 409 TOKEN_ALREADY_USED.
// Audit gap: single-use was verified at Redis level, not through the HTTP path.
func TestIntegration_LockSeats_TokenReplay409(t *testing.T) {
	svc, qt, pool, _ := setupBookingEnv(t)
	ctx := context.Background()
	uid := seedCustomerID

	tok := newIntegrationToken(t, qt, uid)

	// First call: succeeds.
	_, err := svc.LockSeats(ctx, uid, seedEventID, seedVIPSectID,
		&booking.LockSeatsRequest{SeatPositions: []int{30}, PaymentMode: "SPLIT"}, tok)
	require.NoError(t, err)

	// Second call with the SAME token: ErrTokenAlreadyUsed.
	_, err = svc.LockSeats(ctx, uid, seedEventID, seedVIPSectID,
		&booking.LockSeatsRequest{SeatPositions: []int{31}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, booking.ErrTokenAlreadyUsed)

	// DB must show exactly one booking for this user.
	var n int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bookings WHERE leader_user_id = $1`, uid).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "replay must not create a second booking")
}
