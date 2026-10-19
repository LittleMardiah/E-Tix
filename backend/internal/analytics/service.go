package analytics

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrEventForbidden is returned when caller is not the event organizer.
var ErrEventForbidden = errors.New("analytics: forbidden")

// ErrPDFNotImplemented is returned for format=pdf (stub).
var ErrPDFNotImplemented = errors.New("analytics: pdf format not implemented")

// EventOwnerReader resolves event organizer for ownership check.
type EventOwnerReader interface {
	GetOrganizerID(ctx context.Context, eventID uuid.UUID) (uuid.UUID, error)
}

// AnalyticsService orchestrates dashboard/analytics/report.
type AnalyticsService struct {
	repo   AnalyticsRepository
	owners EventOwnerReader
	nowFn  func() time.Time
}

// NewAnalyticsService constructs an AnalyticsService.
func NewAnalyticsService(repo AnalyticsRepository, owners EventOwnerReader) *AnalyticsService {
	return &AnalyticsService{repo: repo, owners: owners, nowFn: time.Now}
}

func (s *AnalyticsService) checkOwner(ctx context.Context, callerID, eventID uuid.UUID, callerRole string) error {
	orgID, err := s.owners.GetOrganizerID(ctx, eventID)
	if err != nil {
		return err
	}
	if callerRole == "admin" {
		return nil
	}
	if orgID != callerID {
		return ErrEventForbidden
	}
	return nil
}

// GetDashboard builds the real-time dashboard.
func (s *AnalyticsService) GetDashboard(ctx context.Context, callerID, eventID uuid.UUID, callerRole string) (*DashboardResponse, error) {
	if err := s.checkOwner(ctx, callerID, eventID, callerRole); err != nil {
		return nil, err
	}
	sales, err := s.repo.SalesAndGateSummary(ctx, eventID)
	if err != nil {
		return nil, err
	}
	gates, activeCount, err := s.repo.GateStatusToday(ctx, eventID)
	if err != nil {
		return nil, err
	}
	totalScans, err := s.repo.TotalScansToday(ctx, eventID)
	if err != nil {
		return nil, err
	}
	aB, err := s.repo.AntiBotStats(ctx, eventID)
	if err != nil {
		return nil, err
	}
	activity, err := s.repo.RecentActivity(ctx, eventID, 10)
	if err != nil {
		return nil, err
	}
	return &DashboardResponse{
		EventID: eventID, Sales: *sales,
		GateStatus: GateStatusBlock{TotalGatesActive: activeCount, Gates: gates, TotalScansToday: totalScans},
		AntiBot:    *aB, RecentActivity: activity,
		Alerts: []Alert{}, UpdatedAt: s.nowFn(),
	}, nil
}

// GetAnalytics builds the analytics view.
func (s *AnalyticsService) GetAnalytics(ctx context.Context, callerID, eventID uuid.UUID, callerRole string) (*AnalyticsResponse, error) {
	if err := s.checkOwner(ctx, callerID, eventID, callerRole); err != nil {
		return nil, err
	}
	att, err := s.repo.AttendanceMetrics(ctx, eventID)
	if err != nil {
		return nil, err
	}
	gu, err := s.repo.GateUtilization(ctx, eventID)
	if err != nil {
		return nil, err
	}
	tl, err := s.repo.ScanTimeline(ctx, eventID)
	if err != nil {
		return nil, err
	}
	loyal, newC, err := s.repo.LoyaltySplit(ctx, eventID)
	if err != nil {
		return nil, err
	}
	sales, err := s.repo.SalesAndGateSummary(ctx, eventID)
	if err != nil {
		return nil, err
	}
	attRate := 0.0
	if att.TotalTickets > 0 {
		attRate = float64(att.UsedTickets) / float64(att.TotalTickets) * 100
	}
	return &AnalyticsResponse{
		EventID: eventID, TotalTickets: att.TotalTickets, TicketsSold: sales.TicketsSold,
		AttendanceCount: att.UsedTickets, AttendanceRate: attRate, NoShowRate: att.NoShowRate,
		GateUtilization: gu, ScanTimeline: tl, LoyalCustomers: loyal, NewCustomers: newC,
		GeneratedAt: s.nowFn(),
	}, nil
}

// GetPostEventReport returns JSON / CSV / 501 for pdf.
func (s *AnalyticsService) GetPostEventReport(ctx context.Context, callerID, eventID uuid.UUID, callerRole, format string) (interface{}, error) {
	if err := s.checkOwner(ctx, callerID, eventID, callerRole); err != nil {
		return nil, err
	}
	switch format {
	case "", "json":
		return s.buildReportJSON(ctx, eventID)
	case "csv":
		return s.buildReportCSV(ctx, eventID)
	case "pdf":
		return nil, ErrPDFNotImplemented
	default:
		return nil, fmt.Errorf("analytics: unknown format %q", format)
	}
}

func (s *AnalyticsService) buildReportJSON(ctx context.Context, eventID uuid.UUID) (*PostEventReportResponse, error) {
	att, err := s.repo.AttendanceMetrics(ctx, eventID)
	if err != nil {
		return nil, err
	}
	gp, err := s.repo.ReportGatePerformance(ctx, eventID)
	if err != nil {
		return nil, err
	}
	sales, err := s.repo.SalesAndGateSummary(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return &PostEventReportResponse{
		EventID: eventID, Attendance: *att, GatePerformance: gp,
		Summary:     ReportSummary{TotalRevenue: sales.TotalRevenue},
		GeneratedAt: s.nowFn(),
	}, nil
}

func (s *AnalyticsService) buildReportCSV(ctx context.Context, eventID uuid.UUID) ([]byte, error) {
	rep, err := s.buildReportJSON(ctx, eventID)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("\xEF\xBB\xBF") // UTF-8 BOM
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"event_id", "total_tickets", "used_tickets", "unused_tickets", "no_show_rate", "total_revenue"})
	_ = w.Write([]string{
		escapeCSV(rep.EventID.String()),
		fmt.Sprintf("%d", rep.Attendance.TotalTickets),
		fmt.Sprintf("%d", rep.Attendance.UsedTickets),
		fmt.Sprintf("%d", rep.Attendance.UnusedTickets),
		fmt.Sprintf("%.2f", rep.Attendance.NoShowRate),
		escapeCSV(rep.Summary.TotalRevenue),
	})
	w.Flush()
	return []byte(b.String()), w.Error()
}

// escapeCSV mitigates CSV injection (=,+,-,@).
func escapeCSV(v string) string {
	if v == "" {
		return v
	}
	if strings.ContainsAny(v[:1], "=+-@") {
		return "'" + v
	}
	return v
}
