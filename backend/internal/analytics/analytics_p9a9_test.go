package analytics

// FIX P9a-9b TD-F6-09: analytics service/handler/error coverage boost.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type p9a9AnalyticsRepo struct {
	sales     *SalesBlock
	gatesMap  map[string]string
	activeCnt int
	totalScan int
	antiBot   *AntiBotBlock
	activity  []ActivityItem
	att       *ReportAttendance
	gateUtil  map[string]GateUtilization
	timeline  []ScanTimelineBucket
	loyal     int
	newC      int
	reportGP  map[string]ReportGatePerformance
	errs      map[string]error
}

func (p *p9a9AnalyticsRepo) fail(k string) error {
	if p.errs == nil {
		return nil
	}
	return p.errs[k]
}
func (p *p9a9AnalyticsRepo) SalesAndGateSummary(context.Context, uuid.UUID) (*SalesBlock, error) {
	if e := p.fail("sales"); e != nil {
		return nil, e
	}
	if p.sales == nil {
		return &SalesBlock{TicketsSold: 1, TotalTickets: 10, SalesPercentage: 10}, nil
	}
	return p.sales, nil
}
func (p *p9a9AnalyticsRepo) GateStatusToday(context.Context, uuid.UUID) (map[string]string, int, error) {
	if e := p.fail("gates"); e != nil {
		return nil, 0, e
	}
	if p.gatesMap == nil {
		return map[string]string{"G1": "ACTIVE"}, 1, nil
	}
	return p.gatesMap, p.activeCnt, nil
}
func (p *p9a9AnalyticsRepo) TotalScansToday(context.Context, uuid.UUID) (int, error) {
	if e := p.fail("scans"); e != nil {
		return 0, e
	}
	return p.totalScan, nil
}
func (p *p9a9AnalyticsRepo) AntiBotStats(context.Context, uuid.UUID) (*AntiBotBlock, error) {
	if e := p.fail("antibot"); e != nil {
		return nil, e
	}
	if p.antiBot == nil {
		return &AntiBotBlock{RequestsBlockedToday: 2}, nil
	}
	return p.antiBot, nil
}
func (p *p9a9AnalyticsRepo) RecentActivity(context.Context, uuid.UUID, int) ([]ActivityItem, error) {
	if e := p.fail("activity"); e != nil {
		return nil, e
	}
	if p.activity == nil {
		return []ActivityItem{{Type: "SCAN", Description: "x"}}, nil
	}
	return p.activity, nil
}
func (p *p9a9AnalyticsRepo) AttendanceMetrics(context.Context, uuid.UUID) (*ReportAttendance, error) {
	if e := p.fail("att"); e != nil {
		return nil, e
	}
	if p.att == nil {
		return &ReportAttendance{TotalTickets: 10, UsedTickets: 5, UnusedTickets: 5, NoShowRate: 50}, nil
	}
	return p.att, nil
}
func (p *p9a9AnalyticsRepo) GateUtilization(context.Context, uuid.UUID) (map[string]GateUtilization, error) {
	if e := p.fail("gu"); e != nil {
		return nil, e
	}
	if p.gateUtil == nil {
		return map[string]GateUtilization{"G1": {TotalScans: 10, ValidScans: 9, Utilization: 90}}, nil
	}
	return p.gateUtil, nil
}
func (p *p9a9AnalyticsRepo) ScanTimeline(context.Context, uuid.UUID) ([]ScanTimelineBucket, error) {
	if e := p.fail("tl"); e != nil {
		return nil, e
	}
	if p.timeline == nil {
		return []ScanTimelineBucket{{Hour: "19:00", Scans: 5}}, nil
	}
	return p.timeline, nil
}
func (p *p9a9AnalyticsRepo) LoyaltySplit(context.Context, uuid.UUID) (int, int, error) {
	if e := p.fail("loyal"); e != nil {
		return 0, 0, e
	}
	return p.loyal, p.newC, nil
}
func (p *p9a9AnalyticsRepo) ReportGatePerformance(context.Context, uuid.UUID) (map[string]ReportGatePerformance, error) {
	if e := p.fail("rgp"); e != nil {
		return nil, e
	}
	if p.reportGP == nil {
		return map[string]ReportGatePerformance{"G1": {TotalScans: 10, ValidScans: 9, InvalidScans: 1}}, nil
	}
	return p.reportGP, nil
}
func (p *p9a9AnalyticsRepo) InsertInsight(context.Context, *InsightRecord) error { return nil }
func (p *p9a9AnalyticsRepo) ExistsByEventAndType(context.Context, uuid.UUID, string) (bool, error) {
	return false, nil
}
func (p *p9a9AnalyticsRepo) ListByEvent(context.Context, uuid.UUID) ([]InsightRecord, error) {
	return nil, nil
}

