package gate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/LittleMardiah/E-Tix/backend/internal/ticket"
)

// ErrInvalidQR is returned when the HMAC signature fails or the decoded
// ticket_id does not match the requested ticket_id.
var ErrInvalidQR = errors.New("gate: invalid qr")

// ErrAlreadyUsed is returned when the ticket has already been scanned.
var ErrAlreadyUsed = errors.New("gate: ticket already used")

// ErrTicketNotActive is returned when the ticket is not in CONFIRMED status.
var ErrTicketNotActive = errors.New("gate: ticket not active")

// GateService orchestrates gate scan + verify logic.
type GateService struct {
	repo  GateRepository
	nowFn func() time.Time
}

// NewGateService constructs a GateService.
func NewGateService(repo GateRepository) *GateService {
	return &GateService{repo: repo, nowFn: time.Now}
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
