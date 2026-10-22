package gate

// FIX P9a-10 (TD-F6-09 / TD-F9-01): unit coverage for the gate SQL layer.
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

func TestP9a10Repo_GetTicketByID_Success(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{
		ticketID, ownerID, "Buyer", "VIP", 7, "CONFIRMED", "qr-secret", "123456", false,
	}}}}
	repo := NewPostgresGateRepository(pool)

	got, err := repo.GetTicketByID(context.Background(), ticketID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, ticketID, got.ID)
	assert.Equal(t, ownerID, got.OwnerUserID)
	assert.Equal(t, "Buyer", got.OwnerName)
	assert.Equal(t, "VIP", got.SectionName)
	assert.Equal(t, 7, got.SeatPos)
	assert.Equal(t, "CONFIRMED", got.Status)
	assert.Equal(t, "qr-secret", got.QRSecretKey)
	assert.Equal(t, "123456", got.FallbackPIN)
	assert.False(t, got.FallbackPINUsed)
}

func TestP9a10Repo_GetTicketByID_NotFound(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: pgx.ErrNoRows}}}
	repo := NewPostgresGateRepository(pool)

	_, err := repo.GetTicketByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Equal(t, ErrTicketNotFound, err)
}

func TestP9a10Repo_GetTicketByID_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresGateRepository(pool)

	_, err := repo.GetTicketByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gate: get ticket")
}

func TestP9a10Repo_InsertScan_Success(t *testing.T) {
	pool := &p9a10FakePool{}
	repo := NewPostgresGateRepository(pool)
	deviceID := uuid.New()

	err := repo.InsertScan(context.Background(), &ScanRecord{
		ID:         uuid.New(),
		TicketID:   uuid.New(),
		GateID:     "GATE-A",
		ScanStatus: "VALID",
		ScanMethod: "QR",
		ScannedAt:  time.Now(),
	})
	require.NoError(t, err)
	require.Len(t, pool.sqlLog, 1)
	assert.Contains(t, pool.sqlLog[0], "INSERT INTO ticket_scans")
	assert.NotEqual(t, uuid.Nil, deviceID)
}

func TestP9a10Repo_InsertScan_Error(t *testing.T) {
	pool := &p9a10FakePool{execErr: errors.New("boom")}
	repo := NewPostgresGateRepository(pool)

	err := repo.InsertScan(context.Background(), &ScanRecord{ID: uuid.New()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gate: insert scan")
}

func TestP9a10Repo_EarliestValidScan_Success(t *testing.T) {
	scanID := uuid.New()
	ticketID := uuid.New()
	deviceID := uuid.New()
	userID := uuid.New()
	ip := "10.0.0.1"
	scannedAt := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{
		scanID, ticketID, "GATE-A", &deviceID, &userID, "VALID", "QR", scannedAt, &ip,
	}}}}
	repo := NewPostgresGateRepository(pool)

	got, err := repo.EarliestValidScan(context.Background(), ticketID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, scanID, got.ID)
	assert.Equal(t, ticketID, got.TicketID)
	assert.Equal(t, "GATE-A", got.GateID)
	require.NotNil(t, got.ScannerDeviceID)
	assert.Equal(t, deviceID, *got.ScannerDeviceID)
	require.NotNil(t, got.ScannerUserID)
	assert.Equal(t, userID, *got.ScannerUserID)
	assert.Equal(t, "VALID", got.ScanStatus)
	assert.Equal(t, scannedAt, got.ScannedAt)
	require.NotNil(t, got.IPAddress)
	assert.Equal(t, ip, *got.IPAddress)
}

func TestP9a10Repo_EarliestValidScan_NoValidScan(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: pgx.ErrNoRows}}}
	repo := NewPostgresGateRepository(pool)

	got, err := repo.EarliestValidScan(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestP9a10Repo_EarliestValidScan_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresGateRepository(pool)

	_, err := repo.EarliestValidScan(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gate: earliest valid scan")
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
