//go:build integration

package admin_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
)

func setupComplexEnv(t *testing.T) (*pgxpool.Pool, admin.AdminRepository) {
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
	return pool, admin.NewPostgresAdminRepository(pool)
}

// insertBooking creates a booking with the given payment status/expiry and
// registers cleanup. Returns the booking ID + section/event IDs.
func insertBooking(t *testing.T, pool *pgxpool.Pool, status string, expiresAt time.Time, seats string) (bookingID, sectionID, eventID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var userID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id, event_id FROM sections LIMIT 1`).Scan(&sectionID, &eventID), "no sections in seed")
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM users WHERE role::text='customer' LIMIT 1`).Scan(&userID), "no customer seed")

	bookingID = uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO bookings (id, event_id, section_id, leader_user_id, seat_positions, total_seats, total_amount, payment_mode, payment_status, expires_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, 1, 1000000, 'BULK', $6::payment_status_enum, $7)`,
		bookingID, eventID, sectionID, userID, seats, status, expiresAt)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM bookings WHERE id=$1`, bookingID)
	})
	return bookingID, sectionID, eventID
}

// seedPaidObligation creates a PAID obligation on a fresh booking and returns
// its ID + booking/event/user IDs.
func seedPaidObligation(t *testing.T, pool *pgxpool.Pool) (obID, bookingID, eventID, userID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	bookingID, _, eventID = insertBooking(t, pool, "PENDING", time.Now().Add(24*time.Hour), "[1]")
	require.NoError(t, pool.QueryRow(ctx, `SELECT leader_user_id FROM bookings WHERE id=$1`, bookingID).Scan(&userID))

	obID = uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO payment_obligations (id, booking_id, user_id, amount, status, external_transaction_id, created_at, updated_at)
		VALUES ($1, $2, $3, 1000000, 'PAID', $4, NOW() - INTERVAL '1 hour', NOW())`,
		obID, bookingID, userID, "test-ext-"+uuid.NewString())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_obligations WHERE id=$1`, obID)
	})
	return obID, bookingID, eventID, userID
}

// insertObligation creates an obligation with an explicit status.
func insertObligation(t *testing.T, pool *pgxpool.Pool, bookingID, userID uuid.UUID, status string) uuid.UUID {
	t.Helper()
	obID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO payment_obligations (id, booking_id, user_id, amount, status, external_transaction_id)
		VALUES ($1, $2, $3, 500000, $4::payment_status_enum, $5)`,
		obID, bookingID, userID, status, "test-"+uuid.NewString())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payment_obligations WHERE id=$1`, obID)
	})
	return obID
}

// ---- GetPaymentObligationForUpdate ----

func TestRepo_GetPaymentObligationForUpdate_Success(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	obID, bookingID, _, userID := seedPaidObligation(t, pool)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	le, err := repo.GetPaymentObligationForUpdate(ctx, tx, obID)
	require.NoError(t, err)
	require.NotNil(t, le)
	assert.Equal(t, obID, le.ReferenceID)
	assert.Equal(t, "payment_obligation", le.ReferenceType)
	assert.Equal(t, "PAYMENT_PAID", le.EventType)
	assert.Equal(t, "PAID", le.Status)
	require.NotNil(t, le.BookingID)
	assert.Equal(t, bookingID, *le.BookingID)
	require.NotNil(t, le.UserID)
	assert.Equal(t, userID, *le.UserID)
	assert.True(t, le.Reconciled)
	assert.NotEmpty(t, le.Amount)
}

func TestRepo_GetPaymentObligationForUpdate_NotFound(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = repo.GetPaymentObligationForUpdate(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
}

func TestRepo_GetPaymentObligationForUpdate_LockBehavior(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	obID, _, _, _ := seedPaidObligation(t, pool)

	tx1, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = repo.GetPaymentObligationForUpdate(ctx, tx1, obID)
	require.NoError(t, err)

	var gotErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tx2Ctx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
		defer cancel()
		tx2, err := pool.Begin(tx2Ctx)
		if err != nil {
			gotErr = err
			return
		}
		defer func() { _ = tx2.Rollback(tx2Ctx) }()
		_, gotErr = repo.GetPaymentObligationForUpdate(tx2Ctx, tx2, obID)
	}()
	wg.Wait()
	_ = tx1.Commit(ctx)

	assert.Error(t, gotErr, "second tx should block on FOR UPDATE lock")
}

func TestRepo_GetPaymentObligationForUpdate_UnlocksAfterCommit(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	obID, _, _, _ := seedPaidObligation(t, pool)

	tx1, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = repo.GetPaymentObligationForUpdate(ctx, tx1, obID)
	require.NoError(t, err)
	require.NoError(t, tx1.Commit(ctx))

	// After commit the lock is released; a fresh tx succeeds.
	tx2, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx2.Rollback(ctx) }()
	le, err := repo.GetPaymentObligationForUpdate(ctx, tx2, obID)
	require.NoError(t, err)
	assert.Equal(t, "PAID", le.Status)
}

