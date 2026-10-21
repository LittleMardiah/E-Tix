package event

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Event catalog tests — P9a-5
// The whole event package (catalog endpoints) had 0% coverage.
// Covers: EventService.List / GetByID, buildEventSummary,
// formatDecimal, formatHHMM, ValidateStatus, ValidateSortBy,
// buildListWhere, buildOrderBy, and both HTTP handlers.
// ============================================================

// fakeEventRepo implements EventRepository with programmable results.
type fakeEventRepo struct {
	events   []Event
	total    int
	listErr  error
	byID     *Event
	byIDErr  error
	sections []Section
	sectErr  error
	byIDs    map[uuid.UUID][]Section
	byIDsErr error
}

func (f *fakeEventRepo) ListEvents(_ context.Context, _ ListFilter) ([]Event, int, error) {
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	return f.events, f.total, nil
}

func (f *fakeEventRepo) GetEventByID(_ context.Context, _ uuid.UUID) (*Event, error) {
	if f.byIDErr != nil {
		return nil, f.byIDErr
	}
	if f.byID == nil {
		return nil, pgx.ErrNoRows
	}
	return f.byID, nil
}

func (f *fakeEventRepo) GetSectionsByEventID(_ context.Context, _ uuid.UUID) ([]Section, error) {
	if f.sectErr != nil {
		return nil, f.sectErr
	}
	return f.sections, nil
}

func (f *fakeEventRepo) GetSectionsByEventIDs(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]Section, error) {
	if f.byIDsErr != nil {
		return nil, f.byIDsErr
	}
	if f.byIDs == nil {
		return map[uuid.UUID][]Section{}, nil
	}
	return f.byIDs, nil
}

