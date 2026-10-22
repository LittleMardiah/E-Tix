package analytics

// FIX P9a-10 (TD-F6-09 / TD-F9-01): unit coverage for the analytics SQL layer.
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

func TestP9a10Repo_SalesAndGateSummary_Success(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{7, 10, "125000"}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	got, err := repo.SalesAndGateSummary(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 7, got.TicketsSold)
	assert.Equal(t, 10, got.TotalTickets)
	assert.InDelta(t, 70.0, got.SalesPercentage, 0.0001)
	assert.Equal(t, "125000", got.RevenueToday)
	assert.Equal(t, "125000", got.TotalRevenue)
}

func TestP9a10Repo_SalesAndGateSummary_ZeroTotal(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{0, 0, "0"}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	got, err := repo.SalesAndGateSummary(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 0.0, got.SalesPercentage)
}

func TestP9a10Repo_SalesAndGateSummary_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.SalesAndGateSummary(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: sales")
}

func TestP9a10Repo_GateStatusToday_ActiveAndIdle(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{"GATE-A", 5},
		{"GATE-B", 0},
	}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	gates, count, err := repo.GateStatusToday(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"GATE-A": "ACTIVE", "GATE-B": "IDLE"}, gates)
	assert.Equal(t, 2, count)
}

func TestP9a10Repo_GateStatusToday_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, _, err := repo.GateStatusToday(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: gate status")
}

func TestP9a10Repo_TotalScansToday_Success(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{42}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	n, err := repo.TotalScansToday(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 42, n)
}

func TestP9a10Repo_TotalScansToday_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.TotalScansToday(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: total scans")
}

func TestP9a10Repo_AntiBotStats_StaticZeroBlock(t *testing.T) {
	repo := NewPostgresAnalyticsRepository(&p9a10FakePool{})

	got, err := repo.AntiBotStats(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, &AntiBotBlock{}, got)
}

func TestP9a10Repo_RecentActivity_Success(t *testing.T) {
	ts := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{ts, "VALID", "GATE-A"},
		{ts, "INVALID", "GATE-B"},
	}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	items, err := repo.RecentActivity(context.Background(), uuid.New(), 10)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "TICKET_SCANNED", items[0].Type)
	assert.Equal(t, "VALID at GATE-A", items[0].Description)
	assert.Equal(t, ts, items[0].Timestamp)
	assert.Equal(t, "INVALID at GATE-B", items[1].Description)
}

func TestP9a10Repo_RecentActivity_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.RecentActivity(context.Background(), uuid.New(), 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: recent")
}

func TestP9a10Repo_AttendanceMetrics_Success(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{10, 6, 4}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	got, err := repo.AttendanceMetrics(context.Background(), uuid.New())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 10, got.TotalTickets)
	assert.Equal(t, 6, got.UsedTickets)
	assert.Equal(t, 4, got.UnusedTickets)
	assert.InDelta(t, 40.0, got.NoShowRate, 0.0001)
}

func TestP9a10Repo_AttendanceMetrics_ZeroTotal(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{0, 0, 0}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	got, err := repo.AttendanceMetrics(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 0.0, got.NoShowRate)
}

func TestP9a10Repo_AttendanceMetrics_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.AttendanceMetrics(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: attendance")
}

func TestP9a10Repo_GateUtilization_Success(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{"GATE-A", 10, 8},
		{"GATE-B", 5, 5},
	}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	got, err := repo.GateUtilization(context.Background(), uuid.New())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 10, got["GATE-A"].TotalScans)
	assert.Equal(t, 8, got["GATE-A"].ValidScans)
	assert.Equal(t, 100, got["GATE-A"].Utilization)
	assert.Equal(t, 50, got["GATE-B"].Utilization)
}

func TestP9a10Repo_GateUtilization_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.GateUtilization(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: gate util")
}

func TestP9a10Repo_ScanTimeline_Success(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{"09:00", 12},
		{"10:00", 30},
	}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	got, err := repo.ScanTimeline(context.Background(), uuid.New())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "09:00", got[0].Hour)
	assert.Equal(t, 30, got[1].Scans)
}

func TestP9a10Repo_ScanTimeline_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.ScanTimeline(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: timeline")
}

func TestP9a10Repo_LoyaltySplit_Success(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{3, 9}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	loyal, fresh, err := repo.LoyaltySplit(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 3, loyal)
	assert.Equal(t, 9, fresh)
}

func TestP9a10Repo_LoyaltySplit_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, _, err := repo.LoyaltySplit(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: loyalty")
}

func TestP9a10Repo_ReportGatePerformance_Success(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{"GATE-A", 10, 8, 2},
	}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	got, err := repo.ReportGatePerformance(context.Background(), uuid.New())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 10, got["GATE-A"].TotalScans)
	assert.Equal(t, 8, got["GATE-A"].ValidScans)
	assert.Equal(t, 2, got["GATE-A"].InvalidScans)
}

func TestP9a10Repo_ReportGatePerformance_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.ReportGatePerformance(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: gate perf")
}

func TestP9a10Repo_InsertInsight_Success(t *testing.T) {
	pool := &p9a10FakePool{}
	repo := NewPostgresAnalyticsRepository(pool)

	err := repo.InsertInsight(context.Background(), &InsightRecord{
		EventID:        uuid.New(),
		InsightType:    "PEAK_TIME",
		Title:          "Peak hour",
		Description:    "12:00-13:00",
		Recommendation: []byte("{}"),
		GeneratedAt:    time.Now(),
	})
	require.NoError(t, err)
	require.Len(t, pool.sqlLog, 1)
	assert.Contains(t, pool.sqlLog[0], "INSERT INTO insights")
}

func TestP9a10Repo_InsertInsight_Error(t *testing.T) {
	pool := &p9a10FakePool{execErr: errors.New("boom")}
	repo := NewPostgresAnalyticsRepository(pool)

	err := repo.InsertInsight(context.Background(), &InsightRecord{EventID: uuid.New()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: insert insight")
}

func TestP9a10Repo_ExistsByEventAndType_True(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{true}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	exists, err := repo.ExistsByEventAndType(context.Background(), uuid.New(), "PEAK_TIME")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestP9a10Repo_ExistsByEventAndType_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.ExistsByEventAndType(context.Background(), uuid.New(), "PEAK_TIME")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: exists")
}

func TestP9a10Repo_ListByEvent_Success(t *testing.T) {
	insightID := uuid.New()
	eventID := uuid.New()
	generated := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	expires := generated.Add(24 * time.Hour)
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{insightID, eventID, "PEAK_TIME", "Peak hour", "12:00-13:00", []byte("{\"a\":1}"), true, generated, &expires},
	}}}}
	repo := NewPostgresAnalyticsRepository(pool)

	got, err := repo.ListByEvent(context.Background(), eventID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, insightID, got[0].ID)
	assert.Equal(t, eventID, got[0].EventID)
	assert.Equal(t, "PEAK_TIME", got[0].InsightType)
	assert.Equal(t, "Peak hour", got[0].Title)
	assert.True(t, got[0].IsApplied)
	require.NotNil(t, got[0].ExpiresAt)
	assert.Equal(t, expires, *got[0].ExpiresAt)
}

func TestP9a10Repo_ListByEvent_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresAnalyticsRepository(pool)

	_, err := repo.ListByEvent(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analytics: list insights")
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

func TestP9a10Fake_RowsScanWithoutNext(t *testing.T) {
	rows := &p9a10FakeRows{rows: [][]any{{1}}}
	err := rows.Scan(new(int))
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
