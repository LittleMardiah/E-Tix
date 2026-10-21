package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

// STP traceability: analytics dashboard / analytics / post-event-report —
// no UT-* ID exists in STP v1.1 §3.2 for this module (module gap, see
// TD-F5-09). Tests below cover the service + HTTP handler layer.

// p9a7Repo is a programmable AnalyticsRepository stub: every method can be
// made to fail on demand so each error branch of the service is exercised.
type p9a7Repo struct {
	failOn string
	err    error

	sales    *SalesBlock
	gates    map[string]string
	active   int
	scans    int
	anti     *AntiBotBlock
	activity []ActivityItem
	att      *ReportAttendance
	gu       map[string]GateUtilization
	tl       []ScanTimelineBucket
	loyal    int
	newCust  int
	gp       map[string]ReportGatePerformance
}

func (p *p9a7Repo) fail(name string) error {
	if p.failOn == name {
		return p.err
	}
	return nil
}

func (p *p9a7Repo) SalesAndGateSummary(context.Context, uuid.UUID) (*SalesBlock, error) {
	if err := p.fail("sales"); err != nil {
		return nil, err
	}
	return p.sales, nil
}

func (p *p9a7Repo) GateStatusToday(context.Context, uuid.UUID) (map[string]string, int, error) {
	if err := p.fail("gates"); err != nil {
		return nil, 0, err
	}
	return p.gates, p.active, nil
}

func (p *p9a7Repo) TotalScansToday(context.Context, uuid.UUID) (int, error) {
	if err := p.fail("scans"); err != nil {
		return 0, err
	}
	return p.scans, nil
}

func (p *p9a7Repo) AntiBotStats(context.Context, uuid.UUID) (*AntiBotBlock, error) {
	if err := p.fail("antibot"); err != nil {
		return nil, err
	}
	return p.anti, nil
}

func (p *p9a7Repo) RecentActivity(context.Context, uuid.UUID, int) ([]ActivityItem, error) {
	if err := p.fail("activity"); err != nil {
		return nil, err
	}
	return p.activity, nil
}

func (p *p9a7Repo) AttendanceMetrics(context.Context, uuid.UUID) (*ReportAttendance, error) {
	if err := p.fail("attendance"); err != nil {
		return nil, err
	}
	return p.att, nil
}

func (p *p9a7Repo) GateUtilization(context.Context, uuid.UUID) (map[string]GateUtilization, error) {
	if err := p.fail("utilization"); err != nil {
		return nil, err
	}
	return p.gu, nil
}

func (p *p9a7Repo) ScanTimeline(context.Context, uuid.UUID) ([]ScanTimelineBucket, error) {
	if err := p.fail("timeline"); err != nil {
		return nil, err
	}
	return p.tl, nil
}

func (p *p9a7Repo) LoyaltySplit(context.Context, uuid.UUID) (int, int, error) {
	if err := p.fail("loyalty"); err != nil {
		return 0, 0, err
	}
	return p.loyal, p.newCust, nil
}

func (p *p9a7Repo) ReportGatePerformance(context.Context, uuid.UUID) (map[string]ReportGatePerformance, error) {
	if err := p.fail("gateperf"); err != nil {
		return nil, err
	}
	return p.gp, nil
}

func (p *p9a7Repo) InsertInsight(context.Context, *InsightRecord) error { return nil }

func (p *p9a7Repo) ExistsByEventAndType(context.Context, uuid.UUID, string) (bool, error) {
	return false, nil
}

func (p *p9a7Repo) ListByEvent(context.Context, uuid.UUID) ([]InsightRecord, error) {
	return nil, nil
}

// p9a7Owners is an EventOwnerReader stub with a programmable error.
type p9a7Owners struct {
	orgID uuid.UUID
	err   error
}

func (o p9a7Owners) GetOrganizerID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return o.orgID, o.err
}

