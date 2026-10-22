package admin

// FIX P9a-10 (TD-F6-09 / TD-F9-01): unit coverage for the admin SQL layer.
// The repository used a concrete *pgxpool.Pool, so every SQL method sat at
// 0% coverage. With the DBPool seam the repository is now exercised through
// an in-memory pgx fake — no live PostgreSQL required.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// P9a-10 test doubles — in-memory pgx fake (pool / rows / row).
//
// The repository depends on the DBPool interface (repository.go), so the SQL
// layer is unit-testable without a live PostgreSQL server.
// ---------------------------------------------------------------------------

type p9a10RowResult struct {
	values []any
	err    error
}

type p9a10SetResult struct {
	rows [][]any
	err  error
}

type p9a10FakePool struct {
	rowResults []p9a10RowResult
	setResults []p9a10SetResult
	execErr    error
	execTag    pgconn.CommandTag
	sqlLog     []string
}

var _ DBPool = (*p9a10FakePool)(nil)

func (p *p9a10FakePool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	p.sqlLog = append(p.sqlLog, sql)
	if len(p.rowResults) == 0 {
		return &p9a10FakeRow{err: pgx.ErrNoRows}
	}
	res := p.rowResults[0]
	p.rowResults = p.rowResults[1:]
	return &p9a10FakeRow{values: res.values, err: res.err}
}

func (p *p9a10FakePool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	p.sqlLog = append(p.sqlLog, sql)
	if len(p.setResults) == 0 {
		return &p9a10FakeRows{}, nil
	}
	res := p.setResults[0]
	p.setResults = p.setResults[1:]
	if res.err != nil {
		return nil, res.err
	}
	return &p9a10FakeRows{rows: res.rows}, nil
}

func (p *p9a10FakePool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	p.sqlLog = append(p.sqlLog, sql)
	if p.execErr != nil {
		return pgconn.CommandTag{}, p.execErr
	}
	return p.execTag, nil
}

type p9a10FakeRow struct {
	values []any
	err    error
}

func (r *p9a10FakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return p9a10ScanRow(r.values, dest)
}

type p9a10FakeRows struct {
	rows [][]any
	idx  int
	cur  []any
}

func (r *p9a10FakeRows) Close() {}

func (r *p9a10FakeRows) Err() error { return nil }

func (r *p9a10FakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *p9a10FakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *p9a10FakeRows) Next() bool {
	if r.idx >= len(r.rows) {
		r.cur = nil
		return false
	}
	r.cur = r.rows[r.idx]
	r.idx++
	return true
}

func (r *p9a10FakeRows) Scan(dest ...any) error {
	if r.cur == nil {
		return pgx.ErrNoRows
	}
	return p9a10ScanRow(r.cur, dest)
}

func (r *p9a10FakeRows) Values() ([]any, error) { return r.cur, nil }

func (r *p9a10FakeRows) RawValues() [][]byte { return nil }

func (r *p9a10FakeRows) Conn() *pgx.Conn { return nil }

func (r *p9a10FakeRows) TypeMap() *pgtype.Map { return nil }

func p9a10ScanRow(values []any, dest []any) error {
	if len(values) != len(dest) {
		return fmt.Errorf("p9a10: scan arity mismatch: %d values for %d dest", len(values), len(dest))
	}
	for i, d := range dest {
		if err := p9a10Assign(d, values[i]); err != nil {
			return err
		}
	}
	return nil
}

func p9a10Assign(dest any, val any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Ptr || dv.IsNil() {
		return fmt.Errorf("p9a10: dest must be a non-nil pointer, got %T", dest)
	}
	elem := dv.Elem()
	if val == nil {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}
	vv := reflect.ValueOf(val)
	if vv.Type().AssignableTo(elem.Type()) {
		elem.Set(vv)
		return nil
	}
	if vv.Kind() == elem.Kind() && vv.Type().ConvertibleTo(elem.Type()) {
		elem.Set(vv.Convert(elem.Type()))
		return nil
	}
	return fmt.Errorf("p9a10: cannot assign %T into %s", val, elem.Type())
}

func p9a10AdminUserRow(userID uuid.UUID) []any {
	return []any{
		userID, "buyer@example.com", "08120000", "Buyer", "customer", "ACTIVE",
		50, 3, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC),
	}
}

