package event

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// EventRepository provides read access to the event catalog.
type EventRepository interface {
	ListEvents(ctx context.Context, filter ListFilter) ([]Event, int, error)
	GetEventByID(ctx context.Context, id uuid.UUID) (*Event, error)
	GetSectionsByEventID(ctx context.Context, eventID uuid.UUID) ([]Section, error)
	// GetSectionsByEventIDs batches the per-event section lookup into a single
	// query. Used by EventService.List to avoid N+1 on catalog pages.
	GetSectionsByEventIDs(ctx context.Context, eventIDs []uuid.UUID) (map[uuid.UUID][]Section, error)
}

// ListFilter carries validated catalog list filters.
type ListFilter struct {
	Status string
	Search string
	SortBy string
	Page   int
	Limit  int
}

// DBPool is the minimal pgx surface used by PostgresEventRepository.
//
// FIX P9a-10 (TD-F9-01): the repository held a concrete *pgxpool.Pool, so the
// SQL layer could not be unit-tested (no fake pool could be injected) and
// stayed at 0% coverage. *pgxpool.Pool satisfies DBPool, therefore the
// production wiring in cmd/api/main.go is unchanged.
type DBPool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PostgresEventRepository is the pgx-backed EventRepository.
type PostgresEventRepository struct {
	db DBPool
}

func NewPostgresEventRepository(db DBPool) *PostgresEventRepository {
	return &PostgresEventRepository{db: db}
}

var allowedStatuses = map[string]bool{
	"DRAFT":     true,
	"PUBLISHED": true,
	"ON_SALE":   true,
	"SOLD_OUT":  true,
	"COMPLETED": true,
	"CANCELLED": true,
}

var allowedSorts = map[string]bool{
	"":                true,
	"event_date_asc":  true,
	"event_date_desc": true,
	"popularity":      true,
}

// ValidateStatus reports whether status is empty or a known event_status_enum value.
func ValidateStatus(status string) bool {
	if status == "" {
		return true
	}
	return allowedStatuses[status]
}

// ValidateSortBy reports whether sortBy is a supported sort key.
func ValidateSortBy(sortBy string) bool {
	return allowedSorts[sortBy]
}

const eventColumns = `id, organizer_id, name, COALESCE(description, ''), venue_name, venue_address,
	event_date::text, event_start_time::text, COALESCE(event_end_time::text, ''),
	status::text, COALESCE(banner_url, ''), seat_layout_version, created_at, updated_at, published_at`

const eventColumnsQualified = `e.id, e.organizer_id, e.name, COALESCE(e.description, ''), e.venue_name, e.venue_address,
	e.event_date::text, e.event_start_time::text, COALESCE(e.event_end_time::text, ''),
	e.status::text, COALESCE(e.banner_url, ''), e.seat_layout_version, e.created_at, e.updated_at, e.published_at`

func buildListWhere(filter ListFilter) (string, []any) {
	conds := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if filter.Status != "" {
		args = append(args, filter.Status)
		conds = append(conds, fmt.Sprintf("status::text = $%d", len(args)))
	}
	if filter.Search != "" {
		args = append(args, "%"+filter.Search+"%")
		conds = append(conds, fmt.Sprintf("name ILIKE $%d", len(args)))
	}
	if len(conds) == 0 {
		return "TRUE", args
	}
	return strings.Join(conds, " AND "), args
}

func buildOrderBy(sortBy string) string {
	switch sortBy {
	case "event_date_desc":
		return "event_date DESC, event_start_time DESC"
	case "popularity":
		return "created_at DESC"
	default:
		return "event_date ASC, event_start_time ASC"
	}
}

