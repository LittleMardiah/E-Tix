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

// ErrBatchTooLarge is returned when sync batch exceeds 500 scans.
var ErrBatchTooLarge = errors.New("gate: batch exceeds 500 scans")

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
// B5 FIX: VALID path is transactional with FOR UPDATE — prevents concurrent
// scans from both being recorded VALID (double-count race).
func (s *GateService) ScanQR(ctx context.Context, scannerUserID uuid.UUID, req *ScanQRRequest) (*ScanResult, error) {
	now := s.nowFn()

	// Phase 1: read for HMAC verify (no lock — cheap pre-check).
	t, err := s.repo.GetTicketByID(ctx, req.TicketID)
	if err != nil {
		return nil, err
	}

	decoded, err := ticket.VerifyDynamicQR(req.QRData, t.OwnerUserID, t.QRSecretKey, now)
	if err != nil || decoded != req.TicketID {
		// INVALID — record attempt, no state change, no lock needed.
		_ = s.repo.InsertScan(ctx, &ScanRecord{
			ScannerUserID:   &scannerUserID,
			ID:              req.ScanID,
			TicketID:        req.TicketID,
			GateID:          req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID,
			ScanStatus:      "INVALID",
			ScanMethod:      "QR",
			ScannedAt:       now,
		})
		return nil, ErrInvalidQR
	}

	// Phase 2: VALID path — transactional + row-lock to serialize concurrent scans.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("gate: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	t2, err := s.repo.GetTicketByIDForUpdate(ctx, tx, req.TicketID)
	if err != nil {
		return nil, err
	}

	switch t2.Status {
	case "USED":
		if _, err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
			ScannerUserID:   &scannerUserID,
			ID:              req.ScanID,
			TicketID:        req.TicketID,
			GateID:          req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID,
			ScanStatus:      "ALREADY_USED",
			ScanMethod:      "QR",
			ScannedAt:       now,
		}); err != nil {
			return nil, fmt.Errorf("gate: insert scan: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("gate: commit: %w", err)
		}
		return nil, ErrAlreadyUsed
	case "CONFIRMED":
		if _, err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
			ScannerUserID:   &scannerUserID,
			ID:              req.ScanID,
			TicketID:        req.TicketID,
			GateID:          req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID,
			ScanStatus:      "VALID",
			ScanMethod:      "QR",
			ScannedAt:       now,
		}); err != nil {
			return nil, fmt.Errorf("gate: insert scan: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("gate: commit: %w", err)
		}
		return &ScanResult{
			Valid: true, TicketID: req.TicketID, OwnerName: t2.OwnerName,
			Seat:   fmt.Sprintf("%s - %d", t2.SectionName, t2.SeatPos),
			Status: "USED", ScannedAt: now,
		}, nil
	default:
		if _, err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
			ScannerUserID:   &scannerUserID,
			ID:              req.ScanID,
			TicketID:        req.TicketID,
			GateID:          req.GateID,
			ScannerDeviceID: &req.ScannerDeviceID,
			ScanStatus:      "INVALID",
			ScanMethod:      "QR",
			ScannedAt:       now,
		}); err != nil {
			return nil, fmt.Errorf("gate: insert scan: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("gate: commit: %w", err)
		}
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
		if _, err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
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
		if _, err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
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

	if _, err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
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

const syncMaxScans = 500

// SyncOfflineScans processes a batch of offline scans with deterministic
// conflict resolution. Earliest scanned_at wins; tie-breaker smallest scan_id.
func (s *GateService) SyncOfflineScans(ctx context.Context, scannerUserID uuid.UUID, req *SyncRequest) (*SyncResponse, error) {
	if len(req.Scans) > syncMaxScans {
		return nil, ErrBatchTooLarge
	}
	if len(req.Scans) == 0 {
		return &SyncResponse{Conflicts: []ConflictEntry{}, Errors: []string{}}, nil
	}

	resp := &SyncResponse{Conflicts: []ConflictEntry{}, Errors: []string{}}

	for _, sc := range req.Scans {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			resp.Failed++
			resp.Errors = append(resp.Errors, fmt.Sprintf("scan %s: begin: %v", sc.ScanID, err))
			continue
		}

		// Conflict resolution for VALID scans: decide status BEFORE insert.
		var insertStatus string
		var prevWinner *ScanRecord
		var targetTicket *TicketLookup
		if sc.ScanStatus == "VALID" {
			t, err := s.repo.GetTicketByIDForUpdate(ctx, tx, sc.TicketID)
			if err != nil {
				_ = tx.Rollback(ctx)
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("scan %s: lock ticket: %v", sc.ScanID, err))
				continue
			}
			targetTicket = t

			existing, err := s.repo.GetEarliestValidScanByTicketIDs(ctx, tx, []uuid.UUID{sc.TicketID})
			if err != nil {
				_ = tx.Rollback(ctx)
				resp.Failed++
				resp.Errors = append(resp.Errors, fmt.Sprintf("scan %s: query winners: %v", sc.ScanID, err))
				continue
			}
			prevWinner = existing[sc.TicketID]

			// Deterministic winner: earliest scanned_at; tie-break smallest scan_id.
			weWin := prevWinner == nil ||
				sc.ScannedAt.Before(prevWinner.ScannedAt) ||
				(sc.ScannedAt.Equal(prevWinner.ScannedAt) && sc.ScanID.String() < prevWinner.ID.String())

			if weWin {
				insertStatus = "VALID"
			} else {
				insertStatus = "ALREADY_USED"
			}
		} else {
			insertStatus = sc.ScanStatus
		}

		// Insert with resolved status; duplicate detection via inserted bool.
		inserted, err := s.repo.InsertScanTx(ctx, tx, &ScanRecord{
			ScannerUserID:   &scannerUserID,
			ID:              sc.ScanID,
			TicketID:        sc.TicketID,
			GateID:          sc.GateID,
			ScannerDeviceID: &req.DeviceID,
			ScanStatus:      insertStatus,
			ScanMethod:      sc.ScanMethod,
			ScannedAt:       sc.ScannedAt,
		})
		if err != nil {
			_ = tx.Rollback(ctx)
			resp.Failed++
			resp.Errors = append(resp.Errors, fmt.Sprintf("scan %s: insert: %v", sc.ScanID, err))
			continue
		}
		if !inserted {
			_ = tx.Rollback(ctx)
			resp.Duplicates++
			continue
		}

		// Post-insert actions for VALID winner/loser.
		if sc.ScanStatus == "VALID" {
			if insertStatus == "VALID" {
				// We win: demote previous winner (if any).
				if prevWinner != nil {
					if err := s.repo.UpdateScanStatus(ctx, tx, prevWinner.ID, "ALREADY_USED"); err != nil {
						_ = tx.Rollback(ctx)
						resp.Failed++
						resp.Errors = append(resp.Errors, fmt.Sprintf("scan %s: demote winner: %v", sc.ScanID, err))
						continue
					}
				}
				if targetTicket.Status == "CONFIRMED" {
					if err := s.repo.UpdateTicketStatus(ctx, tx, sc.TicketID, "USED"); err != nil {
						_ = tx.Rollback(ctx)
						resp.Failed++
						resp.Errors = append(resp.Errors, fmt.Sprintf("scan %s: update ticket: %v", sc.ScanID, err))
						continue
					}
				}
			} else {
				// We lose: record conflict.
				var winnerDevice uuid.UUID
				if prevWinner.ScannerDeviceID != nil {
					winnerDevice = *prevWinner.ScannerDeviceID
				}
				resp.Conflicts = append(resp.Conflicts, ConflictEntry{
					ScanID:          sc.ScanID,
					TicketID:        sc.TicketID,
					Status:          "ALREADY_USED",
					WinnerDeviceID:  winnerDevice,
					WinnerScannedAt: prevWinner.ScannedAt,
					LoserDeviceID:   req.DeviceID,
					LoserScannedAt:  sc.ScannedAt,
				})
			}
		}

		if err := tx.Commit(ctx); err != nil {
			resp.Failed++
			resp.Errors = append(resp.Errors, fmt.Sprintf("scan %s: commit: %v", sc.ScanID, err))
			continue
		}
		resp.Synced++
	}

	return resp, nil
}
