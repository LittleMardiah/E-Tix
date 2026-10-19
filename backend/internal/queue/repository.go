package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EventRepository provides read access to event status.
type EventRepository interface {
	GetEventStatus(ctx context.Context, eventID uuid.UUID) (string, error)
}

// PostgresEventRepository is the pgx-backed EventRepository.
type PostgresEventRepository struct {
	db *pgxpool.Pool
}

func NewPostgresEventRepository(db *pgxpool.Pool) *PostgresEventRepository {
	return &PostgresEventRepository{db: db}
}

// GetOrganizerID returns the organizer (owner) of an event. Satisfies
// analytics.EventOwnerReader for dashboard/analytics/report ownership check.
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

func (r *PostgresEventRepository) GetEventStatus(ctx context.Context, eventID uuid.UUID) (string, error) {
	var status string
	err := r.db.QueryRow(ctx, `SELECT status FROM events WHERE id = $1`, eventID).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("event not found: %w", err)
		}
		return "", fmt.Errorf("get event status: %w", err)
	}
	return status, nil
}
