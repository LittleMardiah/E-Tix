package ticket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidStatusFilter is returned when the status query param is not allowed.
var ErrInvalidStatusFilter = errors.New("invalid status filter")

// ErrTicketForbidden is returned when the caller is not the ticket owner.
var ErrTicketForbidden = errors.New("ticket forbidden")

// ErrTicketNotActive is returned when QR is requested for a ticket whose
// status is not CONFIRMED or RESALE_PENDING.
var ErrTicketNotActive = errors.New("ticket not active for QR")

// ErrTicketAlreadyAssigned is returned when a ticket is already assigned.
var ErrTicketAlreadyAssigned = errors.New("ticket already assigned")

// ErrTicketNotConfirmed is returned when the ticket is not CONFIRMED.
var ErrTicketNotConfirmed = errors.New("ticket not confirmed")

// ErrTargetUserInactive is returned when the target user is not ACTIVE or blacklisted.
var ErrTargetUserInactive = errors.New("target user inactive or suspended")

// ErrAssignConflict is returned when concurrent assign lost the race.
var ErrAssignConflict = errors.New("assign conflict (concurrent)")

// QRStore abstracts the QR cache backend (Redis-backed in production).
type QRStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

// TxBeginner abstracts the transaction opener (production: *pgxpool.Pool).
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
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
	db      TxBeginner
	nowFn   func() time.Time
}

// NewTicketService constructs a TicketService.
func NewTicketService(repo TicketRepository, qrStore QRStore, db TxBeginner) *TicketService {
	return &TicketService{repo: repo, qrStore: qrStore, db: db, nowFn: time.Now}
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

// Assign transfers ownership of a CONFIRMED, unassigned ticket to a target
// user. All steps run in a single transaction with FOR UPDATE on the ticket
// row. Idempotency is enforced at the handler layer (E6-B).
func (s *TicketService) Assign(ctx context.Context, ownerID, ticketID, targetUserID uuid.UUID) (*Ticket, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("ticket assign: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ticket, err := s.repo.GetByIDForUpdate(ctx, tx, ticketID)
	if err != nil {
		return nil, err
	}
	if ticket.OwnerUserID != ownerID {
		return nil, ErrTicketForbidden
	}
	if ticket.Status != "CONFIRMED" {
		return nil, ErrTicketNotConfirmed
	}
	if ticket.IsAssigned {
		return nil, ErrTicketAlreadyAssigned
	}

	status, blacklisted, err := s.repo.GetUserStatusAndBlacklist(ctx, tx, targetUserID)
	if err != nil {
		return nil, err
	}
	if status != "ACTIVE" || blacklisted {
		return nil, ErrTargetUserInactive
	}

	rows, err := s.repo.UpdateAssign(ctx, tx, ticketID, targetUserID)
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, ErrAssignConflict
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("ticket assign: commit: %w", err)
	}

	ticket.OwnerUserID = targetUserID
	orig := ownerID
	ticket.OriginalOwnerUserID = &orig
	ticket.IsAssigned = true
	return ticket, nil
}

// CacheGet is used by the handler to store/retrieve idempotency responses
// for PATCH /tickets/{id}/assign. Reuses the qrStore interface with a
// dedicated key prefix (idem:assign:). L1-only (non-financial operation).
func (s *TicketService) CacheGet(ctx context.Context, key string) (string, error) {
	return s.qrStore.Get(ctx, key)
}

// CacheSet stores an idempotency response with the given TTL.
func (s *TicketService) CacheSet(ctx context.Context, key, value string, ttl time.Duration) error {
	return s.qrStore.Set(ctx, key, value, ttl)
}
