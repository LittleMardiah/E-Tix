package gate

// FIX P9a-9b — unit coverage boost for the gate package.
//
// Targets the error branches of ScanQR / VerifyPIN / SyncOfflineScans and
// the HTTP error mapping of the gate handler.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/ticket"
)

// ============================================================
// Fakes
// ============================================================

// p9a9GateRepo is a fully configurable GateRepository fake.
type p9a9GateRepo struct {
	ticket          *TicketLookup
	ticketErr       error
	ticketFU        *TicketLookup
	ticketFUErr     error
	pinTicket       *TicketLookup
	pinErr          error
	insertScanErr   error
	insertScanTxErr error
	inserted        bool
	updateStatErr   error
	markPINRows     int64
	markPINErr      error
	earliestErr     error
	earliestMap     map[uuid.UUID]*ScanRecord
	updateScanErr   error
	scans           []*ScanRecord
	scanTx          []*ScanRecord
}

func (f *p9a9GateRepo) GetTicketByID(_ context.Context, _ uuid.UUID) (*TicketLookup, error) {
	return f.ticket, f.ticketErr
}

func (f *p9a9GateRepo) GetTicketByFallbackPINForUpdate(_ context.Context, _ pgx.Tx, _ string) (*TicketLookup, error) {
	if f.pinErr != nil {
		return nil, f.pinErr
	}
	return f.pinTicket, nil
}

func (f *p9a9GateRepo) InsertScan(_ context.Context, s *ScanRecord) error {
	if f.insertScanErr != nil {
		return f.insertScanErr
	}
	f.scans = append(f.scans, s)
	return nil
}

func (f *p9a9GateRepo) InsertScanTx(_ context.Context, _ pgx.Tx, s *ScanRecord) (bool, error) {
	if f.insertScanTxErr != nil {
		return false, f.insertScanTxErr
	}
	f.scanTx = append(f.scanTx, s)
	return f.inserted, nil
}

func (f *p9a9GateRepo) UpdateTicketStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return f.updateStatErr
}

func (f *p9a9GateRepo) MarkFallbackPINUsed(_ context.Context, _ pgx.Tx, _ uuid.UUID) (int64, error) {
	return f.markPINRows, f.markPINErr
}

func (f *p9a9GateRepo) GetTicketByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*TicketLookup, error) {
	return f.ticketFU, f.ticketFUErr
}

func (f *p9a9GateRepo) EarliestValidScan(_ context.Context, _ uuid.UUID) (*ScanRecord, error) {
	return nil, f.earliestErr
}

func (f *p9a9GateRepo) GetEarliestValidScanByTicketIDs(_ context.Context, _ pgx.Tx, _ []uuid.UUID) (map[uuid.UUID]*ScanRecord, error) {
	if f.earliestErr != nil {
		return nil, f.earliestErr
	}
	return f.earliestMap, nil
}

func (f *p9a9GateRepo) UpdateScanStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return f.updateScanErr
}

// p9a9GateTx is a pgx.Tx fake with an injectable commit error.
type p9a9GateTx struct {
	pgx.Tx
	commitErr error
	committed bool
}

func (t *p9a9GateTx) Commit(_ context.Context) error {
	if t.commitErr != nil {
		return t.commitErr
	}
	t.committed = true
	return nil
}

func (t *p9a9GateTx) Rollback(_ context.Context) error { return nil }

// p9a9GateTxBeginner opens a p9a9GateTx and can fail on Begin.
type p9a9GateTxBeginner struct {
	beginErr error
	tx       *p9a9GateTx
}

func (b *p9a9GateTxBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	if b.beginErr != nil {
		return nil, b.beginErr
	}
	if b.tx == nil {
		b.tx = &p9a9GateTx{}
	}
	return b.tx, nil
}

var p9a9Now = time.Unix(1_700_000_000, 0).UTC()

func p9a9Service(repo GateRepository, db TxBeginner) *GateService {
	svc := NewGateService(repo, db)
	svc.nowFn = func() time.Time { return p9a9Now }
	return svc
}

// p9a9ValidQR builds QR data that matches the given ticket + owner + secret.
func p9a9ValidQR(t *testing.T, ticketID, ownerID uuid.UUID, secret string) string {
	t.Helper()
	data, err := ticket.GenerateQRData(ticketID, ownerID, secret, p9a9Now)
	require.NoError(t, err)
	return data
}

// ============================================================
// ScanQR
// ============================================================