func TestP9a10Repo_GetUserByID_Success(t *testing.T) {
	userID := uuid.New()
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: p9a10AdminUserRow(userID)}}}
	repo := NewPostgresAdminRepository(pool)

	got, err := repo.GetUserByID(context.Background(), userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, userID, got.UserID)
	assert.Equal(t, "buyer@example.com", got.Email)
	assert.Equal(t, "08120000", got.Phone)
	assert.Equal(t, "Buyer", got.Name)
	assert.Equal(t, "customer", got.Role)
	assert.Equal(t, "ACTIVE", got.Status)
	assert.Equal(t, 3, got.TotalPurchases)
	assert.Equal(t, "TIER_2", got.LoyaltyTier)
}

func TestP9a10Repo_GetUserByID_NotFound(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: pgx.ErrNoRows}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.GetUserByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Equal(t, ErrAdminNotFound, err)
}

func TestP9a10Repo_GetUserByID_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.GetUserByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestP9a10Repo_ListUsers_Success(t *testing.T) {
	userID := uuid.New()
	pool := &p9a10FakePool{
		rowResults: []p9a10RowResult{{values: []any{1}}},
		setResults: []p9a10SetResult{{rows: [][]any{p9a10AdminUserRow(userID)}}},
	}
	repo := NewPostgresAdminRepository(pool)

	users, total, err := repo.ListUsers(context.Background(), "", "", "", 1, 10)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, userID, users[0].UserID)
	assert.Equal(t, 3, users[0].TotalPurchases)
}

func TestP9a10Repo_ListUsers_AppliesFilters(t *testing.T) {
	pool := &p9a10FakePool{
		rowResults: []p9a10RowResult{{values: []any{0}}},
		setResults: []p9a10SetResult{{rows: [][]any{}}},
	}
	repo := NewPostgresAdminRepository(pool)

	_, total, err := repo.ListUsers(context.Background(), "organizer", "SUSPENDED", "buyer", 2, 25)
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	require.Len(t, pool.sqlLog, 2)
	assert.Contains(t, pool.sqlLog[0], "role::text = $1")
	assert.Contains(t, pool.sqlLog[0], "status::text = $2")
	assert.Contains(t, pool.sqlLog[0], "ILIKE $3")
}

func TestP9a10Repo_ListUsers_CountError(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, _, err := repo.ListUsers(context.Background(), "", "", "", 1, 10)
	require.Error(t, err)
}

func TestP9a10Repo_ListUsers_RowsError(t *testing.T) {
	pool := &p9a10FakePool{
		rowResults: []p9a10RowResult{{values: []any{0}}},
		setResults: []p9a10SetResult{{err: errors.New("boom")}},
	}
	repo := NewPostgresAdminRepository(pool)

	_, _, err := repo.ListUsers(context.Background(), "", "", "", 1, 10)
	require.Error(t, err)
}

func TestP9a10Repo_ListBlacklist_Success(t *testing.T) {
	entryID := uuid.New()
	addedBy := uuid.New()
	reason := "brute force"
	expires := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	pool := &p9a10FakePool{
		rowResults: []p9a10RowResult{{values: []any{1}}},
		setResults: []p9a10SetResult{{rows: [][]any{
			{entryID, "ip", "10.0.0.1", &reason, &addedBy, time.Now(), &expires},
		}}},
	}
	repo := NewPostgresAdminRepository(pool)

	entries, total, err := repo.ListBlacklist(context.Background(), 1, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, entryID, entries[0].ID)
	assert.Equal(t, "ip", entries[0].Type)
	require.NotNil(t, entries[0].Reason)
	assert.Equal(t, reason, *entries[0].Reason)
	require.NotNil(t, entries[0].AddedBy)
	assert.Equal(t, addedBy, *entries[0].AddedBy)
	require.NotNil(t, entries[0].ExpiresAt)
}

func TestP9a10Repo_ListBlacklist_CountError(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, _, err := repo.ListBlacklist(context.Background(), 1, 10)
	require.Error(t, err)
}

