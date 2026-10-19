package gate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/ticket"
)

// ---------- fakes ----------

type fakeTx struct {
	pgx.Tx
	committed  bool
	rolledBack bool
}

func (f *fakeTx) Commit(_ context.Context) error { f.committed = true; return nil }
func (f *fakeTx) Rollback(_ context.Context) error {
	if f.committed {
		return nil
	}
	f.rolledBack = true
	return nil
}

type fakeTxBeginner struct{ tx *fakeTx }

func (f *fakeTxBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	if f.tx == nil {
		f.tx = &fakeTx{}
	}
	return f.tx, nil
}

type fakeRepo struct {
	ticket          *TicketLookup
	ticketErr       error
	ticketFU        *TicketLookup
	ticketFUErr     error
	pinTicket       *TicketLookup // returned by GetTicketByFallbackPINForUpdate
	pinErr          error
	insertResult    *bool // nil = true (default); non-nil = use value (duplicate when false)
	insertErr       error
	prevWinner      *ScanRecord
	getEarliestErr  error
	updateScanErr   error
	updateStatusErr error
	markPINRows     int64
	markPINErr      error
}

func (f *fakeRepo) GetTicketByID(_ context.Context, _ uuid.UUID) (*TicketLookup, error) {
	return f.ticket, f.ticketErr
}
func (f *fakeRepo) GetTicketByFallbackPIN(_ context.Context, _ string) (*TicketLookup, error) {
	return nil, ErrTicketNotFound
}
func (f *fakeRepo) GetTicketByFallbackPINForUpdate(_ context.Context, _ pgx.Tx, _ string) (*TicketLookup, error) {
	if f.pinErr != nil {
		return nil, f.pinErr
	}
	if f.pinTicket != nil {
		return f.pinTicket, nil
	}
	return nil, ErrTicketNotFound
}
func (f *fakeRepo) InsertScan(_ context.Context, _ *ScanRecord) error { return nil }
func (f *fakeRepo) InsertScanTx(_ context.Context, _ pgx.Tx, _ *ScanRecord) (bool, error) {
	if f.insertErr != nil {
		return false, f.insertErr
	}
	if f.insertResult != nil {
		return *f.insertResult, nil
	}
	return true, nil
}
func (f *fakeRepo) UpdateTicketStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return f.updateStatusErr
}
func (f *fakeRepo) MarkFallbackPINUsed(_ context.Context, _ pgx.Tx, _ uuid.UUID) (int64, error) {
	return f.markPINRows, f.markPINErr
}
func (f *fakeRepo) GetTicketByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*TicketLookup, error) {
	return f.ticketFU, f.ticketFUErr
}
func (f *fakeRepo) EarliestValidScan(_ context.Context, _ uuid.UUID) (*ScanRecord, error) {
	return nil, nil
}
func (f *fakeRepo) GetEarliestValidScanByTicketIDs(_ context.Context, _ pgx.Tx, _ []uuid.UUID) (map[uuid.UUID]*ScanRecord, error) {
	if f.getEarliestErr != nil {
		return nil, f.getEarliestErr
	}
	m := map[uuid.UUID]*ScanRecord{}
	if f.prevWinner != nil {
		m[f.prevWinner.TicketID] = f.prevWinner
	}
	return m, nil
}
func (f *fakeRepo) UpdateScanStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, _ string) error {
	return f.updateScanErr
}

// buildService creates a service with fake repo/tx and a fixed clock.
func buildService(repo *fakeRepo) (*GateService, *fakeTx) {
	tx := &fakeTx{}
	svc := NewGateService(repo, &fakeTxBeginner{tx: tx})
	svc.nowFn = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	return svc, tx
}

// valid QR generation helper — uses real ticket.GenerateQRData so HMAC passes.
func makeValidQR(t *testing.T, ticketID, ownerID uuid.UUID, secret string) string {
	t.Helper()
	qr, err := ticket.GenerateQRData(ticketID, ownerID, secret, time.Unix(1_700_000_000, 0).UTC())
	require.NoError(t, err)
	return qr
}

