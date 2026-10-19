package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

// ErrAnalyticsNotFound is returned when the target event doesn't exist.
var ErrAnalyticsNotFound = errors.New("analytics: event not found")

// PostgresAnalyticsRepository is the pgx-backed implementation.
type PostgresAnalyticsRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresAnalyticsRepository constructs a PostgresAnalyticsRepository.
func NewPostgresAnalyticsRepository(pool *pgxpool.Pool) *PostgresAnalyticsRepository {
	return &PostgresAnalyticsRepository{pool: pool}
}

// SalesAndGateSummary aggregates revenue + ticket counts for the event.
func (r *PostgresAnalyticsRepository) SalesAndGateSummary(ctx context.Context, eventID uuid.UUID) (*SalesBlock, error) {
	q := `
		SELECT 
			COUNT(*) FILTER (WHERE t.status IN ('CONFIRMED','USED')) AS sold,
			(SELECT COALESCE(SUM(s.total_seats),0)::int FROM sections s WHERE s.event_id = $1) AS total,
			COALESCE(SUM(CASE WHEN t.status IN ('CONFIRMED','USED') THEN s.price ELSE 0 END),0)::text AS revenue
		FROM tickets t
		JOIN sections s ON s.id = t.section_id
		JOIN events e ON e.id = $1
		WHERE s.event_id = $1`
	var sold, total int
	var revenue string
	if err := r.pool.QueryRow(ctx, q, eventID).Scan(&sold, &total, &revenue); err != nil {
		return nil, fmt.Errorf("analytics: sales: %w", err)
	}
	pct := 0.0
	if total > 0 {
		pct = float64(sold) / float64(total) * 100
	}
	return &SalesBlock{
		TicketsSold: sold, TotalTickets: total, SalesPercentage: pct,
		RevenueToday: revenue, TotalRevenue: revenue,
	}, nil
}

