//go:build integration

package booking

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Seed UUIDs from tests/fixtures/seed.sql
var (
	repoSeedEventID    = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	repoSeedVIPSectID  = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	repoSeedCAT1SectID = uuid.MustParse("55555555-5555-5555-5555-555555555555")
	repoSeedCustomerID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
)

func newRepoTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping repository integration tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))
	t.Cleanup(pool.Close)

	// Seed (idempotent via ON CONFLICT).
	seedBytes, err := os.ReadFile("../../tests/fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seedBytes), pgx.QueryExecModeSimpleProtocol)
	require.NoError(t, err)

	// Cleanup created bookings.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM bookings WHERE leader_user_id = $1`, repoSeedCustomerID)
	})

	return pool
}

func TestRepo_GetSectionByID_Success(t *testing.T) {
	pool := newRepoTestPool(t)
	repo := NewPostgresBookingRepository(pool)

	sec, err := repo.GetSectionByID(context.Background(), repoSeedVIPSectID)
	require.NoError(t, err)
	require.NotNil(t, sec)

	assert.Equal(t, repoSeedVIPSectID, sec.ID)
	assert.Equal(t, repoSeedEventID, sec.EventID)
	assert.Equal(t, "VIP", sec.Name)
	assert.Equal(t, "2000000.00", sec.Price) // NUMERIC(15,2) rendering
	assert.Equal(t, 100, sec.TotalSeats)
	assert.Equal(t, 1, sec.LayoutVersion)
}

func TestRepo_GetSectionByID_NotFound(t *testing.T) {
	pool := newRepoTestPool(t)
	repo := NewPostgresBookingRepository(pool)

	randomID := uuid.New()
	_, err := repo.GetSectionByID(context.Background(), randomID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "section not found")
}

// FIX P9a-6 #106: the old assertion (assert.Empty) contradicted
// tests/fixtures/seed.sql, which inserts a PAID booking + CONFIRMED ticket
// at seat 2 of CAT 1 (55555555-...). GetTakenSeats selects tickets whose
// status is CONFIRMED/LOCKED/PENDING_SPLIT_PAY/RESALE_PENDING, so CAT 1
// legitimately reports seat 2 as taken. The genuinely-empty case is a
// section without any ticket.
func TestRepo_GetTakenSeats_SeedSeatTaken(t *testing.T) {
	pool := newRepoTestPool(t)
	repo := NewPostgresBookingRepository(pool)

	taken, err := repo.GetTakenSeats(context.Background(), repoSeedCAT1SectID)
	require.NoError(t, err)
	assert.NotEmpty(t, taken, "seed.sql inserts a CONFIRMED ticket at CAT 1 seat 2")
	assert.Contains(t, taken, 2, "seed.sql ticket cccccccc-0002 is at CAT 1 seat 2")
}

func TestRepo_GetTakenSeats_Empty(t *testing.T) {
	pool := newRepoTestPool(t)
	repo := NewPostgresBookingRepository(pool)

	// Random section UUID => no tickets => no taken seats.
	taken, err := repo.GetTakenSeats(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Empty(t, taken)
}

func TestRepo_CreateBooking_Success(t *testing.T) {
	pool := newRepoTestPool(t)
	repo := NewPostgresBookingRepository(pool)
	ctx := context.Background()

	bookingID := uuid.New()
	b := &Booking{
		ID:            bookingID,
		EventID:       repoSeedEventID,
		SectionID:     repoSeedVIPSectID,
		LeaderUserID:  repoSeedCustomerID,
		SeatPositions: []int{1, 2, 3},
		TotalSeats:    3,
		TotalAmount:   "6000000",
		PaymentMode:   "SPLIT",
		PaymentStatus: "PENDING",
		ExpiresAt:     time.Now().Add(180 * time.Second),
	}

	err := repo.CreateBooking(ctx, b)
	require.NoError(t, err)

	// Verify row exists.
	var (
		dbSeats   string
		dbAmount  string
		dbStatus  string
		dbTotal   int
		dbPayMode string
	)
	err = pool.QueryRow(ctx,
		`SELECT seat_positions::text, total_amount::text, payment_status::text,
		        total_seats, payment_mode::text
		 FROM bookings WHERE id = $1`, bookingID,
	).Scan(&dbSeats, &dbAmount, &dbStatus, &dbTotal, &dbPayMode)
	require.NoError(t, err)

	assert.Contains(t, dbSeats, "1")
	assert.Contains(t, dbSeats, "3")
	assert.Equal(t, "6000000.00", dbAmount)
	assert.Equal(t, "PENDING", dbStatus)
	assert.Equal(t, 3, dbTotal)
	assert.Equal(t, "SPLIT", dbPayMode)
}

func TestRepo_CreateBooking_DuplicateID(t *testing.T) {
	pool := newRepoTestPool(t)
	repo := NewPostgresBookingRepository(pool)
	ctx := context.Background()

	bookingID := uuid.New()
	b := &Booking{
		ID:            bookingID,
		EventID:       repoSeedEventID,
		SectionID:     repoSeedVIPSectID,
		LeaderUserID:  repoSeedCustomerID,
		SeatPositions: []int{5},
		TotalSeats:    1,
		TotalAmount:   "2000000",
		PaymentMode:   "BULK",
		PaymentStatus: "PENDING",
		ExpiresAt:     time.Now().Add(180 * time.Second),
	}

	require.NoError(t, repo.CreateBooking(ctx, b))

	// Duplicate ID → PK violation.
	err := repo.CreateBooking(ctx, b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create booking")
}
