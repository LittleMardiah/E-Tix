package analytics

import (
	"time"

	"github.com/google/uuid"
)

// SalesBlock is the sales summary in dashboard.
type SalesBlock struct {
	TicketsSold     int     `json:"tickets_sold"`
	TotalTickets    int     `json:"total_tickets"`
	SalesPercentage float64 `json:"sales_percentage"`
	RevenueToday    string  `json:"revenue_today"`
	TotalRevenue    string  `json:"total_revenue"`
}

// GateStatusBlock is the real-time gate summary. Gates holds a map of
// dynamic gate_id -> status ("ACTIVE"|"IDLE"). Extensible: any number
// of gates without struct change.
type GateStatusBlock struct {
	TotalGatesActive int               `json:"total_gates_active"`
	Gates            map[string]string `json:"gates,omitempty"`
	TotalScansToday  int               `json:"total_scans_today"`
}

// AntiBotBlock is the anti-bot stats in dashboard.
type AntiBotBlock struct {
	RequestsBlockedToday int `json:"requests_blocked_today"`
	SuspiciousIPsFlagged int `json:"suspicious_ips_flagged"`
	CaptchaTriggersToday int `json:"captcha_triggers_today"`
}

// ActivityItem is one entry of recent_activity[].
type ActivityItem struct {
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
}

// Alert is one entry of alerts[].
type Alert struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// DashboardResponse is the body of GET /events/{id}/dashboard.
type DashboardResponse struct {
	EventID        uuid.UUID       `json:"event_id"`
	EventName      string          `json:"event_name"`
	EventStatus    string          `json:"event_status"`
	Sales          SalesBlock      `json:"sales"`
	GateStatus     GateStatusBlock `json:"gate_status"`
	AntiBot        AntiBotBlock    `json:"anti_bot"`
	RecentActivity []ActivityItem  `json:"recent_activity"`
	Alerts         []Alert         `json:"alerts"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// GateUtilization is one gate's metrics in analytics.
type GateUtilization struct {
	TotalScans  int `json:"total_scans"`
	ValidScans  int `json:"valid_scans"`
	Utilization int `json:"utilization"`
}

// ScanTimelineBucket is one hour bucket in scan_timeline[].
type ScanTimelineBucket struct {
	Hour  string `json:"hour"`
	Scans int    `json:"scans"`
}

// AnalyticsResponse is the body of GET /events/{id}/analytics.
type AnalyticsResponse struct {
	EventID         uuid.UUID                  `json:"event_id"`
	EventName       string                     `json:"event_name"`
	TotalTickets    int                        `json:"total_tickets"`
	TicketsSold     int                        `json:"tickets_sold"`
	AttendanceCount int                        `json:"attendance_count"`
	AttendanceRate  float64                    `json:"attendance_rate"`
	NoShowRate      float64                    `json:"no_show_rate"`
	PeakEntryTime   string                     `json:"peak_entry_time"`
	GateUtilization map[string]GateUtilization `json:"gate_utilization"`
	ScanTimeline    []ScanTimelineBucket       `json:"scan_timeline"`
	LoyalCustomers  int                        `json:"loyal_customers"`
	NewCustomers    int                        `json:"new_customers"`
	GeneratedAt     time.Time                  `json:"generated_at"`
}

// ReportSummary is the summary block of post-event report.
type ReportSummary struct {
	TotalRevenue       string `json:"total_revenue"`
	AverageTicketPrice string `json:"average_ticket_price"`
	PlatformFeeEarned  string `json:"platform_fee_earned"`
	RefundAmount       string `json:"refund_amount"`
}

// ReportAttendance is the attendance block of post-event report.
type ReportAttendance struct {
	TotalTickets  int     `json:"total_tickets"`
	UsedTickets   int     `json:"used_tickets"`
	UnusedTickets int     `json:"unused_tickets"`
	NoShowRate    float64 `json:"no_show_rate"`
	ResaleCount   int     `json:"resale_count"`
	ResaleRevenue string  `json:"resale_revenue"`
}

// ReportAntiBot is the anti_bot block of post-event report (API_CONTRACT §13.3).
type ReportAntiBot struct {
	TotalRequestsBlocked int     `json:"total_requests_blocked"`
	BotFilteringRate     float64 `json:"bot_filtering_rate"`
	SuspiciousIPsFlagged int     `json:"suspicious_ips_flagged"`
	CaptchaTriggers      int     `json:"captcha_triggers"`
}

// ReportGatePerformance is one gate's row in gate_performance.
type ReportGatePerformance struct {
	TotalScans   int `json:"total_scans"`
	ValidScans   int `json:"valid_scans"`
	InvalidScans int `json:"invalid_scans"`
}

// ReportInsights is the insights block of post-event report (API_CONTRACT §13.3).
type ReportInsights struct {
	Recommendations []string `json:"recommendations"`
}

// PostEventReportResponse is the body of GET /events/{id}/post-event-report.
type PostEventReportResponse struct {
	EventID         uuid.UUID                        `json:"event_id"`
	EventName       string                           `json:"event_name"`
	Summary         ReportSummary                    `json:"summary"`
	Attendance      ReportAttendance                 `json:"attendance"`
	AntiBot         ReportAntiBot                    `json:"anti_bot"`
	GatePerformance map[string]ReportGatePerformance `json:"gate_performance"`
	Insights        ReportInsights                   `json:"insights"`
	GeneratedAt     time.Time                        `json:"generated_at"`
}