// GateStatusToday returns gate status map + active count + total scans today.
func (r *PostgresAnalyticsRepository) GateStatusToday(ctx context.Context, eventID uuid.UUID) (map[string]string, int, error) {
	q := `
		SELECT ts.gate_id, COUNT(*) FILTER (WHERE ts.scanned_at >= CURRENT_DATE) AS cnt
		FROM ticket_scans ts
		JOIN tickets t ON t.id = ts.ticket_id
		JOIN sections s ON s.id = t.section_id
		WHERE s.event_id = $1 AND ts.scanned_at >= CURRENT_DATE
		GROUP BY ts.gate_id`
	rows, err := r.pool.Query(ctx, q, eventID)
	if err != nil {
		return nil, 0, fmt.Errorf("analytics: gate status: %w", err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var gate string
		var cnt int
		if err := rows.Scan(&gate, &cnt); err != nil {
			return nil, 0, err
		}
		if cnt > 0 {
			m[gate] = "ACTIVE"
		} else {
			m[gate] = "IDLE"
		}
	}
	return m, len(m), nil
}

// TotalScansToday returns scan count today for event.
func (r *PostgresAnalyticsRepository) TotalScansToday(ctx context.Context, eventID uuid.UUID) (int, error) {
	q := `
		SELECT COUNT(*) FROM ticket_scans ts
		JOIN tickets t ON t.id = ts.ticket_id
		JOIN sections s ON s.id = t.section_id
		WHERE s.event_id = $1 AND ts.scanned_at >= CURRENT_DATE`
	var n int
	if err := r.pool.QueryRow(ctx, q, eventID).Scan(&n); err != nil {
		return 0, fmt.Errorf("analytics: total scans: %w", err)
	}
	return n, nil
}

// AntiBotStats returns placeholder counters (data comes from external systems in future).
func (r *PostgresAnalyticsRepository) AntiBotStats(ctx context.Context, eventID uuid.UUID) (*AntiBotBlock, error) {
	// Static zero counters — anti-bot data lives in middleware layer, not DB.
	return &AntiBotBlock{}, nil
}

// RecentActivity returns latest N scan events for dashboard feed.
func (r *PostgresAnalyticsRepository) RecentActivity(ctx context.Context, eventID uuid.UUID, limit int) ([]ActivityItem, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	q := `
		SELECT ts.scanned_at, ts.scan_status::text, ts.gate_id
		FROM ticket_scans ts
		JOIN tickets t ON t.id = ts.ticket_id
		JOIN sections s ON s.id = t.section_id
		WHERE s.event_id = $1
		ORDER BY ts.scanned_at DESC
		LIMIT $2`
	rows, err := r.pool.Query(ctx, q, eventID, limit)
	if err != nil {
		return nil, fmt.Errorf("analytics: recent: %w", err)
	}
	defer rows.Close()
	out := []ActivityItem{}
	for rows.Next() {
		var item ActivityItem
		var status, gate string
		if err := rows.Scan(&item.Timestamp, &status, &gate); err != nil {
			return nil, err
		}
		item.Type = "TICKET_SCANNED"
		item.Description = fmt.Sprintf("%s at %s", status, gate)
		out = append(out, item)
	}
	return out, rows.Err()
}

// AttendanceMetrics returns total/used/unused + rates.
func (r *PostgresAnalyticsRepository) AttendanceMetrics(ctx context.Context, eventID uuid.UUID) (*ReportAttendance, error) {
	q := `
		SELECT 
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE t.status = 'USED') AS used,
			COUNT(*) FILTER (WHERE t.status = 'CONFIRMED') AS unused
		FROM tickets t
		JOIN sections s ON s.id = t.section_id
		WHERE s.event_id = $1`
	var total, used, unused int
	if err := r.pool.QueryRow(ctx, q, eventID).Scan(&total, &used, &unused); err != nil {
		return nil, fmt.Errorf("analytics: attendance: %w", err)
	}
	noShow := 0.0
	if total > 0 {
		noShow = float64(unused) / float64(total) * 100
	}
	return &ReportAttendance{
		TotalTickets: total, UsedTickets: used, UnusedTickets: unused, NoShowRate: noShow,
	}, nil
}

// GateUtilization returns per-gate metrics map.
func (r *PostgresAnalyticsRepository) GateUtilization(ctx context.Context, eventID uuid.UUID) (map[string]GateUtilization, error) {
	q := `
		SELECT ts.gate_id,
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE ts.scan_status::text = 'VALID') AS valid
		FROM ticket_scans ts
		JOIN tickets t ON t.id = ts.ticket_id
		JOIN sections s ON s.id = t.section_id
		WHERE s.event_id = $1
		GROUP BY ts.gate_id`
	rows, err := r.pool.Query(ctx, q, eventID)
	if err != nil {
		return nil, fmt.Errorf("analytics: gate util: %w", err)
	}
	defer rows.Close()
	out := map[string]GateUtilization{}
	max := 0
	for rows.Next() {
		var g string
		var total, valid int
		if err := rows.Scan(&g, &total, &valid); err != nil {
			return nil, err
		}
		if total > max {
			max = total
		}
		out[g] = GateUtilization{TotalScans: total, ValidScans: valid}
	}
	// Compute utilization as % of max.
	for k, v := range out {
		if max > 0 {
			v.Utilization = v.TotalScans * 100 / max
			out[k] = v
		}
	}
	return out, rows.Err()
}

// ScanTimeline returns hourly scan buckets.
func (r *PostgresAnalyticsRepository) ScanTimeline(ctx context.Context, eventID uuid.UUID) ([]ScanTimelineBucket, error) {
	q := `
		SELECT TO_CHAR(ts.scanned_at, 'HH24:00') AS hr, COUNT(*)
		FROM ticket_scans ts
		JOIN tickets t ON t.id = ts.ticket_id
		JOIN sections s ON s.id = t.section_id
		WHERE s.event_id = $1
		GROUP BY hr ORDER BY hr ASC`
	rows, err := r.pool.Query(ctx, q, eventID)
	if err != nil {
		return nil, fmt.Errorf("analytics: timeline: %w", err)
	}
	defer rows.Close()
	out := []ScanTimelineBucket{}
	for rows.Next() {
		var b ScanTimelineBucket
		if err := rows.Scan(&b.Hour, &b.Scans); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LoyaltySplit returns (loyal, new) customer counts.
func (r *PostgresAnalyticsRepository) LoyaltySplit(ctx context.Context, eventID uuid.UUID) (int, int, error) {
	q := `
		SELECT 
			COUNT(DISTINCT u.id) FILTER (WHERE u.total_purchases > 1) AS loyal,
			COUNT(DISTINCT u.id) FILTER (WHERE u.total_purchases <= 1) AS new_cust
		FROM users u
		JOIN tickets t ON t.owner_user_id = u.id
		JOIN sections s ON s.id = t.section_id
		WHERE s.event_id = $1`
	var loyal, newCust int
	if err := r.pool.QueryRow(ctx, q, eventID).Scan(&loyal, &newCust); err != nil {
		return 0, 0, fmt.Errorf("analytics: loyalty: %w", err)
	}
	return loyal, newCust, nil
}

// ReportGatePerformance returns per-gate stats for post-event report.
func (r *PostgresAnalyticsRepository) ReportGatePerformance(ctx context.Context, eventID uuid.UUID) (map[string]ReportGatePerformance, error) {
	q := `
		SELECT ts.gate_id,
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE ts.scan_status::text = 'VALID') AS valid,
			COUNT(*) FILTER (WHERE ts.scan_status::text != 'VALID') AS invalid
		FROM ticket_scans ts
		JOIN tickets t ON t.id = ts.ticket_id
		JOIN sections s ON s.id = t.section_id
		WHERE s.event_id = $1
		GROUP BY ts.gate_id`
	rows, err := r.pool.Query(ctx, q, eventID)
	if err != nil {
		return nil, fmt.Errorf("analytics: gate perf: %w", err)
	}
	defer rows.Close()
	out := map[string]ReportGatePerformance{}
	for rows.Next() {
		var g string
		var p ReportGatePerformance
		if err := rows.Scan(&g, &p.TotalScans, &p.ValidScans, &p.InvalidScans); err != nil {
			return nil, err
		}
		out[g] = p
	}
	return out, rows.Err()
}

// InsertInsight adds a new insight row.
func (r *PostgresAnalyticsRepository) InsertInsight(ctx context.Context, in *InsightRecord) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO insights (event_id, insight_type, title, description, recommendation, generated_at)
		VALUES ($1, $2::insight_type_enum, $3, $4, $5, COALESCE($6, NOW()))`,
		in.EventID, in.InsightType, in.Title, in.Description, in.Recommendation, in.GeneratedAt)
	if err != nil {
		return fmt.Errorf("analytics: insert insight: %w", err)
	}
	return nil
}

// ExistsByEventAndType checks idempotency for insight generation.
func (r *PostgresAnalyticsRepository) ExistsByEventAndType(ctx context.Context, eventID uuid.UUID, insightType string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM insights WHERE event_id = $1 AND insight_type::text = $2)`,
		eventID, insightType).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("analytics: exists: %w", err)
	}
	return exists, nil
}

// ListByEvent returns all insights for an event.
func (r *PostgresAnalyticsRepository) ListByEvent(ctx context.Context, eventID uuid.UUID) ([]InsightRecord, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, event_id, insight_type::text, title, description, recommendation, 
		       is_applied, generated_at, expires_at
		FROM insights WHERE event_id = $1 ORDER BY generated_at DESC`, eventID)
	if err != nil {
		return nil, fmt.Errorf("analytics: list insights: %w", err)
	}
	defer rows.Close()
	out := []InsightRecord{}
	for rows.Next() {
		var r InsightRecord
		if err := rows.Scan(&r.ID, &r.EventID, &r.InsightType, &r.Title, &r.Description,
			&r.Recommendation, &r.IsApplied, &r.GeneratedAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
