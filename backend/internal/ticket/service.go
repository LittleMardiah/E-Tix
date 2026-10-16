package ticket

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrInvalidStatusFilter is returned when the status query param is not allowed.
var ErrInvalidStatusFilter = errors.New("invalid status filter")

// ErrTicketForbidden is returned when the caller is not the ticket owner.
var ErrTicketForbidden = errors.New("ticket forbidden")

const (
	defaultListLimit = 20
	maxListLimit     = 50
)

var allowedStatusFilter = map[string]bool{
	"":               true,
	"CONFIRMED":      true,
	"USED":           true,
	"RESALE_PENDING": true,
	"RESOLD":         true,
	"CANCELLED":      true,
}

// TicketService holds business logic for ticket endpoints.
type TicketService struct {
	repo TicketRepository
}

// NewTicketService constructs a TicketService.
func NewTicketService(repo TicketRepository) *TicketService {
	return &TicketService{repo: repo}
}

// ListByOwner returns paginated tickets for the owner with filter validation.
func (s *TicketService) ListByOwner(ctx context.Context, ownerID uuid.UUID, statusFilter string, page, limit int) ([]TicketListItem, int, error) {
	if !allowedStatusFilter[statusFilter] {
		return nil, 0, ErrInvalidStatusFilter
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > maxListLimit {
		limit = defaultListLimit
	}
	return s.repo.ListByOwner(ctx, ownerID, statusFilter, page, limit)
}

// GetByID returns a ticket detail, enforcing ownership at the service layer.
func (s *TicketService) GetByID(ctx context.Context, ownerID, ticketID uuid.UUID) (*TicketDetail, error) {
	td, err := s.repo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if td.OwnerUserID != ownerID {
		return nil, ErrTicketForbidden
	}
	return td, nil
}