func TestP9a9ScanQR_TicketLookupError(t *testing.T) {
	repo := &p9a9GateRepo{ticketErr: errors.New("db down")}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: uuid.New(), QRData: "x", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

// An invalid QR must still be recorded as an INVALID attempt.
func TestP9a9ScanQR_InvalidQRRecordsAttempt(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{ticket: &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "s"}}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: "garbage", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.ErrorIs(t, err, ErrInvalidQR)
	require.Len(t, repo.scans, 1)
	assert.Equal(t, "INVALID", repo.scans[0].ScanStatus)
	assert.Equal(t, "QR", repo.scans[0].ScanMethod)
}

// TD-F6-12: a failed INVALID-scan insert must not swallow the verdict.
func TestP9a9ScanQR_InvalidScanInsertFailureStillReturnsInvalidQR(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:        &TicketLookup{ID: ticketID, OwnerUserID: uuid.New(), QRSecretKey: "s"},
		insertScanErr: errors.New("insert failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: "garbage", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.ErrorIs(t, err, ErrInvalidQR)
}

func TestP9a9ScanQR_BeginTxError(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:   &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU: &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "CONFIRMED"},
	}
	beginner := &p9a9GateTxBeginner{beginErr: errors.New("cannot begin")}
	svc := p9a9Service(repo, beginner)
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestP9a9ScanQR_LockTicketError(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:      &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFUErr: errors.New("lock failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "lock failed")
}

// A CONFIRMED ticket scans VALID and marks the ticket USED.
func TestP9a9ScanQR_ConfirmedTicketIsValid(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket: &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU: &TicketLookup{
			ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret",
			Status: "CONFIRMED", OwnerName: "Rina", SectionName: "A", SeatPos: 7,
		},
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	res, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Valid)
	assert.Equal(t, "USED", res.Status)
	assert.Equal(t, "Rina", res.OwnerName)
	assert.Equal(t, "A - 7", res.Seat)
	require.Len(t, repo.scanTx, 1)
	assert.Equal(t, "VALID", repo.scanTx[0].ScanStatus)
}

func TestP9a9ScanQR_ConfirmedTicketInsertScanError(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:          &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU:        &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "CONFIRMED"},
		insertScanTxErr: errors.New("insert failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert scan")
}

func TestP9a9ScanQR_ConfirmedTicketCommitError(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:   &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU: &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "CONFIRMED"},
	}
	beginner := &p9a9GateTxBeginner{tx: &p9a9GateTx{commitErr: errors.New("commit failed")}}
	svc := p9a9Service(repo, beginner)
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")
}

// An already-scanned ticket must be rejected with ALREADY_USED.
func TestP9a9ScanQR_UsedTicketIsAlreadyUsed(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:   &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU: &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "USED"},
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.ErrorIs(t, err, ErrAlreadyUsed)
	require.Len(t, repo.scanTx, 1)
	assert.Equal(t, "ALREADY_USED", repo.scanTx[0].ScanStatus)
}

func TestP9a9ScanQR_UsedTicketInsertScanError(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:          &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU:        &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "USED"},
		insertScanTxErr: errors.New("insert failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert scan")
}

// A ticket in any other status is not active.
func TestP9a9ScanQR_NonActiveTicketIsRejected(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:   &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU: &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "CANCELLED"},
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.ErrorIs(t, err, ErrTicketNotActive)
	require.Len(t, repo.scanTx, 1)
	assert.Equal(t, "INVALID", repo.scanTx[0].ScanStatus)
}

