package event

// FIX P9a-10 (TD-F6-09 / TD-F9-01): unit coverage for the event SQL layer.
// The repository used a concrete *pgxpool.Pool, so every SQL method sat at
// 0% coverage. With the DBPool seam the repository is now exercised through
// an in-memory pgx fake — no live PostgreSQL required.

import (
	"context"
	"database/sql"
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

func p9a10EventRow(eventID, organizerID uuid.UUID, publishedAt *time.Time) []any {
	return []any{
		eventID, organizerID, "Flash Sale", sql.NullString{String: "desc", Valid: true},
		"Velodrome", "Jl. Merdeka 1", "2026-12-01", "10:00:00",
		sql.NullString{String: "14:00:00", Valid: true}, "PUBLISHED",
		sql.NullString{String: "https://cdn/banner.png", Valid: true},
		3, time.Now(), time.Now(), publishedAt,
	}
}

func TestP9a10Repo_GetOrganizerID_Success(t *testing.T) {
	organizerID := uuid.New()
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: []any{organizerID}}}}
	repo := NewPostgresEventRepository(pool)

	got, err := repo.GetOrganizerID(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, organizerID, got)
}

func TestP9a10Repo_GetOrganizerID_NotFound(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: pgx.ErrNoRows}}}
	repo := NewPostgresEventRepository(pool)

	_, err := repo.GetOrganizerID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event not found")
}

func TestP9a10Repo_GetOrganizerID_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresEventRepository(pool)

	_, err := repo.GetOrganizerID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get organizer id")
}

func TestP9a10Repo_ListEvents_Success(t *testing.T) {
	eventID := uuid.New()
	organizerID := uuid.New()
	published := time.Now()
	pool := &p9a10FakePool{
		setResults: []p9a10SetResult{{rows: [][]any{p9a10EventRow(eventID, organizerID, &published)}}},
		rowResults: []p9a10RowResult{{values: []any{1}}},
	}
	repo := NewPostgresEventRepository(pool)

	events, total, err := repo.ListEvents(context.Background(), ListFilter{Page: 1, Limit: 10})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, eventID, events[0].ID)
	assert.Equal(t, organizerID, events[0].OrganizerID)
	assert.Equal(t, "Flash Sale", events[0].Name)
	assert.Equal(t, "desc", events[0].Description)
	assert.Equal(t, "Velodrome", events[0].VenueName)
	assert.Equal(t, "2026-12-01", events[0].EventDate)
	assert.Equal(t, "10:00:00", events[0].EventStartTime)
	assert.Equal(t, "14:00:00", events[0].EventEndTime)
	assert.Equal(t, "PUBLISHED", events[0].Status)
	assert.Equal(t, "https://cdn/banner.png", events[0].BannerURL)
	assert.Equal(t, 3, events[0].SeatLayoutVersion)
	require.NotNil(t, events[0].PublishedAt)
}

func TestP9a10Repo_ListEvents_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresEventRepository(pool)

	_, _, err := repo.ListEvents(context.Background(), ListFilter{Page: 1, Limit: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query events")
}

func TestP9a10Repo_ListEvents_ScanError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{{}}}}}
	repo := NewPostgresEventRepository(pool)

	_, _, err := repo.ListEvents(context.Background(), ListFilter{Page: 1, Limit: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scan event")
}

func TestP9a10Repo_ListEvents_CountError(t *testing.T) {
	pool := &p9a10FakePool{
		setResults: []p9a10SetResult{{rows: [][]any{}}},
		rowResults: []p9a10RowResult{{err: errors.New("boom")}},
	}
	repo := NewPostgresEventRepository(pool)

	_, _, err := repo.ListEvents(context.Background(), ListFilter{Page: 1, Limit: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "count events")
}

func TestP9a10Repo_GetEventByID_Success(t *testing.T) {
	eventID := uuid.New()
	organizerID := uuid.New()
	row := append(p9a10EventRow(eventID, organizerID, nil), "Organizer Name")
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{values: row}}}
	repo := NewPostgresEventRepository(pool)

	got, err := repo.GetEventByID(context.Background(), eventID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, eventID, got.ID)
	assert.Equal(t, "Organizer Name", got.OrganizerName)
	assert.Nil(t, got.PublishedAt)
}

func TestP9a10Repo_GetEventByID_NotFound(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: pgx.ErrNoRows}}}
	repo := NewPostgresEventRepository(pool)

	_, err := repo.GetEventByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Equal(t, pgx.ErrNoRows, err)
}

