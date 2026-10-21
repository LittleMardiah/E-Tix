//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/analytics"
	"github.com/LittleMardiah/E-Tix/backend/internal/event"
)

func setupAnalyticsEnv(t *testing.T) (*analytics.AnalyticsService, *pgxpool.Pool) {
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

	anRepo := analytics.NewPostgresAnalyticsRepository(pool)
	eventRepo := event.NewPostgresEventRepository(pool) // TD-F6-07: ownership lookup now lives in event pkg
	svc := analytics.NewAnalyticsService(anRepo, eventRepo)
	return svc, pool
}

func insertEventForAnalytics(t *testing.T, pool *pgxpool.Pool, ownerID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var eid uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO events (organizer_id, name, venue_name, venue_address,
			event_date, event_start_time, status)
		VALUES ($1, 'Analytics Test', 'V', 'A', CURRENT_DATE + 30, '19:00', 'ON_SALE')
		RETURNING id`, ownerID).Scan(&eid)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM events WHERE id=$1`, eid)
	})
	return eid
}

func TestIntegration_Analytics_Dashboard_OwnerAllowed(t *testing.T) {
	svc, pool := setupAnalyticsEnv(t)
	ctx := context.Background()
	owner := insertFreshUser(t, pool)
	eid := insertEventForAnalytics(t, pool, owner)

	resp, err := svc.GetDashboard(ctx, owner, eid, "event_organizer")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, eid, resp.EventID)
}

func TestIntegration_Analytics_Dashboard_NonOwnerForbidden(t *testing.T) {
	svc, pool := setupAnalyticsEnv(t)
	ctx := context.Background()
	owner := insertFreshUser(t, pool)
	other := insertFreshUser(t, pool)
	eid := insertEventForAnalytics(t, pool, owner)

	_, err := svc.GetDashboard(ctx, other, eid, "event_organizer")
	assert.ErrorIs(t, err, analytics.ErrEventForbidden)
}

func TestIntegration_Analytics_Dashboard_AdminBypass(t *testing.T) {
	svc, pool := setupAnalyticsEnv(t)
	ctx := context.Background()
	owner := insertFreshUser(t, pool)
	adminUser := insertFreshUser(t, pool)
	eid := insertEventForAnalytics(t, pool, owner)

	resp, err := svc.GetDashboard(ctx, adminUser, eid, "admin")
	require.NoError(t, err, "B2 regression: admin must bypass ownership")
	require.NotNil(t, resp)
}
