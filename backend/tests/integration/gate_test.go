//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/gate"
	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
	"github.com/LittleMardiah/E-Tix/backend/internal/ticket"
)

type gateEnv struct {
	svc  *gate.GateService
	pool *pgxpool.Pool
	rdb  *redisclient.Client
	mr   *miniredis.Miniredis
}

func setupGateEnv(t *testing.T) *gateEnv {
	t.Helper()
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

	mr := miniredis.RunT(t)
	rdb := &redisclient.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = rdb.Close() })

	repo := gate.NewPostgresGateRepository(pool)
	svc := gate.NewGateService(repo, pool)
	return &gateEnv{svc: svc, pool: pool, rdb: rdb, mr: mr}
}

// insertConfirmedTicket creates a fresh CONFIRMED ticket; returns (ticketID, ownerID, secret, fallbackPIN).
func insertConfirmedTicket(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID, string, string) {
	t.Helper()
	ctx := context.Background()

	ownerID := insertFreshUser(t, pool)

	var sectionID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM sections LIMIT 1`).Scan(&sectionID)
	require.NoError(t, err, "seed.sql must contain at least one section")

	var bookingID uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO bookings (event_id, section_id, leader_user_id,
			seat_positions, total_seats, total_amount, payment_mode, payment_status, expires_at)
		SELECT s.event_id, $1, $2, '[1]'::jsonb, 1, 100000, 'BULK', 'PAID', NOW() + INTERVAL '1 day'
		FROM sections s WHERE s.id = $1
		RETURNING id`, sectionID, ownerID).Scan(&bookingID)
	require.NoError(t, err)

	var ticketID uuid.UUID
	var secret, pin string
	err = pool.QueryRow(ctx, `
		INSERT INTO tickets (booking_id, owner_user_id, section_id, seat_position,
			status, is_assigned, qr_secret_key, fallback_pin)
		VALUES ($1, $2, $3, 1, 'CONFIRMED', FALSE, $4, $5)
		RETURNING id, qr_secret_key, fallback_pin`,
		bookingID, ownerID, sectionID,
		uuid.New().String(), "PIN"+uuid.New().String()[:9]).Scan(&ticketID, &secret, &pin)
	require.NoError(t, err)

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM tickets WHERE id=$1`, ticketID)
		_, _ = pool.Exec(c, `DELETE FROM bookings WHERE id=$1`, bookingID)
	})

	return ticketID, ownerID, secret, pin
}

// ------- IT-GATE-001: scan happy path -------

func TestIntegration_Gate_ScanQR_Happy(t *testing.T) {
	env := setupGateEnv(t)
	ctx := context.Background()
	tid, owner, secret, _ := insertConfirmedTicket(t, env.pool)

	qr, err := ticket.GenerateQRData(tid, owner, secret, time.Now())
	require.NoError(t, err)

	res, err := env.svc.ScanQR(ctx, owner, &gate.ScanQRRequest{
		TicketID: tid, QRData: qr, GateID: "A",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})
	require.NoError(t, err)
	assert.True(t, res.Valid)
	assert.Equal(t, "USED", res.Status)

	var status string
	err = env.pool.QueryRow(ctx, `SELECT status::text FROM tickets WHERE id=$1`, tid).Scan(&status)
	require.NoError(t, err)
	assert.Equal(t, "USED", status)
}

// ------- IT-GATE-002: CONCURRENT scan — B5 regression -------

func TestIntegration_Gate_ScanQR_Concurrent_OneWinner(t *testing.T) {
	env := setupGateEnv(t)
	ctx := context.Background()
	tid, owner, secret, _ := insertConfirmedTicket(t, env.pool)

	const N = 10
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			qr, _ := ticket.GenerateQRData(tid, owner, secret, time.Now())
			_, err := env.svc.ScanQR(ctx, owner, &gate.ScanQRRequest{
				TicketID: tid, QRData: qr, GateID: "A",
				ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
			})
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	wins, alr := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			wins++
		case errors.Is(e, gate.ErrAlreadyUsed):
			alr++
		default:
			t.Logf("unexpected: %v", e)
		}
	}
	assert.Equal(t, 1, wins, "B5 regression: exactly 1 winner required")
	assert.Equal(t, N-1, alr, "N-1 must be ALREADY_USED")

	// Verify: exactly 1 VALID scan in DB (B5 proof).
	var validCount int
	err := env.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ticket_scans WHERE ticket_id=$1 AND scan_status::text='VALID'`,
		tid).Scan(&validCount)
	require.NoError(t, err)
	assert.Equal(t, 1, validCount, "exactly 1 VALID scan — B5 race fix verified")
}

