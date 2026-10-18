package gate

import (
	"time"

	"github.com/google/uuid"
)

// ScanQRRequest is the body of POST /gate/scan-qr.
type ScanQRRequest struct {
	TicketID        uuid.UUID `json:"ticket_id" binding:"required"`
	QRData          string    `json:"qr_data" binding:"required"`
	GateID          string    `json:"gate_id" binding:"required"`
	ScannerDeviceID uuid.UUID `json:"scanner_device_id" binding:"required"`
	ScanID          uuid.UUID `json:"scan_id" binding:"required"`
}

// VerifyPINRequest is the body of POST /gate/verify-pin.
type VerifyPINRequest struct {
	TicketID        uuid.UUID `json:"ticket_id" binding:"required"`
	PIN             string    `json:"pin" binding:"required"`
	GateID          string    `json:"gate_id" binding:"required"`
	ScannerDeviceID uuid.UUID `json:"scanner_device_id" binding:"required"`
	ScanID          uuid.UUID `json:"scan_id" binding:"required"`
}

// OfflineScan is one entry in POST /gate/sync.
type OfflineScan struct {
	ScanID     uuid.UUID `json:"scan_id" binding:"required"`
	TicketID   uuid.UUID `json:"ticket_id" binding:"required"`
	GateID     string    `json:"gate_id" binding:"required"`
	ScannedAt  time.Time `json:"scanned_at" binding:"required"`
	ScanMethod string    `json:"scan_method" binding:"required"`
	ScanStatus string    `json:"scan_status" binding:"required"`
}

// SyncRequest is the body of POST /gate/sync.
type SyncRequest struct {
	DeviceID uuid.UUID     `json:"device_id" binding:"required"`
	Scans    []OfflineScan `json:"scans" binding:"required"`
}

// ScanResult is the response of scan-qr / verify-pin.
type ScanResult struct {
	Valid      bool      `json:"valid"`
	TicketID   uuid.UUID `json:"ticket_id"`
	OwnerName  string    `json:"owner_name,omitempty"`
	Seat       string    `json:"seat,omitempty"`
	Status     string    `json:"status,omitempty"`
	ScanMethod string    `json:"scan_method,omitempty"`
	ScannedAt  time.Time `json:"scanned_at,omitempty"`
}

// ConflictEntry is one row of the conflicts[] array in sync response.
type ConflictEntry struct {
	ScanID          uuid.UUID `json:"scan_id"`
	TicketID        uuid.UUID `json:"ticket_id"`
	Status          string    `json:"status"`
	WinnerDeviceID  uuid.UUID `json:"winner_device_id"`
	WinnerScannedAt time.Time `json:"winner_scanned_at"`
	LoserDeviceID   uuid.UUID `json:"loser_device_id"`
	LoserScannedAt  time.Time `json:"loser_scanned_at"`
}

// SyncResponse is the response of POST /gate/sync.
type SyncResponse struct {
	Synced     int             `json:"synced"`
	Duplicates int             `json:"duplicates"`
	Failed     int             `json:"failed"`
	Conflicts  []ConflictEntry `json:"conflicts"`
	Errors     []string        `json:"errors"`
}
