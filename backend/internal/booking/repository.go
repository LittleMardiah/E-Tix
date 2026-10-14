package booking

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SectionRow struct {
	ID            uuid.UUID
	EventID       uuid.UUID
	Name          string
	Price         string
	TotalSeats    int
	LayoutVersion int
}

type BookingRepository interface {
	GetSectionByID(ctx context.Context, sectionID uuid.UUID) (*SectionRow, error)
	GetTakenSeats(ctx context.Context, sectionID uuid.UUID) ([]int, error)
}

type PostgresBookingRepository struct {
	db *pgxpool.Pool
}

func NewPostgresBookingRepository(db *pgxpool.Pool) BookingRepository {
	return &PostgresBookingRepository{db: db}
}

func (r *PostgresBookingRepository) GetSectionByID(ctx context.Context, sectionID uuid.UUID) (*SectionRow, error) {
	var s SectionRow
	err := r.db.QueryRow(ctx, `
		SELECT id, event_id, name, price::text, total_seats, layout_version
		FROM sections WHERE id = $1`, sectionID,
	).Scan(&s.ID, &s.EventID, &s.Name, &s.Price, &s.TotalSeats, &s.LayoutVersion)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("booking: section not found")
		}
		return nil, fmt.Errorf("booking: get section: %w", err)
	}
	return &s, nil
}

func (r *PostgresBookingRepository) GetTakenSeats(ctx context.Context, sectionID uuid.UUID) ([]int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT seat_position FROM tickets
		WHERE section_id = $1
		AND status IN ('CONFIRMED','LOCKED','PENDING_SPLIT_PAY','RESALE_PENDING')`,
		sectionID)
	if err != nil {
		return nil, fmt.Errorf("booking: get taken seats: %w", err)
	}
	defer rows.Close()

	var taken []int
	for rows.Next() {
		var pos int
		if err := rows.Scan(&pos); err != nil {
			return nil, err
		}
		taken = append(taken, pos)
	}
	return taken, rows.Err()
}