func p9a7RepoFull() *p9a7Repo {
	return &p9a7Repo{
		sales: &SalesBlock{
			TicketsSold: 120, TotalTickets: 200, SalesPercentage: 60.0,
			RevenueToday: "1500000.00", TotalRevenue: "5000000.00",
		},
		gates:    map[string]string{"gate-1": "ACTIVE", "gate-2": "IDLE"},
		active:   2,
		scans:    87,
		anti:     &AntiBotBlock{RequestsBlockedToday: 4, SuspiciousIPsFlagged: 2, CaptchaTriggersToday: 9},
		activity: []ActivityItem{{Type: "SCAN", Description: "gate-1 scanned"}},
		att: &ReportAttendance{
			TotalTickets: 200, UsedTickets: 150, UnusedTickets: 50,
			NoShowRate: 25.0, ResaleCount: 5, ResaleRevenue: "250000.00",
		},
		gu:      map[string]GateUtilization{"gate-1": {TotalScans: 87, ValidScans: 80, Utilization: 91}},
		tl:      []ScanTimelineBucket{{Hour: "10:00", Scans: 40}},
		loyal:   33,
		newCust: 67,
		gp:      map[string]ReportGatePerformance{"gate-1": {TotalScans: 87, ValidScans: 80, InvalidScans: 7}},
	}
}

func TestP9a7_GetDashboard_Success(t *testing.T) {
	eventID := uuid.New()
	caller := uuid.New()
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{orgID: caller})

	resp, err := svc.GetDashboard(context.Background(), caller, eventID, "organizer")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, eventID, resp.EventID)
	assert.Equal(t, 120, resp.Sales.TicketsSold)
	assert.Equal(t, 2, resp.GateStatus.TotalGatesActive)
	assert.Equal(t, 87, resp.GateStatus.TotalScansToday)
	assert.Equal(t, "ACTIVE", resp.GateStatus.Gates["gate-1"])
	assert.Equal(t, 4, resp.AntiBot.RequestsBlockedToday)
	assert.Len(t, resp.RecentActivity, 1)
	assert.NotNil(t, resp.Alerts)
	assert.False(t, resp.UpdatedAt.IsZero())
}

func TestP9a7_GetDashboard_RepoErrors(t *testing.T) {
	caller := uuid.New()
	for _, tc := range []string{"sales", "gates", "scans", "antibot", "activity"} {
		t.Run(tc, func(t *testing.T) {
			repo := p9a7RepoFull()
			repo.failOn = tc
			repo.err = errors.New("boom " + tc)
			svc := NewAnalyticsService(repo, p9a7Owners{orgID: caller})
			_, err := svc.GetDashboard(context.Background(), caller, uuid.New(), "organizer")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc)
		})
	}
}

func TestP9a7_GetDashboard_OwnerLookupError(t *testing.T) {
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{err: errors.New("owner down")})
	_, err := svc.GetDashboard(context.Background(), uuid.New(), uuid.New(), "organizer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner down")
}

func TestP9a7_GetDashboard_Forbidden(t *testing.T) {
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{orgID: uuid.New()})
	_, err := svc.GetDashboard(context.Background(), uuid.New(), uuid.New(), "organizer")
	require.ErrorIs(t, err, ErrEventForbidden)
}

func TestP9a7_GetAnalytics_Success(t *testing.T) {
	caller := uuid.New()
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{orgID: caller})

	resp, err := svc.GetAnalytics(context.Background(), caller, uuid.New(), "organizer")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, 200, resp.TotalTickets)
	assert.Equal(t, 150, resp.AttendanceCount)
	assert.InDelta(t, 75.0, resp.AttendanceRate, 0.001)
	assert.InDelta(t, 25.0, resp.NoShowRate, 0.001)
	assert.Equal(t, 120, resp.TicketsSold)
	assert.Equal(t, 33, resp.LoyalCustomers)
	assert.Equal(t, 67, resp.NewCustomers)
	assert.Len(t, resp.ScanTimeline, 1)
	assert.Equal(t, 91, resp.GateUtilization["gate-1"].Utilization)
}

func TestP9a7_GetAnalytics_ZeroTickets_NoDivByZero(t *testing.T) {
	caller := uuid.New()
	repo := p9a7RepoFull()
	repo.att = &ReportAttendance{TotalTickets: 0}
	svc := NewAnalyticsService(repo, p9a7Owners{orgID: caller})

	resp, err := svc.GetAnalytics(context.Background(), caller, uuid.New(), "organizer")
	require.NoError(t, err)
	assert.InDelta(t, 0.0, resp.AttendanceRate, 0.001)
}