func TestP9a10Repo_ListBlacklist_RowsError(t *testing.T) {
	pool := &p9a10FakePool{
		rowResults: []p9a10RowResult{{values: []any{0}}},
		setResults: []p9a10SetResult{{err: errors.New("boom")}},
	}
	repo := NewPostgresAdminRepository(pool)

	_, _, err := repo.ListBlacklist(context.Background(), 1, 10)
	require.Error(t, err)
}

func TestP9a10Repo_InsertBlacklist_Success(t *testing.T) {
	entryID := uuid.New()
	addedAt := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{entryID, addedAt}}}}
	repo := NewPostgresAdminRepository(pool)

	entry := &BlacklistEntry{Type: "ip", Value: "10.0.0.1"}
	err := repo.InsertBlacklist(context.Background(), entry)
	require.NoError(t, err)
	assert.Equal(t, entryID, entry.ID)
	assert.Equal(t, addedAt, entry.AddedAt)
}

func TestP9a10Repo_InsertBlacklist_DuplicateIsConflict(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: &pgconn.PgError{Code: "23505"}}}}
	repo := NewPostgresAdminRepository(pool)

	err := repo.InsertBlacklist(context.Background(), &BlacklistEntry{Type: "ip", Value: "10.0.0.1"})
	require.Error(t, err)
	assert.Equal(t, ErrAdminConflict, err)
}

func TestP9a10Repo_InsertBlacklist_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	err := repo.InsertBlacklist(context.Background(), &BlacklistEntry{Type: "ip", Value: "10.0.0.1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestP9a10Repo_DeleteBlacklist_Success(t *testing.T) {
	entryID := uuid.New()
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{entryID}}}}
	repo := NewPostgresAdminRepository(pool)

	got, err := repo.DeleteBlacklist(context.Background(), entryID)
	require.NoError(t, err)
	assert.Equal(t, entryID, got)
}

func TestP9a10Repo_DeleteBlacklist_NotFound(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: pgx.ErrNoRows}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.DeleteBlacklist(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Equal(t, ErrAdminNotFound, err)
}

func TestP9a10Repo_DeleteBlacklist_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.DeleteBlacklist(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestP9a10Repo_ListLedgerEntries_Success(t *testing.T) {
	eventID := uuid.New()
	referenceID := uuid.New()
	bookingID := uuid.New()
	userID := uuid.New()
	adminID := uuid.New()
	gatewayTx := "tx-123"
	pool := &p9a10FakePool{
		setResults: []p9a10SetResult{{rows: [][]any{
			{eventID, "PAYMENT_PAID", "payment_obligation", referenceID,
				&bookingID, &userID, "250000.00", "PAID", &gatewayTx, &adminID, true, time.Now()},
		}}},
		rowResults: []p9a10RowResult{{values: []any{1}}},
	}
	repo := NewPostgresAdminRepository(pool)

	entries, total, err := repo.ListLedgerEntries(context.Background(), "", "", "", 1, 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, eventID, entries[0].EventID)
	assert.Equal(t, "PAYMENT_PAID", entries[0].EventType)
	require.NotNil(t, entries[0].BookingID)
	assert.Equal(t, bookingID, *entries[0].BookingID)
	require.NotNil(t, entries[0].GatewayTransactionID)
	assert.Equal(t, gatewayTx, *entries[0].GatewayTransactionID)
	assert.True(t, entries[0].Reconciled)
}

func TestP9a10Repo_ListLedgerEntries_RowsError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, _, err := repo.ListLedgerEntries(context.Background(), "", "", "", 1, 10)
	require.Error(t, err)
}

func TestP9a10Repo_ListLedgerEntries_CountError(t *testing.T) {
	pool := &p9a10FakePool{
		setResults: []p9a10SetResult{{rows: [][]any{}}},
		rowResults: []p9a10RowResult{{err: errors.New("boom")}},
	}
	repo := NewPostgresAdminRepository(pool)

	_, _, err := repo.ListLedgerEntries(context.Background(), "", "", "", 1, 10)
	require.Error(t, err)
}

func TestP9a10Repo_ComputeLedgerSummary_Success(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{"1000.00", "200.00", "50.00", 3}}}}
	repo := NewPostgresAdminRepository(pool)

	got, err := repo.ComputeLedgerSummary(context.Background(), "", "", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "1000.00", got.TotalPaid)
	assert.Equal(t, "200.00", got.TotalPending)
	assert.Equal(t, "50.00", got.TotalRefunded)
	assert.Equal(t, 3, got.UnreconciledCount)
}