// testEvent builds an Event row.
func testEvent(id uuid.UUID) Event {
	return Event{
		ID:                id,
		OrganizerID:       uuid.New(),
		OrganizerName:     "Organizer Satu",
		Name:              "Konser Jazz",
		Description:       "A night of jazz",
		VenueName:         "Gedung Kesenian",
		VenueAddress:      "Jl. Merdeka 1",
		EventDate:         "2026-12-31",
		EventStartTime:    "19:00:00",
		EventEndTime:      "23:30:00",
		Status:            "ON_SALE",
		BannerURL:         "https://cdn.test/banner.jpg",
		SeatLayoutVersion: 2,
		CreatedAt:         time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

// testSection builds a Section row.
func testSection(eventID uuid.UUID, name, price string, total, avail int) Section {
	return Section{
		ID:             uuid.New(),
		EventID:        eventID,
		Name:           name,
		Price:          price,
		TotalSeats:     total,
		AvailableSeats: avail,
		LayoutVersion:  1,
	}
}

// ============================================================
// Repository helpers: ValidateStatus / ValidateSortBy
// ============================================================

func TestValidateStatus_AllowsEmptyAndKnownValues(t *testing.T) {
	assert.True(t, ValidateStatus(""))
	for _, s := range []string{"DRAFT", "PUBLISHED", "ON_SALE", "SOLD_OUT", "COMPLETED", "CANCELLED"} {
		assert.True(t, ValidateStatus(s), "status %q must be allowed", s)
	}
}

func TestValidateStatus_RejectsUnknownValues(t *testing.T) {
	for _, s := range []string{"onsale", "ON SALE", "HIDDEN", "DRAFTED", "on_sale"} {
		assert.False(t, ValidateStatus(s), "status %q must be rejected", s)
	}
}

func TestValidateSortBy_AllowsKnownValues(t *testing.T) {
	for _, s := range []string{"", "event_date_asc", "event_date_desc", "popularity"} {
		assert.True(t, ValidateSortBy(s), "sort_by %q must be allowed", s)
	}
}

func TestValidateSortBy_RejectsUnknownValues(t *testing.T) {
	for _, s := range []string{"event_date", "price", "created_at", "EVENT_DATE_ASC"} {
		assert.False(t, ValidateSortBy(s), "sort_by %q must be rejected", s)
	}
}

// ============================================================
// Repository helpers: buildListWhere / buildOrderBy
// ============================================================

func TestBuildListWhere_NoFiltersIsAlwaysTrue(t *testing.T) {
	where, args := buildListWhere(ListFilter{})

	assert.Equal(t, "TRUE", where)
	assert.Empty(t, args)
}

func TestBuildListWhere_StatusOnly(t *testing.T) {
	where, args := buildListWhere(ListFilter{Status: "ON_SALE"})

	assert.Equal(t, "status::text = $1", where)
	require.Len(t, args, 1)
	assert.Equal(t, "ON_SALE", args[0])
}

func TestBuildListWhere_SearchOnly(t *testing.T) {
	where, args := buildListWhere(ListFilter{Search: "jazz"})

	assert.Equal(t, "name ILIKE $1", where)
	require.Len(t, args, 1)
	assert.Equal(t, "%jazz%", args[0])
}

func TestBuildListWhere_StatusAndSearchAreAnded(t *testing.T) {
	where, args := buildListWhere(ListFilter{Status: "ON_SALE", Search: "jazz"})

	assert.Equal(t, "status::text = $1 AND name ILIKE $2", where)
	require.Len(t, args, 2)
	assert.Equal(t, "ON_SALE", args[0])
	assert.Equal(t, "%jazz%", args[1])
}

func TestBuildOrderBy_Variants(t *testing.T) {
	assert.Equal(t, "event_date ASC, event_start_time ASC", buildOrderBy(""))
	assert.Equal(t, "event_date ASC, event_start_time ASC", buildOrderBy("event_date_asc"))
	assert.Equal(t, "event_date DESC, event_start_time DESC", buildOrderBy("event_date_desc"))
	assert.Equal(t, "created_at DESC", buildOrderBy("popularity"))
}

// ============================================================
// Service helpers: formatDecimal / formatHHMM
// ============================================================

func TestFormatDecimal_NoDotIsUnchanged(t *testing.T) {
	assert.Equal(t, "2000000", formatDecimal("2000000"))
}

func TestFormatDecimal_StripsTrailingZeros(t *testing.T) {
	assert.Equal(t, "2000000", formatDecimal("2000000.00"))
	assert.Equal(t, "75000", formatDecimal("75000.00"))
	assert.Equal(t, "0", formatDecimal("0.00"))
	// NOTE: TrimRight removes every trailing "0", so a price ending in a
	// non-zero fraction keeps one decimal: "1500.50" -> "1500.5".
	assert.Equal(t, "1500.5", formatDecimal("1500.50"))
}

func TestFormatDecimal_KeepsSignificantDecimals(t *testing.T) {
	assert.Equal(t, "12.34", formatDecimal("12.34"))
	assert.Equal(t, "0.5", formatDecimal("0.50"))
}

func TestFormatDecimal_EmptyString(t *testing.T) {
	assert.Equal(t, "", formatDecimal(""))
}

func TestFormatHHMM_EmptyStaysEmpty(t *testing.T) {
	assert.Equal(t, "", formatHHMM(""))
}

func TestFormatHHMM_ShortensToFiveChars(t *testing.T) {
	assert.Equal(t, "19:00", formatHHMM("19:00:00"))
	assert.Equal(t, "19:00", formatHHMM("19:00"))
}

func TestFormatHHMM_ShortValueUnchanged(t *testing.T) {
	assert.Equal(t, "19", formatHHMM("19"))
	assert.Equal(t, "9", formatHHMM("9"))
}

// ============================================================
// Service: buildEventSummary
// ============================================================

func TestBuildEventSummary_ComputesMinMaxAndTotals(t *testing.T) {
	eid := uuid.New()
	ev := testEvent(eid)
	sections := []Section{
		testSection(eid, "CAT1", "100000.00", 50, 30),
		testSection(eid, "VIP", "500000.00", 20, 5),
		testSection(eid, "FEST", "250000.50", 30, 10),
	}

	sum := buildEventSummary(ev, sections)

	assert.Equal(t, eid.String(), sum.EventID)
	assert.Equal(t, "Konser Jazz", sum.Name)
	assert.Equal(t, "19:00", sum.EventStartTime)
	assert.Equal(t, "ON_SALE", sum.Status)
	assert.Equal(t, "100000", sum.MinPrice)
	assert.Equal(t, "500000", sum.MaxPrice)
	assert.Equal(t, 100, sum.TotalTickets)
	assert.Equal(t, 45, sum.TicketsAvailable)
	require.Len(t, sum.Sections, 3)
	assert.Equal(t, "CAT1", sum.Sections[0].Name)
	assert.Equal(t, "100000", sum.Sections[0].Price)
}

func TestBuildEventSummary_NoSections(t *testing.T) {
	ev := testEvent(uuid.New())

	sum := buildEventSummary(ev, nil)

	assert.Empty(t, sum.Sections)
	assert.Equal(t, 0, sum.TotalTickets)
	assert.Equal(t, 0, sum.TicketsAvailable)
	assert.Equal(t, "", sum.MinPrice)
	assert.Equal(t, "", sum.MaxPrice)
}

func TestBuildEventSummary_UnparsablePriceIsSkipped(t *testing.T) {
	// CHARACTERISATION of the defensive path: buildEventSummary seeds
	// min/max from index 0 (i == 0), so a first section whose price does not
	// parse leaves MinPrice empty while MaxPrice still resolves.
	//
	// FINDING (P9a-5, reported not fixed): the min/max seeding uses
	// "i == 0" instead of a "have-value" flag, so an unparsable first
	// price zeroes out MinPrice. NOT reachable in production — the schema
	// declares sections.price NUMERIC(15,2) NOT NULL CHECK (price > 0), so
	// every stored price parses. No production change warranted; flagged
	// for the record only.
	eid := uuid.New()
	ev := testEvent(eid)
	sections := []Section{
		testSection(eid, "BAD", "not-a-price", 10, 5),
		testSection(eid, "GOOD", "75000.00", 10, 5),
	}

	sum := buildEventSummary(ev, sections)

	assert.Equal(t, "", sum.MinPrice, "current behaviour: unparsable first price leaves min empty")
	assert.Equal(t, "75000", sum.MaxPrice)
	assert.Len(t, sum.Sections, 2, "unparsable prices still appear as section briefs")
	assert.Equal(t, "not-a-price", sum.Sections[0].Price)
}

func TestBuildEventSummary_MinIsTheSmallestPrice(t *testing.T) {
	eid := uuid.New()
	ev := testEvent(eid)
	sections := []Section{
		testSection(eid, "VIP", "900000.00", 10, 5),
		testSection(eid, "CHEAP", "25000.00", 10, 5),
		testSection(eid, "MID", "500000.00", 10, 5),
	}

	sum := buildEventSummary(ev, sections)

	assert.Equal(t, "25000", sum.MinPrice)
	assert.Equal(t, "900000", sum.MaxPrice)
}

// ============================================================
// Service: List
// ============================================================

func TestList_Success(t *testing.T) {
	eid := uuid.New()
	repo := &fakeEventRepo{
		events: []Event{testEvent(eid)},
		total:  1,
		byIDs: map[uuid.UUID][]Section{
			eid: {testSection(eid, "VIP", "300000.00", 40, 12)},
		},
	}
	svc := NewEventService(repo)

	resp, err := svc.List(context.Background(), ListFilter{Page: 1, Limit: 20})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, 1, resp.CurrentPage)
	assert.Equal(t, 20, resp.Limit)
	assert.Equal(t, 1, resp.TotalPages)
	require.Len(t, resp.Events, 1)
	assert.Equal(t, eid.String(), resp.Events[0].EventID)
	assert.Equal(t, "300000", resp.Events[0].MinPrice)
	assert.Equal(t, 40, resp.Events[0].TotalTickets)
}

func TestList_EmptyResultSkipsSectionQuery(t *testing.T) {
	repo := &fakeEventRepo{events: nil, total: 0}
	svc := NewEventService(repo)

	resp, err := svc.List(context.Background(), ListFilter{Page: 1, Limit: 20})

	require.NoError(t, err)
	assert.Empty(t, resp.Events)
	assert.Equal(t, 0, resp.Total)
	assert.Equal(t, 0, resp.TotalPages, "0 items over 20 per page is 0 pages")
}

func TestList_TotalPagesRoundsUp(t *testing.T) {
	repo := &fakeEventRepo{events: nil, total: 41}
	svc := NewEventService(repo)

	resp, err := svc.List(context.Background(), ListFilter{Page: 1, Limit: 20})

	require.NoError(t, err)
	assert.Equal(t, 3, resp.TotalPages, "41 items over 20 per page is 3 pages")
}

func TestList_ZeroLimitYieldsZeroTotalPages(t *testing.T) {
	repo := &fakeEventRepo{events: nil, total: 10}
	svc := NewEventService(repo)

	resp, err := svc.List(context.Background(), ListFilter{Page: 1, Limit: 0})

	require.NoError(t, err)
	assert.Equal(t, 0, resp.TotalPages)
}

func TestList_RepositoryError(t *testing.T) {
	repo := &fakeEventRepo{listErr: errors.New("db down")}
	svc := NewEventService(repo)

	_, err := svc.List(context.Background(), ListFilter{Page: 1, Limit: 20})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "list events")
}