func TestP9a7_GetAnalytics_RepoErrors(t *testing.T) {
	caller := uuid.New()
	for _, tc := range []string{"attendance", "utilization", "timeline", "loyalty", "sales"} {
		t.Run(tc, func(t *testing.T) {
			repo := p9a7RepoFull()
			repo.failOn = tc
			repo.err = errors.New("boom " + tc)
			svc := NewAnalyticsService(repo, p9a7Owners{orgID: caller})
			_, err := svc.GetAnalytics(context.Background(), caller, uuid.New(), "organizer")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc)
		})
	}
}

func TestP9a7_GetAnalytics_Forbidden(t *testing.T) {
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{orgID: uuid.New()})
	_, err := svc.GetAnalytics(context.Background(), uuid.New(), uuid.New(), "organizer")
	require.ErrorIs(t, err, ErrEventForbidden)
}

func TestP9a7_GetPostEventReport_JSON(t *testing.T) {
	caller := uuid.New()
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{orgID: caller})

	for _, format := range []string{"", "json"} {
		out, err := svc.GetPostEventReport(context.Background(), caller, uuid.New(), "organizer", format)
		require.NoError(t, err)
		rep, ok := out.(*PostEventReportResponse)
		require.True(t, ok, "expected *PostEventReportResponse, got %T", out)
		assert.Equal(t, 200, rep.Attendance.TotalTickets)
		assert.Equal(t, "5000000.00", rep.Summary.TotalRevenue)
		assert.Equal(t, 7, rep.GatePerformance["gate-1"].InvalidScans)
	}
}

func TestP9a7_GetPostEventReport_CSV(t *testing.T) {
	caller := uuid.New()
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{orgID: caller})

	out, err := svc.GetPostEventReport(context.Background(), caller, uuid.New(), "organizer", "csv")
	require.NoError(t, err)
	b, ok := out.([]byte)
	require.True(t, ok, "expected []byte, got %T", out)
	csvOut := string(b)
	assert.Contains(t, csvOut, "event_id,total_tickets,used_tickets")
	assert.Contains(t, csvOut, "200,150,50")
	assert.Contains(t, csvOut, "25.00")
	assert.Contains(t, csvOut, "5000000.00")
	assert.True(t, len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF, "UTF-8 BOM expected")
}

func TestP9a7_GetPostEventReport_UnknownFormat(t *testing.T) {
	caller := uuid.New()
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{orgID: caller})
	_, err := svc.GetPostEventReport(context.Background(), caller, uuid.New(), "organizer", "xml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown format")
}