type p9a9AnalOwners struct {
	org uuid.UUID
	err error
}

func (o p9a9AnalOwners) GetOrganizerID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return o.org, o.err
}

func TestP9a9GetDashboard_TotalScansError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"scans": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetDashboard(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetDashboard_AntiBotError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"antibot": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetDashboard(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetDashboard_ActivityError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"activity": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetDashboard(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetDashboard_SalesError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"sales": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetDashboard(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetDashboard_GatesError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"gates": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetDashboard(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetAnalytics_AttendanceError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"att": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetAnalytics(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetAnalytics_GateUtilError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"gu": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetAnalytics(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetAnalytics_TimelineError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"tl": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetAnalytics(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetAnalytics_LoyaltyError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"loyal": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetAnalytics(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetAnalytics_SalesError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"sales": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetAnalytics(context.Background(), caller, ev, "organizer")
	require.Error(t, err)
}

func TestP9a9GetPostEventReport_BuildJSONAttError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"att": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetPostEventReport(context.Background(), caller, ev, "organizer", "json")
	require.Error(t, err)
}

func TestP9a9GetPostEventReport_BuildJSONGPError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"rgp": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetPostEventReport(context.Background(), caller, ev, "organizer", "json")
	require.Error(t, err)
}

func TestP9a9GetPostEventReport_BuildJSONSalesError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"sales": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetPostEventReport(context.Background(), caller, ev, "organizer", "json")
	require.Error(t, err)
}

func TestP9a9GetPostEventReport_CSVBuildError(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{errs: map[string]error{"att": errors.New("db err")}}, p9a9AnalOwners{org: caller})
	_, err := svc.GetPostEventReport(context.Background(), caller, ev, "organizer", "csv")
	require.Error(t, err)
}

func TestP9a9GetPostEventReport_DefaultFormatJSON(t *testing.T) {
	caller := uuid.New()
	ev := uuid.New()
	svc := NewAnalyticsService(&p9a9AnalyticsRepo{}, p9a9AnalOwners{org: caller})
	res, err := svc.GetPostEventReport(context.Background(), caller, ev, "organizer", "")
	require.NoError(t, err)
	assert.NotNil(t, res)
}

func TestP9a9EscapeCSV_FormulaChars(t *testing.T) {
	for _, v := range []string{"=CMD()", "+123", "-123", "@user"} {
		csv := escapeCSV(v)
		if v != "" && len(v) > 0 && strings.ContainsAny(v[:1], "=+-@") {
			assert.True(t, strings.HasPrefix(csv, "'"), "v=%s csv=%s", v, csv)
		}
	}
}

func TestP9a9EscapeCSV_Normal(t *testing.T) {
	assert.Equal(t, "hello", escapeCSV("hello"))
}

func TestP9a9NewAnalyticsService_NowFn(t *testing.T) {
	repo := &p9a9AnalyticsRepo{}
	owners := p9a9AnalOwners{org: uuid.New()}
	svc := NewAnalyticsService(repo, owners)
	caller := owners.org
	ev := uuid.New()
	res, err := svc.GetDashboard(context.Background(), caller, ev, "organizer")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.NotZero(t, res.UpdatedAt)
	_ = time.Now()
}
