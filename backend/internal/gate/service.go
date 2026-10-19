package gate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LittleMardiah/E-Tix/backend/internal/ticket"
)

// ErrInvalidQR is returned when the HMAC signature fails or the decoded
// ticket_id does not match the requested ticket_id.
var ErrInvalidQR = errors.New("gate: invalid qr")

// ErrAlreadyUsed is returned when the ticket has already been scanned.
var ErrAlreadyUsed = errors.New("gate: ticket already used")

// ErrTicketNotActive is returned when the ticket is not in CONFIRMED status.
var ErrTicketNotActive = errors.New("gate: ticket not active")

// ErrTicketMismatch is returned when req.TicketID != PIN owner (TD-F6-03).
var ErrTicketMismatch = errors.New("gate: ticket_id does not match pin owner")

// TxBeginner abstracts the transaction opener for testability.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// GateService orchestrates gate scan + verify logic.
type GateService struct {
	repo  GateRepository
	db    TxBeginner
	nowFn func() time.Time
}

// NewGateService constructs a GateService.
func NewGateService(repo GateRepository, db TxBeginner) *GateService {
	return &GateService{repo: repo, db: db, nowFn: time.Now}
}

// ScanQR validates a dynamic HMAC QR and records the scan.
func (s *GateService) ScanQR(ctx context.Context, scannerUserID uuid.UUID, req *ScanQRRequest) (*ScanResult, error) {
	now := s.nowFn()
	t, err := s.repo.GetTicketByID(ctx, req.TicketID)
	if err != nil {
		return nil, err
	}

	decoded, err := ticket.VerifyDynamicQR(req.QRData, t.OwnerUserID, t.QRSecretKey, now)
	if err != nil || decoded != req.TicketID {
		_ = s.repo.InsertScan(ctx, &ScanRecord{
			ScannerUserID: &scannerUserID,
			ID:            req.ScanID, TicketID: req.TicketID, GateID: req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID, ScanStatus: "INVALID",
			ScanMethod: "QR", ScannedAt: now,
		})
		return nil, ErrInvalidQR
	}

	switch t.Status {
	case "USED":
		_ = s.repo.InsertScan(ctx, &ScanRecord{
			ScannerUserID: &scannerUserID,
			ID:            req.ScanID, TicketID: req.TicketID, GateID: req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID, ScanStatus: "ALREADY_USED",
			ScanMethod: "QR", ScannedAt: now,
		})
		return nil, ErrAlreadyUsed
	case "CONFIRMED":
		if err := s.repo.InsertScan(ctx, &ScanRecord{
			ScannerUserID: &scannerUserID,
			ID:            req.ScanID, TicketID: req.TicketID, GateID: req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID, ScanStatus: "VALID",
			ScanMethod: "QR", ScannedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("gate: insert scan: %w", err)
		}
		return &ScanResult{
			Valid: true, TicketID: req.TicketID, OwnerName: t.OwnerName,
			Seat:   fmt.Sprintf("%s - %d", t.SectionName, t.SeatPos),
			Status: "USED", ScannedAt: now,
		}, nil
	default:
		_ = s.repo.InsertScan(ctx, &ScanRecord{
			ScannerUserID: &scannerUserID,
			ID:            req.ScanID, TicketID: req.TicketID, GateID: req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID, ScanStatus: "INVALID",
			ScanMethod: "QR", ScannedAt: now,
		})
		return nil, ErrTicketNotActive
	}
}

// VerifyPIN validates the fallback PIN under row-level lock and records the
// scan atomically. TD-F6-01: FOR UPDATE + tx ensures single-use guarantee.
func (s *GateService) VerifyPIN(ctx context.Context, scannerUserID uuid.UUID, req *VerifyPINRequest) (*ScanResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("gate: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := s.nowFn()

	t, err := s.repo.GetTicketByFallbackPINForUpdate(ctx, tx, req.PIN)
	if err != nil {
		return nil, err
	}

	// TD-F6-03: validate ticket_id matches PIN owner.
	if req.TicketID != t.ID {
		return nil, ErrTicketMismatch
	}

	// TD-F6-04: distinguish already-used (409) from not-active (422).
	if t.FallbackPINUsed || t.Status == "USED" {
		if err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
			ScannerUserID: &scannerUserID,
			ID:            req.ScanID, TicketID: t.ID, GateID: req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID, ScanStatus: "ALREADY_USED",
			ScanMethod: "PIN", ScannedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("gate: insert scan: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("gate: commit: %w", err)
		}
		return nil, ErrAlreadyUsed
	}

	if t.Status != "CONFIRMED" {
		if err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
			ScannerUserID: &scannerUserID,
			ID:            req.ScanID, TicketID: t.ID, GateID: req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID, ScanStatus: "INVALID",
			ScanMethod: "PIN", ScannedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("gate: insert scan: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("gate: commit: %w", err)
		}
		return nil, ErrTicketNotActive
	}

	if err := s.repo.UpdateTicketStatus(ctx, tx, t.ID, "USED"); err != nil {
		return nil, fmt.Errorf("gate: update ticket status: %w", err)
	}
	if _, err := s.repo.MarkFallbackPINUsed(ctx, tx, t.ID); err != nil {
		return nil, fmt.Errorf("gate: mark pin used: %w", err)
	}

	if err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
		ScannerUserID: &scannerUserID,
		ID:            req.ScanID, TicketID: t.ID, GateID: req.GateID,
		ScannerDeviceID: &req.ScannerDeviceID, ScanStatus: "VALID",
		ScanMethod: "PIN", ScannedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("gate: insert scan: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("gate: commit: %w", err)
	}

	return &ScanResult{
		Valid: true, TicketID: t.ID, OwnerName: t.OwnerName,
		Seat:   fmt.Sprintf("%s - %d", t.SectionName, t.SeatPos),
		Status: "USED", ScanMethod: "PIN", ScannedAt: now,
	}, nil
}