func TestP9a7_GetPostEventReport_RepoError(t *testing.T) {
	caller := uuid.New()
	repo := p9a7RepoFull()
	repo.failOn = "attendance"
	repo.err = errors.New("db down")
	svc := NewAnalyticsService(repo, p9a7Owners{orgID: caller})
	_, err := svc.GetPostEventReport(context.Background(), caller, uuid.New(), "organizer", "json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestP9a7_GetPostEventReport_Forbidden(t *testing.T) {
	svc := NewAnalyticsService(p9a7RepoFull(), p9a7Owners{orgID: uuid.New()})
	_, err := svc.GetPostEventReport(context.Background(), uuid.New(), uuid.New(), "organizer", "json")
	require.ErrorIs(t, err, ErrEventForbidden)
}

// --- HTTP handler layer ---

func p9a7Do(t *testing.T, h *AnalyticsHandler, method, kind, eventID, userID, role, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.GET("/events/:event_id/"+kind, func(c *gin.Context) {
		if userID != "" {
			c.Set("user_id", userID)
		}
		if role != "" {
			c.Set("role", role)
		}
		switch kind {
		case "dashboard":
			h.GetDashboard(c)
		case "analytics":
			h.GetAnalytics(c)
		default:
			h.GetPostEventReport(c)
		}
	})
	req := httptest.NewRequest(method, "/events/"+eventID+"/"+kind+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func p9a7Handler(t *testing.T, repo *p9a7Repo, orgID uuid.UUID) *AnalyticsHandler {
	t.Helper()
	return NewAnalyticsHandler(NewAnalyticsService(repo, p9a7Owners{orgID: orgID}))
}

func TestP9a7_Handler_GetDashboard_200(t *testing.T) {
	caller := uuid.New()
	eventID := uuid.New()
	h := p9a7Handler(t, p9a7RepoFull(), caller)
	w := p9a7Do(t, h, http.MethodGet, "dashboard", eventID.String(), caller.String(), "organizer", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), eventID.String())
}

func TestP9a7_Handler_GetDashboard_401InvalidUser(t *testing.T) {
	h := p9a7Handler(t, p9a7RepoFull(), uuid.New())
	w := p9a7Do(t, h, http.MethodGet, "dashboard", uuid.New().String(), "not-a-uuid", "organizer", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
}

func TestP9a7_Handler_GetDashboard_400InvalidEvent(t *testing.T) {
	caller := uuid.New()
	h := p9a7Handler(t, p9a7RepoFull(), caller)
	w := p9a7Do(t, h, http.MethodGet, "dashboard", "not-a-uuid", caller.String(), "organizer", "")
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_REQUEST")
}

func TestP9a7_Handler_GetDashboard_403Forbidden(t *testing.T) {
	h := p9a7Handler(t, p9a7RepoFull(), uuid.New())
	w := p9a7Do(t, h, http.MethodGet, "dashboard", uuid.New().String(), uuid.New().String(), "organizer", "")
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "FORBIDDEN")
}

func TestP9a7_Handler_GetDashboard_500Internal(t *testing.T) {
	caller := uuid.New()
	repo := p9a7RepoFull()
	repo.failOn = "sales"
	repo.err = errors.New("db down")
	h := p9a7Handler(t, repo, caller)
	w := p9a7Do(t, h, http.MethodGet, "dashboard", uuid.New().String(), caller.String(), "organizer", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
}

func TestP9a7_Handler_GetAnalytics_200(t *testing.T) {
	caller := uuid.New()
	h := p9a7Handler(t, p9a7RepoFull(), caller)
	w := p9a7Do(t, h, http.MethodGet, "analytics", uuid.New().String(), caller.String(), "organizer", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}

func TestP9a7_Handler_GetAnalytics_403Forbidden(t *testing.T) {
	h := p9a7Handler(t, p9a7RepoFull(), uuid.New())
	w := p9a7Do(t, h, http.MethodGet, "analytics", uuid.New().String(), uuid.New().String(), "organizer", "")
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestP9a7_Handler_GetPostEventReport_200JSON(t *testing.T) {
	caller := uuid.New()
	h := p9a7Handler(t, p9a7RepoFull(), caller)
	w := p9a7Do(t, h, http.MethodGet, "post-event-report", uuid.New().String(), caller.String(), "organizer", "")
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, true, body["success"])
}

func TestP9a7_Handler_GetPostEventReport_200CSV(t *testing.T) {
	caller := uuid.New()
	h := p9a7Handler(t, p9a7RepoFull(), caller)
	w := p9a7Do(t, h, http.MethodGet, "post-event-report", uuid.New().String(), caller.String(), "organizer", "?format=csv")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, w.Body.String(), "event_id,total_tickets")
}

func TestP9a7_Handler_GetPostEventReport_501PDF(t *testing.T) {
	caller := uuid.New()
	h := p9a7Handler(t, p9a7RepoFull(), caller)
	w := p9a7Do(t, h, http.MethodGet, "post-event-report", uuid.New().String(), caller.String(), "organizer", "?format=pdf")
	require.Equal(t, http.StatusNotImplemented, w.Code)
	assert.Contains(t, w.Body.String(), "NOT_IMPLEMENTED")
}

func TestP9a7_Handler_GetPostEventReport_500Internal(t *testing.T) {
	caller := uuid.New()
	repo := p9a7RepoFull()
	repo.failOn = "attendance"
	repo.err = errors.New("db down")
	h := p9a7Handler(t, repo, caller)
	w := p9a7Do(t, h, http.MethodGet, "post-event-report", uuid.New().String(), caller.String(), "organizer", "")
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestP9a7_Handler_GetPostEventReport_401InvalidUser(t *testing.T) {
	h := p9a7Handler(t, p9a7RepoFull(), uuid.New())
	w := p9a7Do(t, h, http.MethodGet, "post-event-report", uuid.New().String(), "bogus", "organizer", "")
	require.Equal(t, http.StatusUnauthorized, w.Code)
}