var (
	uScanner = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	uTicket  = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	uScan    = uuid.MustParse("55555555-5555-5555-5555-555555555555")
	uDevice  = uuid.MustParse("66666666-6666-6666-6666-666666666666")
	uOwner   = uuid.MustParse("77777777-7777-7777-7777-777777777777")
)

// ---------- ScanQR ----------

func TestScanQR_InvalidHMAC(t *testing.T) {
	repo := &fakeRepo{ticket: &TicketLookup{
		ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
		QRSecretKey: "secret", OwnerName: "Budi", SectionName: "VIP", SeatPos: 1,
	}}
	svc, _ := buildService(repo)

	_, err := svc.ScanQR(context.Background(), uScanner, &ScanQRRequest{
		TicketID: uTicket, QRData: "invalid-qr", GateID: "A",
		ScannerDeviceID: uDevice, ScanID: uScan,
	})
	assert.ErrorIs(t, err, ErrInvalidQR)
}

func TestScanQR_AlreadyUsed(t *testing.T) {
	repo := &fakeRepo{
		ticket: &TicketLookup{
			ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
			QRSecretKey: "secret", OwnerName: "Budi", SectionName: "VIP", SeatPos: 1,
		},
		ticketFU: &TicketLookup{ID: uTicket, OwnerUserID: uOwner, Status: "USED",
			OwnerName: "Budi", SectionName: "VIP", SeatPos: 1},
	}
	svc, tx := buildService(repo)
	qr := makeValidQR(t, uTicket, uOwner, "secret")

	_, err := svc.ScanQR(context.Background(), uScanner, &ScanQRRequest{
		TicketID: uTicket, QRData: qr, GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	assert.ErrorIs(t, err, ErrAlreadyUsed)
	assert.True(t, tx.committed)
}

func TestScanQR_Valid(t *testing.T) {
	repo := &fakeRepo{
		ticket: &TicketLookup{
			ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
			QRSecretKey: "secret", OwnerName: "Budi", SectionName: "VIP", SeatPos: 42,
		},
		ticketFU: &TicketLookup{ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
			OwnerName: "Budi", SectionName: "VIP", SeatPos: 42},
	}
	svc, tx := buildService(repo)
	qr := makeValidQR(t, uTicket, uOwner, "secret")

	result, err := svc.ScanQR(context.Background(), uScanner, &ScanQRRequest{
		TicketID: uTicket, QRData: qr, GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Equal(t, "Budi", result.OwnerName)
	assert.True(t, tx.committed)
}

func TestScanQR_NotFound(t *testing.T) {
	repo := &fakeRepo{ticketErr: ErrTicketNotFound}
	svc, _ := buildService(repo)

	_, err := svc.ScanQR(context.Background(), uScanner, &ScanQRRequest{
		TicketID: uTicket, QRData: "x", GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	assert.ErrorIs(t, err, ErrTicketNotFound)
}

// ---------- VerifyPIN ----------

func TestVerifyPIN_InvalidPIN(t *testing.T) {
	repo := &fakeRepo{}
	svc, _ := buildService(repo)

	// GetTicketByFallbackPINForUpdate returns ErrTicketNotFound for fakeRepo.
	_, err := svc.VerifyPIN(context.Background(), uScanner, &VerifyPINRequest{
		TicketID: uTicket, PIN: "wrong", GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	assert.ErrorIs(t, err, ErrTicketNotFound)
}

func TestVerifyPIN_AlreadyUsed(t *testing.T) {
	repo := &fakeRepo{markPINRows: 0} // simulate race won by another process
	svc, _ := buildService(repo)
	// Precondition: fakeRepo.GetTicketByFallbackPINForUpdate returns ErrTicketNotFound,
	// so this test only exercises the not-found path. Full PIN path requires
	// extending fakeRepo — intentionally skipped here (TD-F6-09 full coverage).
	_, err := svc.VerifyPIN(context.Background(), uScanner, &VerifyPINRequest{
		TicketID: uTicket, PIN: "A3F8K9L2M1N4", GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	assert.ErrorIs(t, err, ErrTicketNotFound)
	_ = errors.Is // placeholder to keep import
}

// ---------- VerifyPIN (expanded) ----------

func TestVerifyPIN_HappyPath(t *testing.T) {
	pinTicket := &TicketLookup{
		ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
		OwnerName: "Budi", SectionName: "VIP", SeatPos: 42,
		FallbackPIN: "PIN123", FallbackPINUsed: false,
	}
	repo := &fakeRepo{pinTicket: pinTicket, markPINRows: 1}
	svc, tx := buildService(repo)

	res, err := svc.VerifyPIN(context.Background(), uScanner, &VerifyPINRequest{
		TicketID: uTicket, PIN: "PIN123", GateID: "A",
		ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.NoError(t, err)
	assert.True(t, res.Valid)
	assert.Equal(t, "PIN", res.ScanMethod)
	assert.Equal(t, "Budi", res.OwnerName)
	assert.True(t, tx.committed)
}

func TestVerifyPIN_AlreadyUsedPIN(t *testing.T) {
	pinTicket := &TicketLookup{
		ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
		OwnerName: "Budi", SectionName: "VIP", SeatPos: 42,
		FallbackPIN: "PIN123", FallbackPINUsed: true,
	}
	repo := &fakeRepo{pinTicket: pinTicket}
	svc, tx := buildService(repo)

	_, err := svc.VerifyPIN(context.Background(), uScanner, &VerifyPINRequest{
		TicketID: uTicket, PIN: "PIN123", GateID: "A",
		ScannerDeviceID: uDevice, ScanID: uScan,
	})
	assert.ErrorIs(t, err, ErrAlreadyUsed)
	assert.True(t, tx.committed)
}

func TestVerifyPIN_TicketMismatch(t *testing.T) {
	otherTicket := uuid.New()
	pinTicket := &TicketLookup{
		ID: otherTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
		OwnerName: "Budi", SectionName: "VIP", SeatPos: 42,
		FallbackPIN: "PIN123", FallbackPINUsed: false,
	}
	repo := &fakeRepo{pinTicket: pinTicket}
	svc, _ := buildService(repo)

	_, err := svc.VerifyPIN(context.Background(), uScanner, &VerifyPINRequest{
		TicketID: uTicket, PIN: "PIN123", GateID: "A",
		ScannerDeviceID: uDevice, ScanID: uScan,
	})
	assert.ErrorIs(t, err, ErrTicketMismatch)
}

// ---------- SyncOfflineScans ----------

func TestSyncOfflineScans_BatchTooLarge(t *testing.T) {
	repo := &fakeRepo{}
	svc, _ := buildService(repo)

	scans := make([]OfflineScan, 501)
	for i := range scans {
		scans[i] = OfflineScan{
			ScanID: uuid.New(), TicketID: uuid.New(), GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "INVALID",
		}
	}
	_, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice, Scans: scans,
	})
	assert.ErrorIs(t, err, ErrBatchTooLarge)
}

func TestSyncOfflineScans_DuplicateScanID(t *testing.T) {
	dup := false
	repo := &fakeRepo{insertResult: &dup} // simulate duplicate
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "INVALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Duplicates)
	assert.Equal(t, 0, res.Synced)
	assert.Equal(t, 0, res.Failed)
}

func TestSyncOfflineScans_InvalidScanSyncedOK(t *testing.T) {
	// Non-VALID scan path: no ticket lock needed, straight insert.
	trueVal := true
	repo := &fakeRepo{insertResult: &trueVal}
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "INVALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Synced)
	assert.Equal(t, 0, res.Duplicates)
}