// ---- UpdatePaymentObligationRefunded ----

func TestRepo_UpdatePaymentObligationRefunded_Success(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	obID, _, _, _ := seedPaidObligation(t, pool)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	refundTx := "refund-" + uuid.NewString()
	require.NoError(t, repo.UpdatePaymentObligationRefunded(ctx, tx, obID, refundTx))
	require.NoError(t, tx.Commit(ctx))

	var status, gotRefund string
	var refundedAt *time.Time
	err = pool.QueryRow(ctx, `SELECT status::text, refund_transaction_id, refunded_at FROM payment_obligations WHERE id=$1`, obID).Scan(&status, &gotRefund, &refundedAt)
	require.NoError(t, err)
	assert.Equal(t, "REFUNDED", status)
	assert.Equal(t, refundTx, gotRefund)
	assert.NotNil(t, refundedAt)
}

func TestRepo_UpdatePaymentObligationRefunded_Conflict(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, _, _ := insertBooking(t, pool, "PENDING", time.Now().Add(24*time.Hour), "[1,2]")
	var userID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT leader_user_id FROM bookings WHERE id=$1`, bookingID).Scan(&userID))
	obID := insertObligation(t, pool, bookingID, userID, "PENDING")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = repo.UpdatePaymentObligationRefunded(ctx, tx, obID, "refund-x")
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
}

func TestRepo_UpdatePaymentObligationRefunded_NotFound(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = repo.UpdatePaymentObligationRefunded(ctx, tx, uuid.New(), "refund-y")
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
}

func TestRepo_UpdatePaymentObligationRefunded_AlreadyRefunded(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, _, _ := insertBooking(t, pool, "PENDING", time.Now().Add(24*time.Hour), "[3]")
	var userID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT leader_user_id FROM bookings WHERE id=$1`, bookingID).Scan(&userID))
	obID := insertObligation(t, pool, bookingID, userID, "REFUNDED")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = repo.UpdatePaymentObligationRefunded(ctx, tx, obID, "refund-z")
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
}

// ---- CountCancellableTickets ----

func TestRepo_CountCancellableTickets_WithData(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	_, _, eventID, _ := seedPaidObligation(t, pool)
	count, err := repo.CountCancellableTickets(ctx, eventID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 0)
}

func TestRepo_CountCancellableTickets_EmptyEvent(t *testing.T) {
	_, repo := setupComplexEnv(t)
	ctx := context.Background()
	count, err := repo.CountCancellableTickets(ctx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// ---- SumRefundableAmount ----

func TestRepo_SumRefundableAmount_WithData(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	_, _, eventID, _ := seedPaidObligation(t, pool)
	total, err := repo.SumRefundableAmount(ctx, eventID)
	require.NoError(t, err)
	assert.NotEmpty(t, total)
	assert.NotEqual(t, "0", total, "seeded PAID obligation should yield a positive sum")
}

func TestRepo_SumRefundableAmount_Empty(t *testing.T) {
	_, repo := setupComplexEnv(t)
	ctx := context.Background()
	total, err := repo.SumRefundableAmount(ctx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "0", total)
}

// ---- ListLedgerEntries ----

func TestRepo_ListLedgerEntries_HappyPath(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	_, _, _, _ = seedPaidObligation(t, pool)

	entries, total, err := repo.ListLedgerEntries(ctx, "", "", "", 1, 10)
	require.NoError(t, err)
	assert.NotNil(t, entries)
	assert.GreaterOrEqual(t, total, 1)
	assert.LessOrEqual(t, len(entries), 10)
}

func TestRepo_ListLedgerEntries_DateFilter(t *testing.T) {
	_, repo := setupComplexEnv(t)
	ctx := context.Background()
	futureFrom := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	futureTo := time.Now().Add(48 * time.Hour).Format("2006-01-02")
	entries, total, err := repo.ListLedgerEntries(ctx, futureFrom, futureTo, "", 1, 10)
	require.NoError(t, err)
	assert.Equal(t, 0, len(entries))
	assert.Equal(t, 0, total)
}

func TestRepo_ListLedgerEntries_StatusFilter(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	_, _, _, _ = seedPaidObligation(t, pool)

	entries, _, err := repo.ListLedgerEntries(ctx, "", "", "PAID", 1, 10)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, "PAID", e.Status)
	}
}

