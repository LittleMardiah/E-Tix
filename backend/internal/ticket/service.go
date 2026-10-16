package ticket

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidStatusFilter is returned when the status query param is not allowed.
var ErrInvalidStatusFilter = errors.New("invalid status filter")

// ErrTicketForbidden is returned when the caller is not the ticket owner.
var ErrTicketForbidden = errors.New("ticket forbidden")

// ErrTicketNotActive is returned when QR is requested for a ticket whose
// status is not CONFIRMED or RESALE_PENDING.
var ErrTicketNotActive = errors.New("ticket not active for QR")

// QRStore abstracts the QR cache backend (Redis-backed in production).
type QRStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

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
	repo    TicketRepository
	qrStore QRStore
	nowFn   func() time.Time
}

// NewTicketService constructs a TicketService.
func NewTicketService(repo TicketRepository, qrStore QRStore) *TicketService {
	return &TicketService{repo: repo, qrStore: qrStore, nowFn: time.Now}
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

const (
	qrCacheTTLSeconds = 35
)

// GetQR returns the current QR payload for a ticket, using a Redis-backed
// cache with on-demand regeneration. refresh_in / expiresAt reflect the
// current timestamp block boundary.
func (s *TicketService) GetQR(ctx context.Context, ownerID, ticketID uuid.UUID) (*QRResponse, error) {
	td, err := s.repo.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if td.OwnerUserID != ownerID {
		return nil, ErrTicketForbidden
	}
	if td.Status != "CONFIRMED" && td.Status != "RESALE_PENDING" {
		return nil, ErrTicketNotActive
	}

	now := s.nowFn()
	blockEnd := ((now.Unix() / QRTimestampBlockSeconds) + 1) * QRTimestampBlockSeconds
	refreshIn := int(blockEnd - now.Unix())
	expiresAt := time.Unix(blockEnd, 0).UTC()

	key := "qr:" + ticketID.String()
	cached, err := s.qrStore.Get(ctx, key)
	if err == nil && cached != "" {
		return &QRResponse{QRData: cached, ExpiresAt: expiresAt, RefreshIn: refreshIn}, nil
	}

	qrData, err := GenerateQRData(ticketID, ownerID, td.QRSecretKey, now)
	if err != nil {
		return nil, err
	}
	_ = s.qrStore.Set(ctx, key, qrData, time.Duration(qrCacheTTLSeconds)*time.Second)
	return &QRResponse{QRData: qrData, ExpiresAt: expiresAt, RefreshIn: refreshIn}, nil
}
