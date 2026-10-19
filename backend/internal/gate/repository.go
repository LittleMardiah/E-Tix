package gate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LittleMardiah/E-Tix/backend/internal/ticket"
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

	// GetTicketByFallbackPINForUpdate is the row-locked variant (TD-F6-01).
	GetTicketByFallbackPINForUpdate(ctx context.Context, tx pgx.Tx, pin string) (*TicketLookup, error)

	// InsertScan writes a ticket_scans row (idempotent via PK conflict no-op).
	InsertScan(ctx context.Context, scan *ScanRecord) error

	// InsertScanTx is the tx-aware variant for atomic scan + status update.
	// Returns inserted=true on new row, false on duplicate (idempotent no-op).
	InsertScanTx(ctx context.Context, tx pgx.Tx, scan *ScanRecord) (bool, error)

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

	// GetEarliestValidScanByTicketIDs returns earliest VALID scan per ticket
	// (batch). For offline sync conflict resolution.
	GetEarliestValidScanByTicketIDs(ctx context.Context, tx pgx.Tx, ticketIDs []uuid.UUID) (map[uuid.UUID]*ScanRecord, error)

	// UpdateScanStatus overwrites scan_status (used to mark loser as ALREADY_USED).
	UpdateScanStatus(ctx context.Context, tx pgx.Tx, scanID uuid.UUID, status string) error
}

// ErrTicketNotFound is returned when no ticket matches the given lookup.
var ErrTicketNotFound = ticket.ErrTicketNotFound

// PostgresGateRepository is the pgx-backed implementation of GateRepository.
type PostgresGateRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresGateRepository constructs a PostgresGateRepository.
func NewPostgresGateRepository(pool *pgxpool.Pool) *PostgresGateRepository {
	return &PostgresGateRepository{pool: pool}
}

const ticketLookupSQL = `
	SELECT t.id, t.owner_user_id, u.name, s.name, t.seat_position,
	       t.status::text, t.qr_secret_key, t.fallback_pin, t.fallback_pin_used
	FROM tickets t
	JOIN users u ON u.id = t.owner_user_id
	JOIN sections s ON s.id = t.section_id`

func scanTicketLookup(s interface{ Scan(...any) error }) (*TicketLookup, error) {
	var tl TicketLookup
	if err := s.Scan(
		&tl.ID, &tl.OwnerUserID, &tl.OwnerName, &tl.SectionName, &tl.SeatPos,
		&tl.Status, &tl.QRSecretKey, &tl.FallbackPIN, &tl.FallbackPINUsed,
	); err != nil {
		return nil, err
	}
	return &tl, nil
}

// GetTicketByID fetches ticket + owner + section names.
func (r *PostgresGateRepository) GetTicketByID(ctx context.Context, ticketID uuid.UUID) (*TicketLookup, error) {
	row := r.pool.QueryRow(ctx, ticketLookupSQL+` WHERE t.id = $1`, ticketID)
	tl, err := scanTicketLookup(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("gate: get ticket: %w", err)
	}
	return tl, nil
}

// GetTicketByFallbackPIN returns the ticket matching the PIN if unused + CONFIRMED.
func (r *PostgresGateRepository) GetTicketByFallbackPIN(ctx context.Context, pin string) (*TicketLookup, error) {
	row := r.pool.QueryRow(ctx, ticketLookupSQL+
		` WHERE t.fallback_pin = $1 AND t.fallback_pin_used = FALSE AND t.status::text = 'CONFIRMED'`, pin)
	tl, err := scanTicketLookup(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("gate: get ticket by pin: %w", err)
	}
	return tl, nil
}

// GetTicketByFallbackPINForUpdate is the row-locked variant (TD-F6-01).
func (r *PostgresGateRepository) GetTicketByFallbackPINForUpdate(ctx context.Context, tx pgx.Tx, pin string) (*TicketLookup, error) {
	// No status filter: allows service to distinguish 409 (already used) vs
	// 422 (not active) per API_CONTRACT §11.2. See TD-F6-04.
	row := tx.QueryRow(ctx, ticketLookupSQL+
		` WHERE t.fallback_pin = $1 FOR UPDATE`, pin)
	tl, err := scanTicketLookup(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("gate: get ticket by pin for update: %w", err)
	}
	return tl, nil
}