func TestP9a10Repo_GetEventByID_Error(t *testing.T) {
	pool := &p9a10FakePool{rowResults: []p9a10RowResult{{err: errors.New("boom")}}}
	repo := NewPostgresEventRepository(pool)

	_, err := repo.GetEventByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get event by id")
}

func TestP9a10Repo_GetSectionsByEventID_Success(t *testing.T) {
	eventID := uuid.New()
	sectionID := uuid.New()
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{sectionID, eventID, "VIP", "150000.00", 100, 2, 40},
	}}}}
	repo := NewPostgresEventRepository(pool)

	sections, err := repo.GetSectionsByEventID(context.Background(), eventID)
	require.NoError(t, err)
	require.Len(t, sections, 1)
	assert.Equal(t, sectionID, sections[0].ID)
	assert.Equal(t, "VIP", sections[0].Name)
	assert.Equal(t, "150000.00", sections[0].Price)
	assert.Equal(t, 100, sections[0].TotalSeats)
	assert.Equal(t, 2, sections[0].LayoutVersion)
	assert.Equal(t, 40, sections[0].AvailableSeats)
}

func TestP9a10Repo_GetSectionsByEventID_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresEventRepository(pool)

	_, err := repo.GetSectionsByEventID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query sections")
}

func TestP9a10Repo_GetSectionsByEventIDs_GroupedByEvent(t *testing.T) {
	eventA := uuid.New()
	eventB := uuid.New()
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{rows: [][]any{
		{uuid.New(), eventA, "A1", "100.00", 10, 1, 5},
		{uuid.New(), eventB, "B1", "200.00", 20, 1, 15},
		{uuid.New(), eventA, "A2", "300.00", 30, 1, 25},
	}}}}
	repo := NewPostgresEventRepository(pool)

	got, err := repo.GetSectionsByEventIDs(context.Background(), []uuid.UUID{eventA, eventB})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Len(t, got[eventA], 2)
	assert.Len(t, got[eventB], 1)
}

func TestP9a10Repo_GetSectionsByEventIDs_EmptyInputSkipsQuery(t *testing.T) {
	pool := &p9a10FakePool{}
	repo := NewPostgresEventRepository(pool)

	got, err := repo.GetSectionsByEventIDs(context.Background(), []uuid.UUID{})
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Len(t, got, 0)
	assert.Len(t, pool.sqlLog, 0)
}

func TestP9a10Repo_GetSectionsByEventIDs_QueryError(t *testing.T) {
	pool := &p9a10FakePool{setResults: []p9a10SetResult{{err: errors.New("boom")}}}
	repo := NewPostgresEventRepository(pool)

	_, err := repo.GetSectionsByEventIDs(context.Background(), []uuid.UUID{uuid.New()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query sections by events")
}

func TestP9a10Repo_ScanEvent_PureMapping(t *testing.T) {
	eventID := uuid.New()
	organizerID := uuid.New()
	published := time.Now()

	got, err := scanEvent(&p9a10FakeRow{values: p9a10EventRow(eventID, organizerID, &published)})
	require.NoError(t, err)
	assert.Equal(t, eventID, got.ID)
	assert.Equal(t, "Flash Sale", got.Name)
	assert.Equal(t, "desc", got.Description)
	assert.Equal(t, "https://cdn/banner.png", got.BannerURL)
	require.NotNil(t, got.PublishedAt)
}

func TestP9a10Repo_ScanEvent_ScanError(t *testing.T) {
	_, err := scanEvent(&p9a10FakeRow{err: errors.New("boom")})
	require.Error(t, err)
}

func TestP9a10Repo_ScanEventWithOrganizer_PureMapping(t *testing.T) {
	eventID := uuid.New()
	organizerID := uuid.New()
	row := append(p9a10EventRow(eventID, organizerID, nil), "Organizer")

	got, err := scanEventWithOrganizer(&p9a10FakeRow{values: row})
	require.NoError(t, err)
	assert.Equal(t, "Organizer", got.OrganizerName)
	assert.Equal(t, "desc", got.Description)
}

func TestP9a10Repo_ScanEventWithOrganizer_ScanError(t *testing.T) {
	_, err := scanEventWithOrganizer(&p9a10FakeRow{err: errors.New("boom")})
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
