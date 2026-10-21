package gate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// P9a-6 helpers
// ============================================================

// p9a6FailingTxBeginner always fails to open a transaction.
type p9a6FailingTxBeginner struct{ err error }

func (f *p9a6FailingTxBeginner) Begin(context.Context) (pgx.Tx, error) {
	return nil, f.err
}

func p9a6GateCtx(t *testing.T, userID string, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/gate/scan", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if userID != "" {
		c.Set("user_id", userID)
	}
	return c, w
}

func p9a6ScanBody(t *testing.T, qr string) string {
	t.Helper()
	raw, err := json.Marshal(ScanQRRequest{
		TicketID: uTicket, QRData: qr, GateID: "A",
		ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.NoError(t, err)
	return string(raw)
}

func p9a6PinBody(t *testing.T, ticketID uuid.UUID, pin string) string {
	t.Helper()
	raw, err := json.Marshal(VerifyPINRequest{
		TicketID: ticketID, PIN: pin, GateID: "A",
		ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.NoError(t, err)
	return string(raw)
}

func p9a6ConfirmedTicket() *TicketLookup {
	return &TicketLookup{
		ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
		QRSecretKey: "secret", OwnerName: "Budi", SectionName: "VIP", SeatPos: 42,
	}
}

// ============================================================
// Handler: ScanQR
// ============================================================

func TestP9a6_Handler_ScanQR_MissingUserContext(t *testing.T) {
	svc, _ := buildService(&fakeRepo{})
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, "", p9a6ScanBody(t, "x"))
	h.ScanQR(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
}

func TestP9a6_Handler_ScanQR_InvalidUserUUID(t *testing.T) {
	svc, _ := buildService(&fakeRepo{})
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, "not-a-uuid", p9a6ScanBody(t, "x"))
	h.ScanQR(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestP9a6_Handler_ScanQR_InvalidBody(t *testing.T) {
	svc, _ := buildService(&fakeRepo{})
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), `{"not":"valid"}`)
	h.ScanQR(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_REQUEST")
}

func TestP9a6_Handler_ScanQR_InvalidQR_Returns401(t *testing.T) {
	repo := &fakeRepo{ticket: p9a6ConfirmedTicket()}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6ScanBody(t, "invalid-qr"))
	h.ScanQR(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_QR_OR_TICKET_MISMATCH")
}

func TestP9a6_Handler_ScanQR_AlreadyUsed_Returns409(t *testing.T) {
	repo := &fakeRepo{
		ticket:   p9a6ConfirmedTicket(),
		ticketFU: &TicketLookup{ID: uTicket, OwnerUserID: uOwner, Status: "USED", OwnerName: "Budi", SectionName: "VIP", SeatPos: 42},
	}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	qr := makeValidQR(t, uTicket, uOwner, "secret")
	c, w := p9a6GateCtx(t, uScanner.String(), p9a6ScanBody(t, qr))
	h.ScanQR(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "CONFLICT")
}

func TestP9a6_Handler_ScanQR_NotActive_Returns422(t *testing.T) {
	repo := &fakeRepo{
		ticket:   p9a6ConfirmedTicket(),
		ticketFU: &TicketLookup{ID: uTicket, OwnerUserID: uOwner, Status: "CANCELLED", OwnerName: "Budi", SectionName: "VIP", SeatPos: 42},
	}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	qr := makeValidQR(t, uTicket, uOwner, "secret")
	c, w := p9a6GateCtx(t, uScanner.String(), p9a6ScanBody(t, qr))
	h.ScanQR(c)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "UNPROCESSABLE_ENTITY")
}

func TestP9a6_Handler_ScanQR_TicketNotFound_Returns404(t *testing.T) {
	repo := &fakeRepo{ticketErr: ErrTicketNotFound}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6ScanBody(t, "x"))
	h.ScanQR(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "NOT_FOUND")
}

func TestP9a6_Handler_ScanQR_UnexpectedError_Returns500(t *testing.T) {
	repo := &fakeRepo{ticketErr: errors.New("db exploded")}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6ScanBody(t, "x"))
	h.ScanQR(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL_ERROR")
}

func TestP9a6_Handler_ScanQR_Success_Returns200(t *testing.T) {
	repo := &fakeRepo{ticket: p9a6ConfirmedTicket(), ticketFU: p9a6ConfirmedTicket()}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	qr := makeValidQR(t, uTicket, uOwner, "secret")
	c, w := p9a6GateCtx(t, uScanner.String(), p9a6ScanBody(t, qr))
	h.ScanQR(c)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"valid":true`)
	assert.Contains(t, w.Body.String(), "Budi")
}

// ============================================================
// Handler: VerifyPIN
// ============================================================

func TestP9a6_Handler_VerifyPIN_MissingUserContext(t *testing.T) {
	svc, _ := buildService(&fakeRepo{})
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, "", p9a6PinBody(t, uTicket, "PIN123"))
	h.VerifyPIN(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestP9a6_Handler_VerifyPIN_InvalidBody(t *testing.T) {
	svc, _ := buildService(&fakeRepo{})
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), `{"broken":`)
	h.VerifyPIN(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestP9a6_Handler_VerifyPIN_UnknownPIN_Returns401(t *testing.T) {
	repo := &fakeRepo{}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6PinBody(t, uTicket, "NOPE"))
	h.VerifyPIN(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_PIN")
}

func TestP9a6_Handler_VerifyPIN_TicketMismatch_Returns422(t *testing.T) {
	repo := &fakeRepo{pinTicket: &TicketLookup{
		ID: uuid.New(), OwnerUserID: uOwner, Status: "CONFIRMED",
		OwnerName: "Budi", SectionName: "VIP", SeatPos: 42, FallbackPIN: "PIN123",
	}}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6PinBody(t, uTicket, "PIN123"))
	h.VerifyPIN(c)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "ticket_id does not match pin owner")
}

func TestP9a6_Handler_VerifyPIN_AlreadyUsed_Returns409(t *testing.T) {
	repo := &fakeRepo{pinTicket: &TicketLookup{
		ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
		OwnerName: "Budi", SectionName: "VIP", SeatPos: 42,
		FallbackPIN: "PIN123", FallbackPINUsed: true,
	}}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6PinBody(t, uTicket, "PIN123"))
	h.VerifyPIN(c)

	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestP9a6_Handler_VerifyPIN_NotActive_Returns422(t *testing.T) {
	repo := &fakeRepo{pinTicket: &TicketLookup{
		ID: uTicket, OwnerUserID: uOwner, Status: "CANCELLED",
		OwnerName: "Budi", SectionName: "VIP", SeatPos: 42, FallbackPIN: "PIN123",
	}}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6PinBody(t, uTicket, "PIN123"))
	h.VerifyPIN(c)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestP9a6_Handler_VerifyPIN_UnexpectedError_Returns500(t *testing.T) {
	repo := &fakeRepo{pinErr: errors.New("db exploded")}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6PinBody(t, uTicket, "PIN123"))
	h.VerifyPIN(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestP9a6_Handler_VerifyPIN_Success_Returns200(t *testing.T) {
	repo := &fakeRepo{pinTicket: &TicketLookup{
		ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
		OwnerName: "Budi", SectionName: "VIP", SeatPos: 42, FallbackPIN: "PIN123",
	}, markPINRows: 1}
	svc, _ := buildService(repo)
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), p9a6PinBody(t, uTicket, "PIN123"))
	h.VerifyPIN(c)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"scan_method":"PIN"`)
}

// ============================================================
// Handler: SyncOfflineScans
// ============================================================

func TestP9a6_Handler_SyncOfflineScans_BatchTooLarge_Returns422(t *testing.T) {
	svc, _ := buildService(&fakeRepo{})
	h := NewGateHandler(svc)

	scans := make([]OfflineScan, 501)
	for i := range scans {
		scans[i] = OfflineScan{
			ScanID: uuid.New(), TicketID: uuid.New(), GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "INVALID",
		}
	}
	raw, err := json.Marshal(SyncRequest{DeviceID: uDevice, Scans: scans})
	require.NoError(t, err)

	c, w := p9a6GateCtx(t, uScanner.String(), string(raw))
	h.SyncOfflineScans(c)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "batch exceeds 500 scans")
}

func TestP9a6_Handler_SyncOfflineScans_EmptyBatch_Returns200(t *testing.T) {
	svc, _ := buildService(&fakeRepo{})
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), `{"device_id":"`+uDevice.String()+`","scans":[]}`)
	h.SyncOfflineScans(c)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"synced":0`)
}

func TestP9a6_Handler_SyncOfflineScans_InvalidBody_Returns400(t *testing.T) {
	svc, _ := buildService(&fakeRepo{})
	h := NewGateHandler(svc)

	c, w := p9a6GateCtx(t, uScanner.String(), `{"broken":`)
	h.SyncOfflineScans(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ============================================================
// Service: extra paths not covered by the existing suite
// ============================================================

func TestP9a6_Service_ScanQR_BeginTxError(t *testing.T) {
	repo := &fakeRepo{ticket: p9a6ConfirmedTicket()}
	svc := NewGateService(repo, &p9a6FailingTxBeginner{err: errors.New("cannot connect")})
	svc.nowFn = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

	qr := makeValidQR(t, uTicket, uOwner, "secret")
	_, err := svc.ScanQR(context.Background(), uScanner, &ScanQRRequest{
		TicketID: uTicket, QRData: qr, GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "begin tx")
}

func TestP9a6_Service_ScanQR_InsertScanError(t *testing.T) {
	repo := &fakeRepo{
		ticket: p9a6ConfirmedTicket(), ticketFU: p9a6ConfirmedTicket(),
		insertErr: errors.New("insert failed"),
	}
	svc, _ := buildService(repo)

	qr := makeValidQR(t, uTicket, uOwner, "secret")
	_, err := svc.ScanQR(context.Background(), uScanner, &ScanQRRequest{
		TicketID: uTicket, QRData: qr, GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert scan")
}

func TestP9a6_Service_ScanQR_TicketLockError(t *testing.T) {
	repo := &fakeRepo{ticket: p9a6ConfirmedTicket(), ticketFUErr: errors.New("lock failed")}
	svc, _ := buildService(repo)

	qr := makeValidQR(t, uTicket, uOwner, "secret")
	_, err := svc.ScanQR(context.Background(), uScanner, &ScanQRRequest{
		TicketID: uTicket, QRData: qr, GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lock failed")
}

func TestP9a6_Service_VerifyPIN_UpdateStatusError(t *testing.T) {
	repo := &fakeRepo{
		pinTicket: &TicketLookup{
			ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
			OwnerName: "Budi", SectionName: "VIP", SeatPos: 42, FallbackPIN: "PIN123",
		},
		updateStatusErr: errors.New("update failed"),
	}
	svc, _ := buildService(repo)

	_, err := svc.VerifyPIN(context.Background(), uScanner, &VerifyPINRequest{
		TicketID: uTicket, PIN: "PIN123", GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update ticket status")
}

func TestP9a6_Service_VerifyPIN_MarkPINUsedError(t *testing.T) {
	repo := &fakeRepo{
		pinTicket: &TicketLookup{
			ID: uTicket, OwnerUserID: uOwner, Status: "CONFIRMED",
			OwnerName: "Budi", SectionName: "VIP", SeatPos: 42, FallbackPIN: "PIN123",
		},
		markPINErr: errors.New("mark failed"),
	}
	svc, _ := buildService(repo)

	_, err := svc.VerifyPIN(context.Background(), uScanner, &VerifyPINRequest{
		TicketID: uTicket, PIN: "PIN123", GateID: "A", ScannerDeviceID: uDevice, ScanID: uScan,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mark pin used")
}

func TestP9a6_Service_SyncOfflineScans_EmptyBatch(t *testing.T) {
	repo := &fakeRepo{}
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice, Scans: []OfflineScan{},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, res.Synced)
	assert.Empty(t, res.Conflicts)
	assert.Empty(t, res.Errors)
}

func TestP9a6_Service_SyncOfflineScans_BeginTxError_CountsAsFailed(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewGateService(repo, &p9a6FailingTxBeginner{err: errors.New("cannot connect")})
	svc.nowFn = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "INVALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Failed)
	assert.Len(t, res.Errors, 1)
}

func TestP9a6_Service_SyncOfflineScans_InsertError_CountsAsFailed(t *testing.T) {
	repo := &fakeRepo{insertErr: errors.New("insert failed")}
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "INVALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Failed)
	assert.Equal(t, 0, res.Synced)
}

func TestP9a6_Service_SyncOfflineScans_ValidScanWins(t *testing.T) {
	repo := &fakeRepo{ticketFU: p9a6ConfirmedTicket()}
	svc, tx := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Synced)
	assert.Empty(t, res.Conflicts)
	assert.True(t, tx.committed)
}

func TestP9a6_Service_SyncOfflineScans_ValidScanLosesToEarlierScan(t *testing.T) {
	earlier := &ScanRecord{
		ID: uuid.New(), TicketID: uTicket, GateID: "A",
		ScannerDeviceID: &uDevice, ScanStatus: "VALID", ScanMethod: "QR",
		ScannedAt: time.Now().Add(-time.Hour),
	}
	repo := &fakeRepo{ticketFU: p9a6ConfirmedTicket(), prevWinner: earlier}
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Synced)
	require.Len(t, res.Conflicts, 1)
	assert.Equal(t, "ALREADY_USED", res.Conflicts[0].Status)
	assert.Equal(t, uTicket, res.Conflicts[0].TicketID)
}

func TestP9a6_Service_SyncOfflineScans_ValidScanTieBreakOnScanID(t *testing.T) {
	sameTime := time.Unix(1_700_000_000, 0).UTC()
	// A winner with a lexicographically smaller scan_id must win the tie.
	winner := &ScanRecord{
		ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), TicketID: uTicket,
		GateID: "A", ScannerDeviceID: &uDevice, ScanStatus: "VALID", ScanMethod: "QR",
		ScannedAt: sameTime,
	}
	repo := &fakeRepo{ticketFU: p9a6ConfirmedTicket(), prevWinner: winner}
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"), TicketID: uTicket,
			GateID: "A", ScannedAt: sameTime, ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})
	require.NoError(t, err)
	require.Len(t, res.Conflicts, 1)
	assert.Equal(t, "ALREADY_USED", res.Conflicts[0].Status)
}

func TestP9a6_Service_SyncOfflineScans_LockTicketError_CountsAsFailed(t *testing.T) {
	repo := &fakeRepo{ticketFUErr: errors.New("lock failed")}
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Failed)
	assert.Contains(t, res.Errors[0], "lock ticket")
}

func TestP9a6_Service_SyncOfflineScans_QueryWinnersError_CountsAsFailed(t *testing.T) {
	repo := &fakeRepo{ticketFU: p9a6ConfirmedTicket(), getEarliestErr: errors.New("query failed")}
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now(), ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Failed)
	assert.Contains(t, res.Errors[0], "query winners")
}

func TestP9a6_Service_SyncOfflineScans_DemoteWinnerError_CountsAsFailed(t *testing.T) {
	earlier := &ScanRecord{
		ID: uuid.New(), TicketID: uTicket, GateID: "A",
		ScannerDeviceID: &uDevice, ScanStatus: "VALID", ScanMethod: "QR",
		ScannedAt: time.Now().Add(-time.Hour),
	}
	repo := &fakeRepo{
		ticketFU: p9a6ConfirmedTicket(), prevWinner: earlier,
		updateScanErr: errors.New("demote failed"),
	}
	svc, _ := buildService(repo)

	res, err := svc.SyncOfflineScans(context.Background(), uScanner, &SyncRequest{
		DeviceID: uDevice,
		Scans: []OfflineScan{{
			ScanID: uuid.New(), TicketID: uTicket, GateID: "A",
			ScannedAt: time.Now().Add(-2 * time.Hour), ScanMethod: "QR", ScanStatus: "VALID",
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Failed)
	assert.Contains(t, res.Errors[0], "demote winner")
}

// ============================================================
// Sentinels + models
// ============================================================

func TestP9a6_GateSentinels_Distinct(t *testing.T) {
	sentinels := []error{
		ErrInvalidQR, ErrAlreadyUsed, ErrTicketNotActive,
		ErrTicketMismatch, ErrBatchTooLarge, ErrTicketNotFound,
	}
	for i := 0; i < len(sentinels); i++ {
		for j := i + 1; j < len(sentinels); j++ {
			assert.NotEqual(t, sentinels[i].Error(), sentinels[j].Error())
		}
	}
}

func TestP9a6_GateSentinel_Messages(t *testing.T) {
	assert.Contains(t, ErrInvalidQR.Error(), "invalid qr")
	assert.Contains(t, ErrAlreadyUsed.Error(), "already used")
	assert.Contains(t, ErrTicketNotActive.Error(), "not active")
	assert.Contains(t, ErrTicketMismatch.Error(), "ticket_id does not match")
	assert.Contains(t, ErrBatchTooLarge.Error(), "500 scans")
}

func TestP9a6_SyncMaxScans_Is500(t *testing.T) {
	assert.Equal(t, 500, syncMaxScans)
}

func TestP9a6_ScanResult_JSONShape(t *testing.T) {
	res := ScanResult{
		Valid: true, TicketID: uTicket, OwnerName: "Budi", Seat: "VIP - 42",
		Status: "USED", ScanMethod: "QR", ScannedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"valid":true`)
	assert.Contains(t, string(raw), `"owner_name":"Budi"`)
	assert.Contains(t, string(raw), `"seat":"VIP - 42"`)
}

