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

	"github.com/LittleMardiah/E-Tix/backend/internal/resale"
)

func setupResaleEnv(t *testing.T) (*pgxpool.Pool, resale.ResaleRepository) {
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
	// Bug #53 fix: re-run seed per test to reset ticket status.
	// Previous test run flips seed tickets to RESALE_PENDING (trigger doesn't
	// fire on DELETE). Re-exec idempotent seed resets both to CONFIRMED.
	seedBytes, err := os.ReadFile("../fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seedBytes))
	require.NoError(t, err)

	return pool, resale.NewPostgresResaleRepository(pool)
}

func TestIntegration_Resale_CreateAndBrowse(t *testing.T) {
	pool, repo := setupResaleEnv(t)
	ctx := context.Background()

	var ticketID, sellerID, eventID uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT t.id, t.owner_user_id, s.event_id
		FROM tickets t JOIN sections s ON s.id=t.section_id
		WHERE t.status::text='CONFIRMED' LIMIT 1`).Scan(&ticketID, &sellerID, &eventID)
	if err != nil {
		t.Skip("no CONFIRMED ticket in seed")
	}

	rt := &resale.ResaleTransaction{
		ID: uuid.New(), TicketID: ticketID, EventID: eventID,
		SellerUserID: sellerID, OriginalPrice: "1000000", ResalePrice: "1050000",
		PlatformFee: "26250", SellerPayout: "1023750", Status: "PENDING",
	}
	require.NoError(t, repo.Create(ctx, rt))

	active, err := repo.HasActiveListing(ctx, ticketID)
	require.NoError(t, err)
	assert.True(t, active)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM resale_transactions WHERE id=$1`, rt.ID)
	})
}

func TestIntegration_Resale_TicketInfo(t *testing.T) {
	pool, repo := setupResaleEnv(t)
	ctx := context.Background()
	var ticketID uuid.UUID
	err := pool.QueryRow(ctx, `SELECT id FROM tickets WHERE status::text='CONFIRMED' LIMIT 1`).Scan(&ticketID)
	if err != nil {
		t.Skip("no CONFIRMED ticket")
	}
	info, err := repo.TicketInfoForResale(ctx, ticketID)
	require.NoError(t, err)
	assert.NotEmpty(t, info.SectionPrice)
}