func TestList_SectionBatchError(t *testing.T) {
	repo := &fakeEventRepo{
		events:   []Event{testEvent(uuid.New())},
		byIDsErr: errors.New("section query failed"),
	}
	svc := NewEventService(repo)

	_, err := svc.List(context.Background(), ListFilter{Page: 1, Limit: 20})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get sections batch")
}

// ============================================================
// Service: GetByID
// ============================================================

func TestGetByID_Success(t *testing.T) {
	eid := uuid.New()
	ev := testEvent(eid)
	repo := &fakeEventRepo{
		byID:     &ev,
		sections: []Section{testSection(eid, "VIP", "1500000.00", 100, 40)},
	}
	svc := NewEventService(repo)

	resp, err := svc.GetByID(context.Background(), eid)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, eid.String(), resp.EventID)
	assert.Equal(t, "Konser Jazz", resp.Name)
	assert.Equal(t, "19:00", resp.EventStartTime)
	assert.Equal(t, "23:30", resp.EventEndTime)
	assert.Equal(t, "ON_SALE", resp.Status)
	assert.Equal(t, 2, resp.SeatLayoutVersion)
	assert.Equal(t, ev.OrganizerID.String(), resp.Organizer.OrganizerID)
	assert.Equal(t, "Organizer Satu", resp.Organizer.Name)
	assert.Equal(t, "2026-01-02T03:04:05Z", resp.CreatedAt)
	require.Len(t, resp.Sections, 1)
	assert.Equal(t, "1500000", resp.Sections[0].Price)
	assert.Equal(t, 100, resp.Sections[0].TotalSeats)
	assert.Equal(t, 40, resp.Sections[0].AvailableSeats)
}