func TestRepo_ListLedgerEntries_Pagination(t *testing.T) {
	_, repo := setupComplexEnv(t)
	ctx := context.Background()
	entries, _, err := repo.ListLedgerEntries(ctx, "", "", "", 1, 2)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(entries), 2)
}

func TestRepo_ListLedgerEntries_CombinedFilters(t *testing.T) {
	_, repo := setupComplexEnv(t)
	ctx := context.Background()
	from := time.Now().Add(-7 * 24 * time.Hour).Format("2006-01-02")
	to := time.Now().Format("2006-01-02")
	entries, _, err := repo.ListLedgerEntries(ctx, from, to, "PAID", 1, 5)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, "PAID", e.Status)
	}
	assert.LessOrEqual(t, len(entries), 5)
}

// ---- ComputeLedgerSummary ----

func TestRepo_ComputeLedgerSummary_WithData(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	_, _, _, _ = seedPaidObligation(t, pool)

	summary, err := repo.ComputeLedgerSummary(ctx, "", "", "")
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.NotEmpty(t, summary.TotalPaid)
	assert.NotEmpty(t, summary.TotalPending)
	assert.NotEmpty(t, summary.TotalRefunded)
	assert.GreaterOrEqual(t, summary.UnreconciledCount, 0)
}

func TestRepo_ComputeLedgerSummary_EmptyDateRange(t *testing.T) {
	_, repo := setupComplexEnv(t)
	ctx := context.Background()
	future := time.Now().Add(72 * time.Hour).Format("2006-01-02")
	future2 := time.Now().Add(96 * time.Hour).Format("2006-01-02")
	summary, err := repo.ComputeLedgerSummary(ctx, future, future2, "")
	require.NoError(t, err)
	assert.Equal(t, "0", summary.TotalPaid)
	assert.Equal(t, "0", summary.TotalPending)
	assert.Equal(t, "0", summary.TotalRefunded)
	assert.Equal(t, 0, summary.UnreconciledCount)
}

// ---- ListStuckLocks ----

func TestRepo_ListStuckLocks_FindsExpired(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, _, _ := insertBooking(t, pool, "PENDING", time.Now().Add(-20*time.Minute), "[1,2]")

	locks, err := repo.ListStuckLocks(ctx)
	require.NoError(t, err)
	require.NotNil(t, locks)
	found := false
	for _, l := range locks {
		if l.BookingID == bookingID {
			found = true
			assert.Equal(t, "STUCK", l.Status)
			assert.Greater(t, len(l.SeatPositions), 0)
			assert.Greater(t, l.DurationMinutes, 10)
			assert.NotEmpty(t, l.SectionName)
			break
		}
	}
	assert.True(t, found, "expired booking should appear in stuck locks")
}

func TestRepo_ListStuckLocks_ExcludesRecent(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, _, _ := insertBooking(t, pool, "PENDING", time.Now().Add(1*time.Hour), "[3]")

	locks, err := repo.ListStuckLocks(ctx)
	require.NoError(t, err)
	for _, l := range locks {
		assert.NotEqual(t, bookingID, l.BookingID, "fresh booking must not appear")
	}
}

func TestRepo_ListStuckLocks_ExcludesNonPending(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	// PAID but expired — must not appear (only PENDING is considered stuck)
	bookingID, _, _ := insertBooking(t, pool, "PAID", time.Now().Add(-30*time.Minute), "[4]")

	locks, err := repo.ListStuckLocks(ctx)
	require.NoError(t, err)
	for _, l := range locks {
		assert.NotEqual(t, bookingID, l.BookingID, "non-PENDING booking must not appear")
	}
}

// ---- GetBookingForUpdate ----

func TestRepo_GetBookingForUpdate_Success(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, sectionID, _ := insertBooking(t, pool, "PENDING", time.Now().Add(-15*time.Minute), "[4,5]")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	sl, err := repo.GetBookingForUpdate(ctx, tx, bookingID)
	require.NoError(t, err)
	require.NotNil(t, sl)
	assert.Equal(t, bookingID, sl.BookingID)
	assert.Equal(t, sectionID, sl.SectionID)
	assert.Greater(t, len(sl.SeatPositions), 0)
	assert.NotEmpty(t, sl.SectionName)
	assert.Greater(t, sl.DurationMinutes, 10)
	assert.Equal(t, "STUCK", sl.Status)
}

func TestRepo_GetBookingForUpdate_NotFound(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = repo.GetBookingForUpdate(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, admin.ErrAdminNotFound)
}

