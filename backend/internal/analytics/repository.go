package analytics

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// InsightRecord is the DB-mapped row for the insights table.
type InsightRecord struct {
	ID             uuid.UUID
	EventID        uuid.UUID
	InsightType    string // GATE_STAFFING | PRICE_RECOMMENDATION | BOT_BLACKLIST | PEAK_TIME
	Title          string
	Description    string
	Recommendation []byte // raw JSONB
	IsApplied      bool
	GeneratedAt    time.Time
	ExpiresAt      *time.Time
}

// AnalyticsRepository is the read surface for EO dashboard + analytics +
// post-event report. All methods accept an event_id filter for RLS-safe
// scoping (organizer-only).
type AnalyticsRepository interface {
	// SalesAndGateSummary returns tickets_sold + total_tickets + revenue
	// aggregated for dashboard's sales block.
	SalesAndGateSummary(ctx context.Context, eventID uuid.UUID) (*SalesBlock, error)

	// GateStatusToday returns real-time gate state map for dashboard.
	GateStatusToday(ctx context.Context, eventID uuid.UUID) (map[string]string, int, error)

	// TotalScansToday returns count of scans (gate + anti-bot) for event today.
	TotalScansToday(ctx context.Context, eventID uuid.UUID) (int, error)

	// AntiBotStats returns anti-bot counters scoped by event.
	AntiBotStats(ctx context.Context, eventID uuid.UUID) (*AntiBotBlock, error)

	// RecentActivity returns latest N scan events for dashboard feed.
	RecentActivity(ctx context.Context, eventID uuid.UUID, limit int) ([]ActivityItem, error)

	// AttendanceMetrics returns total/used/unused tickets + attendance rate.
	AttendanceMetrics(ctx context.Context, eventID uuid.UUID) (*ReportAttendance, error)

	// GateUtilization returns per-gate metrics map for analytics endpoint.
	GateUtilization(ctx context.Context, eventID uuid.UUID) (map[string]GateUtilization, error)

	// ScanTimeline returns hourly scan buckets for analytics chart.
	ScanTimeline(ctx context.Context, eventID uuid.UUID) ([]ScanTimelineBucket, error)

	// LoyaltySplit returns (loyal_customers, new_customers) for event.
	LoyaltySplit(ctx context.Context, eventID uuid.UUID) (int, int, error)

	// ReportGatePerformance returns per-gate performance map for post-event.
	ReportGatePerformance(ctx context.Context, eventID uuid.UUID) (map[string]ReportGatePerformance, error)

	// InsightRepo surface for feedback loop / report generator.
	InsertInsight(ctx context.Context, in *InsightRecord) error
	ExistsByEventAndType(ctx context.Context, eventID uuid.UUID, insightType string) (bool, error)
	ListByEvent(ctx context.Context, eventID uuid.UUID) ([]InsightRecord, error)
}