// InsertScan writes a ticket_scans row (idempotent via PK conflict no-op).
func (r *PostgresGateRepository) InsertScan(ctx context.Context, scan *ScanRecord) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO ticket_scans (
			id, ticket_id, gate_id, scanner_device_id, scanner_user_id,
			scan_status, scan_method, scanned_at, ip_address
		) VALUES ($1,$2,$3,$4,$5,$6::scan_status_enum,$7,$8,$9)
		ON CONFLICT (id) DO NOTHING`,
		scan.ID, scan.TicketID, scan.GateID, scan.ScannerDeviceID, scan.ScannerUserID,
		scan.ScanStatus, scan.ScanMethod, scan.ScannedAt, scan.IPAddress,
	)
	if err != nil {
		return fmt.Errorf("gate: insert scan: %w", err)
	}
	return nil
}

// InsertScanTx is the tx-aware InsertScan variant for atomic paths (VerifyPIN).
// Returns inserted=true on new row, false on duplicate (idempotent no-op).
func (r *PostgresGateRepository) InsertScanTx(ctx context.Context, tx pgx.Tx, scan *ScanRecord) (bool, error) {
	var returnedID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO ticket_scans (
			id, ticket_id, gate_id, scanner_device_id, scanner_user_id,
			scan_status, scan_method, scanned_at, ip_address
		) VALUES ($1,$2,$3,$4,$5,$6::scan_status_enum,$7,$8,$9)
		ON CONFLICT (id) DO NOTHING
		RETURNING id`,
		scan.ID, scan.TicketID, scan.GateID, scan.ScannerDeviceID, scan.ScannerUserID,
		scan.ScanStatus, scan.ScanMethod, scan.ScannedAt, scan.IPAddress,
	).Scan(&returnedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // duplicate scan_id (idempotent)
	}
	if err != nil {
		return false, fmt.Errorf("gate: insert scan (tx): %w", err)
	}
	return true, nil
}

// UpdateTicketStatus marks a ticket as USED within a caller tx.
func (r *PostgresGateRepository) UpdateTicketStatus(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID, status string) error {
	_, err := tx.Exec(ctx,
		`UPDATE tickets SET status = $1::ticket_status_enum, updated_at = NOW() WHERE id = $2`,
		status, ticketID)
	if err != nil {
		return fmt.Errorf("gate: update ticket status: %w", err)
	}
	return nil
}

// MarkFallbackPINUsed marks the ticket's fallback PIN as consumed (single-use).
func (r *PostgresGateRepository) MarkFallbackPINUsed(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (int64, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE tickets
		SET fallback_pin_used = TRUE, fallback_pin_used_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND fallback_pin_used = FALSE`, ticketID)
	if err != nil {
		return 0, fmt.Errorf("gate: mark pin used: %w", err)
	}
	return tag.RowsAffected(), nil
}

// GetTicketByIDForUpdate locks a ticket row within a caller tx.
func (r *PostgresGateRepository) GetTicketByIDForUpdate(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (*TicketLookup, error) {
	row := tx.QueryRow(ctx, ticketLookupSQL+` WHERE t.id = $1 FOR UPDATE`, ticketID)
	tl, err := scanTicketLookup(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTicketNotFound
		}
		return nil, fmt.Errorf("gate: get ticket for update: %w", err)
	}
	return tl, nil
}

// EarliestValidScan returns the earliest VALID scan for a ticket (or nil, nil
// if none). Deterministic tie-breaker: smallest scan_id (UUID ASC).
func (r *PostgresGateRepository) EarliestValidScan(ctx context.Context, ticketID uuid.UUID) (*ScanRecord, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, ticket_id, gate_id, scanner_device_id, scanner_user_id,
		       scan_status::text, scan_method, scanned_at, ip_address
		FROM ticket_scans
		WHERE ticket_id = $1 AND scan_status::text = 'VALID'
		ORDER BY scanned_at ASC, id ASC
		LIMIT 1`, ticketID)

	var rec ScanRecord
	err := row.Scan(
		&rec.ID, &rec.TicketID, &rec.GateID, &rec.ScannerDeviceID, &rec.ScannerUserID,
		&rec.ScanStatus, &rec.ScanMethod, &rec.ScannedAt, &rec.IPAddress,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("gate: earliest valid scan: %w", err)
	}
	return &rec, nil
}

// GetEarliestValidScanByTicketIDs returns earliest VALID scan per ticket.
// Tie-breaker: smallest scan_id (UUID ASC) for deterministic winner selection.
func (r *PostgresGateRepository) GetEarliestValidScanByTicketIDs(ctx context.Context, tx pgx.Tx, ticketIDs []uuid.UUID) (map[uuid.UUID]*ScanRecord, error) {
	out := make(map[uuid.UUID]*ScanRecord)
	if len(ticketIDs) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (ticket_id)
		       id, ticket_id, gate_id, scanner_device_id, scanner_user_id,
		       scan_status::text, scan_method, scanned_at, ip_address
		FROM ticket_scans
		WHERE ticket_id = ANY($1) AND scan_status::text = 'VALID'
		ORDER BY ticket_id, scanned_at ASC, id ASC`, ticketIDs)
	if err != nil {
		return nil, fmt.Errorf("gate: batch earliest: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rec ScanRecord
		if err := rows.Scan(
			&rec.ID, &rec.TicketID, &rec.GateID, &rec.ScannerDeviceID, &rec.ScannerUserID,
			&rec.ScanStatus, &rec.ScanMethod, &rec.ScannedAt, &rec.IPAddress,
		); err != nil {
			return nil, err
		}
		out[rec.TicketID] = &rec
	}
	return out, rows.Err()
}

// UpdateScanStatus overwrites scan_status for a scan row (loser marking).
func (r *PostgresGateRepository) UpdateScanStatus(ctx context.Context, tx pgx.Tx, scanID uuid.UUID, status string) error {
	_, err := tx.Exec(ctx,
		`UPDATE ticket_scans SET scan_status = $1::scan_status_enum WHERE id = $2`,
		status, scanID)
	if err != nil {
		return fmt.Errorf("gate: update scan status: %w", err)
	}
	return nil
}