func TestGetByID_NotFound(t *testing.T) {
	repo := &fakeEventRepo{}
	svc := NewEventService(repo)

	_, err := svc.GetByID(context.Background(), uuid.New())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEventNotFound)
}

func TestGetByID_RepositoryError(t *testing.T) {
	repo := &fakeEventRepo{byIDErr: errors.New("query failed")}
	svc := NewEventService(repo)

	_, err := svc.GetByID(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get event")
}

func TestGetByID_SectionsError(t *testing.T) {
	ev := testEvent(uuid.New())
	repo := &fakeEventRepo{byID: &ev, sectErr: errors.New("section boom")}
	svc := NewEventService(repo)

	_, err := svc.GetByID(context.Background(), ev.ID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get sections")
}

// ============================================================
// Handler: List
// ============================================================

// newEventRouter wires the catalog handler onto a gin router.
func newEventRouter(svc *EventService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewEventHandler(svc)
	r := gin.New()
	r.GET("/events", h.List)
	r.GET("/events/:event_id", h.GetByID)
	return r
}

func eventGet(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestHandlerList_Success(t *testing.T) {
	eid := uuid.New()
	repo := &fakeEventRepo{
		events: []Event{testEvent(eid)},
		total:  1,
		byIDs:  map[uuid.UUID][]Section{eid: {testSection(eid, "VIP", "300000.00", 40, 12)}},
	}
	r := newEventRouter(NewEventService(repo))

	w := eventGet(t, r, "/events")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"total":1`)
	assert.Contains(t, w.Body.String(), `"current_page":1`)
	assert.Contains(t, w.Body.String(), `"limit":20`)
}

func TestHandlerList_AppliesFilters(t *testing.T) {
	repo := &fakeEventRepo{events: nil, total: 0}
	r := newEventRouter(NewEventService(repo))

	w := eventGet(t, r, "/events?status=ON_SALE&search=jazz&sort_by=event_date_desc&page=3&limit=5")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"current_page":3`)
	assert.Contains(t, w.Body.String(), `"limit":5`)
}

func TestHandlerList_InvalidStatusReturns400(t *testing.T) {
	r := newEventRouter(NewEventService(&fakeEventRepo{}))

	w := eventGet(t, r, "/events?status=BOGUS")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandlerList_InvalidSortByReturns400(t *testing.T) {
	r := newEventRouter(NewEventService(&fakeEventRepo{}))

	w := eventGet(t, r, "/events?sort_by=price")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandlerList_InvalidPageReturns400(t *testing.T) {
	r := newEventRouter(NewEventService(&fakeEventRepo{}))

	for _, q := range []string{"?page=0", "?page=-1", "?page=abc"} {
		w := eventGet(t, r, "/events"+q)
		assert.Equal(t, http.StatusBadRequest, w.Code, "query %q must be rejected", q)
	}
}

func TestHandlerList_InvalidLimitReturns400(t *testing.T) {
	r := newEventRouter(NewEventService(&fakeEventRepo{}))

	for _, q := range []string{"?limit=0", "?limit=51", "?limit=-5", "?limit=xyz"} {
		w := eventGet(t, r, "/events"+q)
		assert.Equal(t, http.StatusBadRequest, w.Code, "query %q must be rejected", q)
	}
}

func TestHandlerList_Limit50IsAccepted(t *testing.T) {
	r := newEventRouter(NewEventService(&fakeEventRepo{}))

	w := eventGet(t, r, "/events?limit=50")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"limit":50`)
}

func TestHandlerList_ServiceErrorReturns500(t *testing.T) {
	repo := &fakeEventRepo{listErr: errors.New("db down")}
	r := newEventRouter(NewEventService(repo))

	w := eventGet(t, r, "/events")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
}

// ============================================================
// Handler: GetByID
// ============================================================

func TestHandlerGetByID_Success(t *testing.T) {
	eid := uuid.New()
	ev := testEvent(eid)
	repo := &fakeEventRepo{
		byID:     &ev,
		sections: []Section{testSection(eid, "CAT1", "100000.00", 50, 20)},
	}
	r := newEventRouter(NewEventService(repo))

	w := eventGet(t, r, "/events/"+eid.String())

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), eid.String())
	assert.Contains(t, w.Body.String(), `"event_start_time":"19:00"`)
}

func TestHandlerGetByID_InvalidUUIDReturns400(t *testing.T) {
	r := newEventRouter(NewEventService(&fakeEventRepo{}))

	w := eventGet(t, r, "/events/not-a-uuid")

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandlerGetByID_NotFoundReturns404(t *testing.T) {
	r := newEventRouter(NewEventService(&fakeEventRepo{}))

	w := eventGet(t, r, "/events/"+uuid.NewString())

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
}

func TestHandlerGetByID_InternalErrorReturns500(t *testing.T) {
	repo := &fakeEventRepo{byIDErr: errors.New("db down")}
	r := newEventRouter(NewEventService(repo))

	w := eventGet(t, r, "/events/"+uuid.NewString())

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
}