func TestP9a10Repo_ComputeLedgerSummary_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.ComputeLedgerSummary(context.Background(), "", "", "")
	require.Error(t, err)
}

func TestP9a10Repo_ListStuckLocks_Success(t *testing.T) {
	bookingID := uuid.New()
	eventID := uuid.New()
	sectionID := uuid.New()
	expires := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{bookingID, eventID, sectionID, "VIP", 2, []byte("[1,2]"), expires},
	}}}}
	repo := NewPostgresAdminRepository(pool)

	locks, err := repo.ListStuckLocks(context.Background())
	require.NoError(t, err)
	require.Len(t, locks, 1)
	assert.Equal(t, bookingID, locks[0].BookingID)
	assert.Equal(t, "VIP", locks[0].SectionName)
	assert.Equal(t, []int{1, 2}, locks[0].SeatPositions)
	assert.Equal(t, "STUCK", locks[0].Status)
}

func TestP9a10Repo_ListStuckLocks_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.ListStuckLocks(context.Background())
	require.Error(t, err)
}

func TestP9a10Repo_ListStuckLocks_InvalidSeatJSON(t *testing.T) {
	bookingID := uuid.New()
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{bookingID, uuid.New(), uuid.New(), "VIP", 2, []byte("not-json"), time.Now()},
	}}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.ListStuckLocks(context.Background())
	require.Error(t, err)
}

func TestP9a10Repo_CountCancellableTickets_Success(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{12}}}}
	repo := NewPostgresAdminRepository(pool)

	got, err := repo.CountCancellableTickets(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 12, got)
}

func TestP9a10Repo_CountCancellableTickets_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.CountCancellableTickets(context.Background(), uuid.New())
	require.Error(t, err)
}

func TestP9a10Repo_SumRefundableAmount_Success(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{"750000.00"}}}}
	repo := NewPostgresAdminRepository(pool)

	got, err := repo.SumRefundableAmount(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "750000.00", got)
}

func TestP9a10Repo_SumRefundableAmount_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAdminRepository(pool)

	_, err := repo.SumRefundableAmount(context.Background(), uuid.New())
	require.Error(t, err)
}

func TestP9a10Fake_ScanArityMismatchIsReported(t *testing.T) {
	err := p9a10ScanRow([]any{1}, []any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "arity mismatch")
}

func TestP9a10Fake_AssignRejectsIncompatibleType(t *testing.T) {
	var target int
	err := p9a10Assign(&target, "not-an-int")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot assign")
}

func TestP9a10Fake_AssignNilZeroesTarget(t *testing.T) {
	target := "keep"
	require.NoError(t, p9a10Assign(&target, nil))
	assert.Equal(t, "", target)
}

func TestP9a10Fake_AssignRequiresPointer(t *testing.T) {
	err := p9a10Assign("not-a-pointer", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-nil pointer")
}

func TestP9a10Fake_ExecReturnsCommandTag(t *testing.T) {
	pool := &p9a10FakePool{execTag: pgconn.NewCommandTag("INSERT 0 1")}
	tag, err := pool.Exec(context.Background(), "SELECT 1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), tag.RowsAffected())
}

func TestP9a10Fake_QueryRowDefaultsToNoRows(t *testing.T) {
	pool := &p9a10FakePool{}
	err := pool.QueryRow(context.Background(), "SELECT 1").Scan(new(int))
	require.Error(t, err)
	assert.Equal(t, pgx.ErrNoRows, err)
}

func TestP9a10Fake_SQLLogCapturesStatements(t *testing.T) {
	pool := &p9a10FakePool{}
	_, _ = pool.Query(context.Background(), "SELECT 1")
	_ = pool.QueryRow(context.Background(), "SELECT 2")
	_, _ = pool.Exec(context.Background(), "SELECT 3")
	assert.Equal(t, 3, len(pool.sqlLog))
	assert.Equal(t, fmt.Sprintf("SELECT %d", 1), pool.sqlLog[0])
}