func TestP9a6_SyncResponse_JSONShape(t *testing.T) {
	res := SyncResponse{
		Synced: 1, Duplicates: 2, Failed: 3,
		Conflicts: []ConflictEntry{}, Errors: []string{"e1"},
	}
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"synced":1`)
	assert.Contains(t, string(raw), `"duplicates":2`)
	assert.Contains(t, string(raw), `"failed":3`)
	assert.Contains(t, string(raw), `"conflicts":[]`)
}

func TestP9a6_OfflineScan_JSONShape(t *testing.T) {
	scan := OfflineScan{
		ScanID: uScan, TicketID: uTicket, GateID: "A",
		ScannedAt: time.Unix(1_700_000_000, 0).UTC(), ScanMethod: "QR", ScanStatus: "VALID",
	}
	raw, err := json.Marshal(scan)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"scan_id"`)
	assert.Contains(t, string(raw), `"scan_method":"QR"`)
	assert.Contains(t, string(raw), `"scan_status":"VALID"`)
}

func TestP9a6_ScanQRRequest_RequiredFields(t *testing.T) {
	req := ScanQRRequest{
		TicketID: uTicket, QRData: "qr", GateID: "A",
		ScannerDeviceID: uDevice, ScanID: uScan,
	}
	assert.Equal(t, uTicket, req.TicketID)
	assert.Equal(t, "qr", req.QRData)
	assert.Equal(t, "A", req.GateID)
}

func TestP9a6_VerifyPINRequest_RequiredFields(t *testing.T) {
	req := VerifyPINRequest{
		TicketID: uTicket, PIN: "PIN123", GateID: "A",
		ScannerDeviceID: uDevice, ScanID: uScan,
	}
	assert.Equal(t, "PIN123", req.PIN)
}

func TestP9a6_SyncRequest_Fields(t *testing.T) {
	req := SyncRequest{DeviceID: uDevice, Scans: []OfflineScan{}}
	assert.Equal(t, uDevice, req.DeviceID)
	assert.Len(t, req.Scans, 0)
}