func TestP9a9ScanQR_NonActiveTicketInsertScanError(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:          &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU:        &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "CANCELLED"},
		insertScanTxErr: errors.New("insert failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")

	_, err := svc.ScanQR(context.Background(), uuid.New(), &ScanQRRequest{
		TicketID: ticketID, QRData: qr, GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert scan")
}

// ============================================================
// VerifyPIN
// ============================================================

func TestP9a9VerifyPIN_BeginTxError(t *testing.T) {
	svc := p9a9Service(&p9a9GateRepo{}, &p9a9GateTxBeginner{beginErr: errors.New("cannot begin")})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: uuid.New(), PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestP9a9VerifyPIN_LookupError(t *testing.T) {
	repo := &p9a9GateRepo{pinErr: errors.New("lookup failed")}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: uuid.New(), PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

// TD-F6-03: the ticket_id in the request must match the PIN owner.
func TestP9a9VerifyPIN_TicketMismatch(t *testing.T) {
	repo := &p9a9GateRepo{pinTicket: &TicketLookup{ID: uuid.New(), Status: "CONFIRMED"}}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: uuid.New(), PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.ErrorIs(t, err, ErrTicketMismatch)
}

func TestP9a9VerifyPIN_AlreadyUsed(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{pinTicket: &TicketLookup{
		ID: ticketID, Status: "USED", FallbackPINUsed: true,
	}}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.ErrorIs(t, err, ErrAlreadyUsed)
	require.Len(t, repo.scanTx, 1)
	assert.Equal(t, "ALREADY_USED", repo.scanTx[0].ScanStatus)
}

func TestP9a9VerifyPIN_AlreadyUsedInsertError(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{
		pinTicket:       &TicketLookup{ID: ticketID, Status: "USED", FallbackPINUsed: true},
		insertScanTxErr: errors.New("insert failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert scan")
}

func TestP9a9VerifyPIN_NotActive(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{pinTicket: &TicketLookup{ID: ticketID, Status: "CANCELLED"}}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.ErrorIs(t, err, ErrTicketNotActive)
	require.Len(t, repo.scanTx, 1)
	assert.Equal(t, "INVALID", repo.scanTx[0].ScanStatus)
}

func TestP9a9VerifyPIN_NotActiveInsertError(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{
		pinTicket:       &TicketLookup{ID: ticketID, Status: "CANCELLED"},
		insertScanTxErr: errors.New("insert failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert scan")
}

func TestP9a9VerifyPIN_UpdateStatusError(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{
		pinTicket:     &TicketLookup{ID: ticketID, Status: "CONFIRMED"},
		updateStatErr: errors.New("update failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "update ticket status")
}

func TestP9a9VerifyPIN_MarkPINUsedError(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{
		pinTicket:  &TicketLookup{ID: ticketID, Status: "CONFIRMED"},
		markPINErr: errors.New("mark failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mark pin used")
}

func TestP9a9VerifyPIN_InsertScanError(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{
		pinTicket:       &TicketLookup{ID: ticketID, Status: "CONFIRMED"},
		insertScanTxErr: errors.New("insert failed"),
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert scan")
}

func TestP9a9VerifyPIN_CommitError(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{pinTicket: &TicketLookup{ID: ticketID, Status: "CONFIRMED"}}
	beginner := &p9a9GateTxBeginner{tx: &p9a9GateTx{commitErr: errors.New("commit failed")}}
	svc := p9a9Service(repo, beginner)

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")
}

// A confirmed ticket + valid PIN produces a VALID scan result.
func TestP9a9VerifyPIN_ConfirmedTicketIsValid(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{pinTicket: &TicketLookup{
		ID: ticketID, Status: "CONFIRMED", OwnerName: "Budi", SectionName: "B", SeatPos: 3,
	}}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	res, err := svc.VerifyPIN(context.Background(), uuid.New(), &VerifyPINRequest{
		TicketID: ticketID, PIN: "1234", GateID: "G1",
		ScannerDeviceID: uuid.New(), ScanID: uuid.New(),
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Valid)
	assert.Equal(t, "PIN", res.ScanMethod)
	assert.Equal(t, "B - 3", res.Seat)
}

// ============================================================
// SyncOfflineScans
// ============================================================

func TestP9a9SyncOfflineScans_BatchTooLarge(t *testing.T) {
	scans := make([]OfflineScan, syncMaxScans+1)
	svc := p9a9Service(&p9a9GateRepo{}, &p9a9GateTxBeginner{})

	_, err := svc.SyncOfflineScans(context.Background(), uuid.New(), &SyncRequest{
		DeviceID: uuid.New(), Scans: scans,
	})

	require.ErrorIs(t, err, ErrBatchTooLarge)
}

func TestP9a9SyncOfflineScans_EmptyBatch(t *testing.T) {
	svc := p9a9Service(&p9a9GateRepo{}, &p9a9GateTxBeginner{})

	res, err := svc.SyncOfflineScans(context.Background(), uuid.New(), &SyncRequest{
		DeviceID: uuid.New(), Scans: []OfflineScan{},
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Empty(t, res.Conflicts)
	assert.Empty(t, res.Errors)
}

func TestP9a9SyncOfflineScans_BeginTxErrorIsReportedPerScan(t *testing.T) {
	svc := p9a9Service(&p9a9GateRepo{}, &p9a9GateTxBeginner{beginErr: errors.New("cannot begin")})

	res, err := svc.SyncOfflineScans(context.Background(), uuid.New(), &SyncRequest{
		DeviceID: uuid.New(),
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uuid.New(), GateID: "G1",
			ScannedAt: p9a9Now, ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, res.Failed)
	require.Len(t, res.Errors, 1)
	assert.Contains(t, res.Errors[0], "begin")
}

func TestP9a9SyncOfflineScans_LockTicketErrorIsReported(t *testing.T) {
	repo := &p9a9GateRepo{ticketFUErr: errors.New("lock failed")}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	res, err := svc.SyncOfflineScans(context.Background(), uuid.New(), &SyncRequest{
		DeviceID: uuid.New(),
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uuid.New(), GateID: "G1",
			ScannedAt: p9a9Now, ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, res.Failed)
	require.Len(t, res.Errors, 1)
	assert.Contains(t, res.Errors[0], "lock ticket")
}

func TestP9a9SyncOfflineScans_WinnerQueryErrorIsReported(t *testing.T) {
	repo := &p9a9GateRepo{earliestErr: errors.New("query failed")}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})

	res, err := svc.SyncOfflineScans(context.Background(), uuid.New(), &SyncRequest{
		DeviceID: uuid.New(),
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uuid.New(), GateID: "G1",
			ScannedAt: p9a9Now, ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, res.Failed)
	require.Len(t, res.Errors, 1)
	assert.Contains(t, res.Errors[0], "query winners")
}

// ============================================================
// Handler error mapping
// ============================================================

// p9a9GateHandlerCall drives one handler request and returns the recorder.
func p9a9GateHandlerCall(t *testing.T, h *GateHandler, method, path string, body []byte, userID string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("user_id", userID)
	h.ScanQR(c)
	return w
}

func TestP9a9GateHandler_ScanQR_InvalidUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewGateHandler(nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/gate/scan-qr", bytes.NewReader([]byte("{}")))
	c.Set("user_id", "not-a-uuid")
	h.ScanQR(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestP9a9GateHandler_ScanQR_InvalidBody(t *testing.T) {
	h := NewGateHandler(nil)

	w := p9a9GateHandlerCall(t, h, http.MethodPost, "/gate/scan-qr", []byte("{bad"), uuid.New().String())

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestP9a9GateHandler_ScanQR_InvalidQR(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{ticket: &TicketLookup{
		ID: ticketID, OwnerUserID: uuid.New(), QRSecretKey: "s",
	}}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	h := NewGateHandler(svc)
	body := []byte(`{"ticket_id":"` + ticketID.String() + `","qr_data":"bad","gate_id":"G1","scanner_device_id":"` + uuid.New().String() + `","scan_id":"` + uuid.New().String() + `"}`)

	w := p9a9GateHandlerCall(t, h, http.MethodPost, "/gate/scan-qr", body, uuid.New().String())

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_QR_OR_TICKET_MISMATCH")
}

func TestP9a9GateHandler_ScanQR_AlreadyUsed(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:   &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU: &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "USED"},
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	h := NewGateHandler(svc)
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")
	body := []byte(`{"ticket_id":"` + ticketID.String() + `","qr_data":"` + qr + `","gate_id":"G1","scanner_device_id":"` + uuid.New().String() + `","scan_id":"` + uuid.New().String() + `"}`)

	w := p9a9GateHandlerCall(t, h, http.MethodPost, "/gate/scan-qr", body, uuid.New().String())

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "CONFLICT")
}

func TestP9a9GateHandler_ScanQR_TicketNotActive(t *testing.T) {
	ticketID := uuid.New()
	ownerID := uuid.New()
	repo := &p9a9GateRepo{
		ticket:   &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret"},
		ticketFU: &TicketLookup{ID: ticketID, OwnerUserID: ownerID, QRSecretKey: "secret", Status: "CANCELLED"},
	}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	h := NewGateHandler(svc)
	qr := p9a9ValidQR(t, ticketID, ownerID, "secret")
	body := []byte(`{"ticket_id":"` + ticketID.String() + `","qr_data":"` + qr + `","gate_id":"G1","scanner_device_id":"` + uuid.New().String() + `","scan_id":"` + uuid.New().String() + `"}`)

	w := p9a9GateHandlerCall(t, h, http.MethodPost, "/gate/scan-qr", body, uuid.New().String())

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "UNPROCESSABLE_ENTITY")
}

func TestP9a9GateHandler_ScanQR_InternalError(t *testing.T) {
	ticketID := uuid.New()
	repo := &p9a9GateRepo{ticketErr: errors.New("db down")}
	svc := p9a9Service(repo, &p9a9GateTxBeginner{})
	h := NewGateHandler(svc)
	body := []byte(`{"ticket_id":"` + ticketID.String() + `","qr_data":"x","gate_id":"G1","scanner_device_id":"` + uuid.New().String() + `","scan_id":"` + uuid.New().String() + `"}`)

	w := p9a9GateHandlerCall(t, h, http.MethodPost, "/gate/scan-qr", body, uuid.New().String())

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
}