// GetOrganizerID returns the organizer (owner) of an event. Moved here from
// the queue package (TD-F6-07) because event ownership is catalog domain.
func (r *PostgresEventRepository) GetOrganizerID(ctx context.Context, eventID uuid.UUID) (uuid.UUID, error) {
	var orgID uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT organizer_id FROM events WHERE id = $1`, eventID).Scan(&orgID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("event not found: %w", err)
		}
		return uuid.Nil, fmt.Errorf("get organizer id: %w", err)
	}
	return orgID, nil
}

func (r *PostgresEventRepository) ListEvents(ctx context.Context, filter ListFilter) ([]Event, int, error) {
	where, args := buildListWhere(filter)
	order := buildOrderBy(filter.SortBy)
	offset := (filter.Page - 1) * filter.Limit

	listSQL := fmt.Sprintf(
		"SELECT %s FROM events WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d",
		eventColumns, where, order, len(args)+1, len(args)+2,
	)
	listArgs := append(append([]any{}, args...), filter.Limit, offset)

	rows, err := r.db.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate events: %w", err)
	}

	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM events WHERE %s", where)
	var total int
	if err := r.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}
	return events, total, nil
}

func (r *PostgresEventRepository) GetEventByID(ctx context.Context, id uuid.UUID) (*Event, error) {
	q := fmt.Sprintf(
		"SELECT %s, u.name FROM events e JOIN users u ON u.id = e.organizer_id WHERE e.id = $1",
		eventColumnsQualified,
	)
	ev, err := scanEventWithOrganizer(r.db.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("get event by id: %w", err)
	}
	return &ev, nil
}

const sectionColumns = `s.id, s.event_id, s.name, s.price::text, s.total_seats, s.layout_version,
	(s.total_seats - COALESCE(t.cnt, 0))`

const sectionJoin = `FROM sections s
	LEFT JOIN (
		SELECT section_id, COUNT(*) AS cnt
		FROM tickets
		WHERE status NOT IN ('RESOLD', 'CANCELLED')
		GROUP BY section_id
	) t ON t.section_id = s.id`

func (r *PostgresEventRepository) GetSectionsByEventID(ctx context.Context, eventID uuid.UUID) ([]Section, error) {
	q := fmt.Sprintf("SELECT %s %s WHERE s.event_id = $1 ORDER BY s.price ASC", sectionColumns, sectionJoin)
	rows, err := r.db.Query(ctx, q, eventID)
	if err != nil {
		return nil, fmt.Errorf("query sections: %w", err)
	}
	defer rows.Close()

	sections := make([]Section, 0)
	for rows.Next() {
		var sec Section
		if err := rows.Scan(
			&sec.ID, &sec.EventID, &sec.Name, &sec.Price,
			&sec.TotalSeats, &sec.LayoutVersion, &sec.AvailableSeats,
		); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		sections = append(sections, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sections: %w", err)
	}
	return sections, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(s rowScanner) (Event, error) {
	var ev Event
	var desc, banner, endDate sql.NullString
	err := s.Scan(
		&ev.ID, &ev.OrganizerID, &ev.Name, &desc, &ev.VenueName, &ev.VenueAddress,
		&ev.EventDate, &ev.EventStartTime, &endDate, &ev.Status, &banner,
		&ev.SeatLayoutVersion, &ev.CreatedAt, &ev.UpdatedAt, &ev.PublishedAt,
	)
	if err != nil {
		return Event{}, err
	}
	ev.Description = desc.String
	ev.BannerURL = banner.String
	ev.EventEndTime = endDate.String
	return ev, nil
}

func scanEventWithOrganizer(s rowScanner) (Event, error) {
	var ev Event
	var desc, banner, endDate sql.NullString
	err := s.Scan(
		&ev.ID, &ev.OrganizerID, &ev.Name, &desc, &ev.VenueName, &ev.VenueAddress,
		&ev.EventDate, &ev.EventStartTime, &endDate, &ev.Status, &banner,
		&ev.SeatLayoutVersion, &ev.CreatedAt, &ev.UpdatedAt, &ev.PublishedAt,
		&ev.OrganizerName,
	)
	if err != nil {
		return Event{}, err
	}
	ev.Description = desc.String
	ev.BannerURL = banner.String
	ev.EventEndTime = endDate.String
	return ev, nil
}

// GetSectionsByEventIDs fetches sections for multiple events in one query,
// grouped by event_id. Empty input returns an empty (non-nil) map.
func (r *PostgresEventRepository) GetSectionsByEventIDs(ctx context.Context, eventIDs []uuid.UUID) (map[uuid.UUID][]Section, error) {
	out := make(map[uuid.UUID][]Section, len(eventIDs))
	if len(eventIDs) == 0 {
		return out, nil
	}
	q := fmt.Sprintf("SELECT %s %s WHERE s.event_id = ANY($1) ORDER BY s.event_id, s.price ASC", sectionColumns, sectionJoin)
	rows, err := r.db.Query(ctx, q, eventIDs)
	if err != nil {
		return nil, fmt.Errorf("query sections by events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sec Section
		if err := rows.Scan(
			&sec.ID, &sec.EventID, &sec.Name, &sec.Price,
			&sec.TotalSeats, &sec.LayoutVersion, &sec.AvailableSeats,
		); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		out[sec.EventID] = append(out[sec.EventID], sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sections: %w", err)
	}
	return out, nil
}
