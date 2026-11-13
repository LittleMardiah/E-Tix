//go:build integration

package integration

// STP traceability: zoned checkout (FASE 10) — success, quota, invalid zone,
// and handler status mapping.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/booking"
	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	zonedSeedEventID = "33333333-3333-3333-3333-333333333333"
	zonedSeedZone    = "VIP"
)

type zonedEnv struct {
	svc  *booking.ZonedService
	pool *pgxpool.Pool
	rdb  *redisclient.Client
	mr   *miniredis.Miniredis
}

// setupZonedEnv wires the real Postgres + a miniredis stand-in. amqp conn is
// nil, so CheckoutZoned skips publishing and the DB/Redis effects are what the
// assertions below observe.
func setupZonedEnv(t *testing.T) *zonedEnv {
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
	rdb := &redisclient.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = rdb.Close() })

	return &zonedEnv{svc: booking.NewZonedService(rdb, nil, pool), pool: pool, rdb: rdb, mr: mr}
}

func zonedQuotaKey(zone string) string {
	return "quota:" + zonedSeedEventID + ":" + zone
}

func zonedEventID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(zonedSeedEventID)
	require.NoError(t, err)
	return id
}

func readQuota(t *testing.T, env *zonedEnv, zone string) string {
	t.Helper()
	val, err := env.rdb.Get(context.Background(), zonedQuotaKey(zone)).Result()
	require.NoError(t, err)
	return val
}

func countZonedBookings(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM bookings WHERE leader_user_id = $1`, userID).Scan(&n))
	return n
}

// TestZonedCheckout_Success: booking row lands in DB with the section + amount
// derived from sections, and the Redis quota is decremented by quantity.
func TestZonedCheckout_Success(t *testing.T) {
	env := setupZonedEnv(t)
	ctx := context.Background()
	userID := insertFreshUser(t, env.pool)

	require.NoError(t, env.rdb.Set(ctx, zonedQuotaKey(zonedSeedZone), 10, 0).Err())

	resp, err := env.svc.CheckoutZoned(ctx, booking.ZonedBookingRequest{
		EventID:  zonedEventID(t),
		ZoneName: zonedSeedZone,
		Quantity: 2,
		UserID:   userID,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.NotEqual(t, uuid.Nil, resp.BookingID, "booking_id must be returned to the client")
	assert.Equal(t, zonedSeedEventID, resp.EventID)
	assert.Equal(t, zonedSeedZone, resp.ZoneName)
	assert.Equal(t, 2, resp.Quantity)
	assert.Equal(t, "success", resp.Status)

	var (
		sectionName string
		totalSeats  int
		mode        string
		status      string
		amount      string
	)
	err = env.pool.QueryRow(ctx, `
		SELECT s.name, b.total_seats, b.payment_mode::text, b.payment_status::text, b.total_amount::text
		FROM bookings b
		JOIN sections s ON s.id = b.section_id
		WHERE b.id = $1`, resp.BookingID).Scan(&sectionName, &totalSeats, &mode, &status, &amount)
	require.NoError(t, err, "booking must exist in DB after checkout")
	assert.Equal(t, zonedSeedZone, sectionName)
	assert.Equal(t, 2, totalSeats)
	assert.Equal(t, "BULK", mode)
	assert.Equal(t, "PAID", status)
	assert.Equal(t, "4000000.00", amount, "2 x VIP price (2000000)")

	assert.Equal(t, "8", readQuota(t, env, zonedSeedZone), "quota must drop by quantity")

	t.Cleanup(func() {
		_, _ = env.pool.Exec(context.Background(), `DELETE FROM bookings WHERE id = $1`, resp.BookingID)
	})
}

// TestZonedCheckout_QuotaExceeded: quantity above remaining quota fails with
// ErrInsufficientQuota (handler maps it to 409) and leaves quota untouched.
func TestZonedCheckout_QuotaExceeded(t *testing.T) {
	env := setupZonedEnv(t)
	ctx := context.Background()
	userID := insertFreshUser(t, env.pool)

	require.NoError(t, env.rdb.Set(ctx, zonedQuotaKey(zonedSeedZone), 1, 0).Err())

	resp, err := env.svc.CheckoutZoned(ctx, booking.ZonedBookingRequest{
		EventID:  zonedEventID(t),
		ZoneName: zonedSeedZone,
		Quantity: 2,
		UserID:   userID,
	})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, booking.ErrInsufficientQuota)
	assert.Equal(t, "1", readQuota(t, env, zonedSeedZone), "failed checkout must not consume quota")
	assert.Equal(t, 0, countZonedBookings(t, env.pool, userID), "no booking row on quota failure")

	w := zonedHandlerResponse(t, env.svc, userID, `{"event_id":"`+zonedSeedEventID+`","zone_name":"`+zonedSeedZone+`","quantity":2}`)
	assert.Equal(t, http.StatusConflict, w.Code, "handler must map quota exhaustion to 409")
}

// TestZonedCheckout_InvalidZone: an unknown zone_name is rejected before Redis
// is touched, so existing quota for a valid zone is never burned.
func TestZonedCheckout_InvalidZone(t *testing.T) {
	env := setupZonedEnv(t)
	ctx := context.Background()
	userID := insertFreshUser(t, env.pool)

	require.NoError(t, env.rdb.Set(ctx, zonedQuotaKey(zonedSeedZone), 5, 0).Err())

	resp, err := env.svc.CheckoutZoned(ctx, booking.ZonedBookingRequest{
		EventID:  zonedEventID(t),
		ZoneName: "ZONA PALSU",
		Quantity: 1,
		UserID:   userID,
	})
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, booking.ErrZoneNotFound)
	assert.Equal(t, "5", readQuota(t, env, zonedSeedZone), "invalid zone must not consume quota")
	assert.Equal(t, 0, countZonedBookings(t, env.pool, userID), "no booking row for invalid zone")

	w := zonedHandlerResponse(t, env.svc, userID, `{"event_id":"`+zonedSeedEventID+`","zone_name":"ZONA PALSU","quantity":1}`)
	assert.Equal(t, http.StatusNotFound, w.Code, "handler must map unknown zone to 404")
}

// TestZonedCheckout_InvalidQuantity: 0 and negative quantities are rejected
// with 400 and never reach Redis.
func TestZonedCheckout_InvalidQuantity(t *testing.T) {
	env := setupZonedEnv(t)
	ctx := context.Background()
	userID := insertFreshUser(t, env.pool)

	require.NoError(t, env.rdb.Set(ctx, zonedQuotaKey(zonedSeedZone), 5, 0).Err())

	for _, qty := range []int{0, -3} {
		resp, err := env.svc.CheckoutZoned(ctx, booking.ZonedBookingRequest{
			EventID:  zonedEventID(t),
			ZoneName: zonedSeedZone,
			Quantity: qty,
			UserID:   userID,
		})
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, booking.ErrInvalidQuantity)
	}

	w := zonedHandlerResponse(t, env.svc, userID, `{"event_id":"`+zonedSeedEventID+`","zone_name":"`+zonedSeedZone+`","quantity":0}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "5", readQuota(t, env, zonedSeedZone))
	assert.Equal(t, 0, countZonedBookings(t, env.pool, userID))
}

// zonedHandlerResponse drives the real gin handler so status-code mapping is
// covered, not just the service error type.
func zonedHandlerResponse(t *testing.T, svc *booking.ZonedService, userID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/bookings/zoned", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user_id", userID.String())

	h := booking.NewZonedHandler(svc, zap.NewNop())
	h.CheckoutZoned(c)
	return w
}

var _ = errors.Is
