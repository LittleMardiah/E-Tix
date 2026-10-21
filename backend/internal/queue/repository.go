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