// ------- IT-GATE-003: sync conflict demotion (B1 regression) -------

func TestIntegration_Gate_SyncConflict_DemotesPreviousWinner(t *testing.T) {
	env := setupGateEnv(t)
	ctx := context.Background()
	tid, owner, secret, _ := insertConfirmedTicket(t, env.pool)
	_ = owner
	_ = secret

	// Device A syncs first with T=now (wins initially).
	now := time.Now().UTC()
	scanA := uuid.New()
	devA := uuid.New()
	qrA, _ := ticket.GenerateQRData(tid, owner, secret, now)

	// Manually create a "winner" scan_A by using SyncOfflineScans with future time
	// — but we want scan_A T=now+10s and scan_B T=now (B beats A).
	resA, err := env.svc.SyncOfflineScans(ctx, owner, &gate.SyncRequest{
		DeviceID: devA,
		Scans: []gate.OfflineScan{{
			ScanID: scanA, TicketID: tid, GateID: "A",
			ScannedAt:  now.Add(10 * time.Second),
			ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, resA.Synced)
	_ = qrA

	// Device B syncs later with T=now (earlier). Should demote A.
	scanB := uuid.New()
	devB := uuid.New()
	resB, err := env.svc.SyncOfflineScans(ctx, owner, &gate.SyncRequest{
		DeviceID: devB,
		Scans: []gate.OfflineScan{{
			ScanID: scanB, TicketID: tid, GateID: "B",
			ScannedAt:  now,
			ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, resB.Synced)

	// scan_A must be ALREADY_USED (demoted); scan_B VALID.
	var statusA, statusB string
	err = env.pool.QueryRow(ctx, `SELECT scan_status::text FROM ticket_scans WHERE id=$1`, scanA).Scan(&statusA)
	require.NoError(t, err)
	err = env.pool.QueryRow(ctx, `SELECT scan_status::text FROM ticket_scans WHERE id=$1`, scanB).Scan(&statusB)
	require.NoError(t, err)
	assert.Equal(t, "ALREADY_USED", statusA, "B1 regression: previous winner must be demoted")
	assert.Equal(t, "VALID", statusB, "B1 regression: earlier scan wins")
}

// IT-GATE-003: Expired HMAC (>±30s tolerance) -> ErrInvalidQR.
func TestIntegration_Gate_ScanQR_ExpiredHMAC(t *testing.T) {
	env := setupGateEnv(t)
	ctx := context.Background()
	tid, owner, secret, _ := insertConfirmedTicket(t, env.pool)

	// T-90s = 3 blocks back, beyond ±1 tolerance.
	expired := time.Now().Add(-90 * time.Second)
	qr, err := ticket.GenerateQRData(tid, owner, secret, expired)
	require.NoError(t, err)

	// scanner_user_id FK requires a real user; owner exists via insertFreshUser.
	_, err = env.svc.ScanQR(ctx, owner, &gate.ScanQRRequest{
		TicketID: tid, QRData: qr, GateID: "A",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})
	assert.ErrorIs(t, err, gate.ErrInvalidQR)

	var n int
	err = env.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ticket_scans WHERE ticket_id=$1 AND scan_status::text='INVALID'`,
		tid).Scan(&n)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}
