package analytics

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOwners is a stub EventOwnerReader for ownership-check tests.
type fakeOwners struct {
	orgID uuid.UUID
}

func (f fakeOwners) GetOrganizerID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.orgID, nil
}

func TestEscapeCSV_FormulaInjection(t *testing.T) {
	cases := []struct{ in, want string }{
		{"=1+1", "'=1+1"},
		{"+123", "'+123"},
		{"-cmd", "'-cmd"},
		{"@user", "'@user"},
		{"normal", "normal"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, escapeCSV(c.in))
	}
}

func TestEscapeCSV_Empty(t *testing.T) {
	assert.Equal(t, "", escapeCSV(""))
}

func TestGetPostEventReport_PDFStub(t *testing.T) {
	callerID := uuid.New()
	eventID := uuid.New()
	owners := &fakeOwners{orgID: callerID}
	svc := NewAnalyticsService(nil, owners)
	_, err := svc.GetPostEventReport(context.Background(), callerID, eventID, "organizer", "pdf")
	require.ErrorIs(t, err, ErrPDFNotImplemented)
}

func TestGetDashboard_Forbidden(t *testing.T) {
	caller := uuid.New()
	other := uuid.New()
	svc := NewAnalyticsService(nil, fakeOwners{orgID: other})
	_, err := svc.GetDashboard(context.Background(), caller, uuid.New(), "organizer")
	require.ErrorIs(t, err, ErrEventForbidden)
}

// TestGetDashboard_AdminBypass verifies B2 fix: admin role bypasses checkOwner.
func TestGetDashboard_AdminBypass(t *testing.T) {
	eventID := uuid.New()
	callerAdminID := uuid.New()
	orgID := uuid.New()

	owners := &fakeOwners{orgID: orgID}
	repo := &stubAnalyticsRepo{}

	svc := NewAnalyticsService(repo, owners)
	resp, err := svc.GetDashboard(context.Background(), callerAdminID, eventID, "admin")
	require.NoError(t, err, "admin must bypass ownership check")
	require.NotNil(t, resp)
	assert.Equal(t, eventID, resp.EventID)
}

// stubAnalyticsRepo returns zero values for dashboard-callable methods.
type stubAnalyticsRepo struct{}

func (s *stubAnalyticsRepo) SalesAndGateSummary(_ context.Context, _ uuid.UUID) (*SalesBlock, error) {
	return &SalesBlock{}, nil
}
func (s *stubAnalyticsRepo) GateStatusToday(_ context.Context, _ uuid.UUID) (map[string]string, int, error) {
	return map[string]string{}, 0, nil
}
func (s *stubAnalyticsRepo) TotalScansToday(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (s *stubAnalyticsRepo) AntiBotStats(_ context.Context, _ uuid.UUID) (*AntiBotBlock, error) {
	return &AntiBotBlock{}, nil
}
func (s *stubAnalyticsRepo) RecentActivity(_ context.Context, _ uuid.UUID, _ int) ([]ActivityItem, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) AttendanceMetrics(_ context.Context, _ uuid.UUID) (*ReportAttendance, error) {
	return &ReportAttendance{}, nil
}
func (s *stubAnalyticsRepo) GateUtilization(_ context.Context, _ uuid.UUID) (map[string]GateUtilization, error) {
	return map[string]GateUtilization{}, nil
}
func (s *stubAnalyticsRepo) ScanTimeline(_ context.Context, _ uuid.UUID) ([]ScanTimelineBucket, error) {
	return nil, nil
}
func (s *stubAnalyticsRepo) LoyaltySplit(_ context.Context, _ uuid.UUID) (int, int, error) {
	return 0, 0, nil
}
func (s *stubAnalyticsRepo) ReportGatePerformance(_ context.Context, _ uuid.UUID) (map[string]ReportGatePerformance, error) {
	return map[string]ReportGatePerformance{}, nil
}
func (s *stubAnalyticsRepo) InsertInsight(_ context.Context, _ *InsightRecord) error { return nil }
func (s *stubAnalyticsRepo) ExistsByEventAndType(_ context.Context, _ uuid.UUID, _ string) (bool, error) {
	return false, nil
}
func (s *stubAnalyticsRepo) ListByEvent(_ context.Context, _ uuid.UUID) ([]InsightRecord, error) {
	return nil, nil
}