func TestRepo_GetBookingForUpdate_LockBehavior(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, _, _ := insertBooking(t, pool, "PENDING", time.Now().Add(-20*time.Minute), "[6]")

	tx1, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = repo.GetBookingForUpdate(ctx, tx1, bookingID)
	require.NoError(t, err)

	var gotErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tx2Ctx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
		defer cancel()
		tx2, err := pool.Begin(tx2Ctx)
		if err != nil {
			gotErr = err
			return
		}
		defer func() { _ = tx2.Rollback(tx2Ctx) }()
		_, gotErr = repo.GetBookingForUpdate(tx2Ctx, tx2, bookingID)
	}()
	wg.Wait()
	_ = tx1.Commit(ctx)

	assert.Error(t, gotErr, "second tx should block on FOR UPDATE lock")
}

// ---- UpdateBookingCancelled ----

func TestRepo_UpdateBookingCancelled_Success(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, _, _ := insertBooking(t, pool, "PENDING", time.Now().Add(-20*time.Minute), "[7]")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	require.NoError(t, repo.UpdateBookingCancelled(ctx, tx, bookingID))
	require.NoError(t, tx.Commit(ctx))

	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT payment_status::text FROM bookings WHERE id=$1`, bookingID).Scan(&status))
	assert.Equal(t, "CANCELLED", status)
}

func TestRepo_UpdateBookingCancelled_AlreadyCancelled(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, _, _ := insertBooking(t, pool, "CANCELLED", time.Now().Add(-1*time.Hour), "[8]")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = repo.UpdateBookingCancelled(ctx, tx, bookingID)
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
}

func TestRepo_UpdateBookingCancelled_PaidConflict(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	bookingID, _, _ := insertBooking(t, pool, "PAID", time.Now().Add(-1*time.Hour), "[9]")

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = repo.UpdateBookingCancelled(ctx, tx, bookingID)
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
}

func TestRepo_UpdateBookingCancelled_NotFound(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	err = repo.UpdateBookingCancelled(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, admin.ErrAdminConflict)
}

// ---- ListUsers boundary ----

func TestRepo_ListUsers_BoundaryZeroLimit(t *testing.T) {
	_, repo := setupComplexEnv(t)
	ctx := context.Background()
	users, _, err := repo.ListUsers(ctx, "", "", "", 1, 0)
	require.NoError(t, err)
	_ = users
}

// ---- ListBlacklist ----

func TestRepo_ListBlacklist_HappyPath(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()

	var adminID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM users WHERE role::text='admin' LIMIT 1`).Scan(&adminID))
	value := "test-bl-" + uuid.NewString()
	entry := &admin.BlacklistEntry{Type: "ip", Value: value, AddedBy: &adminID}
	require.NoError(t, repo.InsertBlacklist(ctx, entry))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM blacklist WHERE id=$1`, entry.ID)
	})

	entries, total, err := repo.ListBlacklist(ctx, 1, 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	found := false
	for _, e := range entries {
		if e.ID == entry.ID {
			found = true
			assert.Equal(t, value, e.Value)
			assert.Equal(t, "ip", e.Type)
		}
	}
	assert.True(t, found, "inserted blacklist entry should be listed")
}

func TestRepo_ListBlacklist_Pagination(t *testing.T) {
	_, repo := setupComplexEnv(t)
	ctx := context.Background()
	entries, _, err := repo.ListBlacklist(ctx, 1, 2)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(entries), 2)
}

// ---- InsertAdminAction ----

func TestRepo_InsertAdminAction_Success(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()

	var adminID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM users WHERE role::text='admin' LIMIT 1`).Scan(&adminID))

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	before := map[string]interface{}{"status": "PENDING"}
	after := map[string]interface{}{"status": "PAID"}
	desc := "test manual paid"
	id, err := repo.InsertAdminAction(ctx, tx, &admin.AdminActionRecord{
		AdminID:       adminID,
		ActionType:    "MANUAL_PAID",
		Description:   &desc,
		Reason:        "integration test",
		ChangesBefore: before,
		ChangesAfter:  after,
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)

	var actionType, reason string
	require.NoError(t, tx.QueryRow(ctx, `SELECT action_type::text, reason FROM admin_actions WHERE id=$1`, id).Scan(&actionType, &reason))
	assert.Equal(t, "MANUAL_PAID", actionType)
	assert.Equal(t, "integration test", reason)
	require.NoError(t, tx.Commit(ctx))

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_actions WHERE id=$1`, id)
	})
}

func TestRepo_InsertAdminAction_NilChanges(t *testing.T) {
	pool, repo := setupComplexEnv(t)
	ctx := context.Background()

	var adminID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM users WHERE role::text='admin' LIMIT 1`).Scan(&adminID))

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	id, err := repo.InsertAdminAction(ctx, tx, &admin.AdminActionRecord{
		AdminID:    adminID,
		ActionType: "FORCE_RELEASE_SEAT",
		Reason:     "nil changes path",
	})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
	require.NoError(t, tx.Commit(ctx))

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_actions WHERE id=$1`, id)
	})
}
