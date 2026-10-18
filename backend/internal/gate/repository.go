package gate

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ScanRecord is the DB-mapped row for ticket_scans INSERT.
// Includes both online (scan-qr/verify-pin) and offline sync paths.
type ScanRecord struct {
	ID              uuid.UUID // from scan_id (client UUID)
	TicketID        uuid.UUID
	GateID          string
	ScannerDeviceID *uuid.UUID // nullable (offline sync may omit)
	ScannerUserID   *uuid.UUID // from JWT (gate_staff) if available
	ScanStatus      string     // VALID | INVALID | EXPIRED | ALREADY_USED
	ScanMethod      string     // QR | PIN
	ScannedAt       time.Time
	IPAddress       *string
}

// TicketLookup is the minimal ticket info needed by gate handlers.
type TicketLookup struct {
	ID              uuid.UUID
	OwnerUserID     uuid.UUID
	OwnerName       string
	SectionName     string
	SeatPos         int
	Status          string // CONFIRMED | USED | etc.
	QRSecretKey     string
	FallbackPIN     string
	FallbackPINUsed bool
}

// GateRepository is the read+write surface for ticket_scans + tickets used
// by gate handlers. Pgx-backed implementation wired at composition root.
type GateRepository interface {
	// GetTicketByID fetches ticket + owner + section names for gate decisions.
	GetTicketByID(ctx context.Context, ticketID uuid.UUID) (*TicketLookup, error)

	// GetTicketByFallbackPIN returns the ticket matching the given PIN if
	// fallback_pin_used = FALSE and status = 'CONFIRMED'. Returns
	// ErrTicketNotFound when no match.
	GetTicketByFallbackPIN(ctx context.Context, pin string) (*TicketLookup, error)

	// InsertScan writes a ticket_scans row (idempotent via PK conflict no-op).
	InsertScan(ctx context.Context, scan *ScanRecord) error

	// UpdateTicketStatus marks a ticket as USED (used only on VALID scans
	// that reach the trigger path; kept explicit for offline sync).
	UpdateTicketStatus(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID, status string) error

	// MarkFallbackPINUsed is exposed via ticket package; gate uses ticket
	// repo directly. Included here for completeness in batch sync paths.
	MarkFallbackPINUsed(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (int64, error)

	// GetTicketByIDForUpdate locks a ticket row for concurrent assign/scan
	// (used in offline sync conflict resolution).
	GetTicketByIDForUpdate(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (*TicketLookup, error)

	// EarliestValidScan returns the earliest VALID scan for a ticket. Used
	// by offline sync conflict resolution — earliest scanned_at wins, tie-
	// breaker smallest scan_id (deterministic winner rule).
	EarliestValidScan(ctx context.Context, ticketID uuid.UUID) (*ScanRecord, error)
}
