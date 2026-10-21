package payment

import (
	"bytes"
	"context"
	"encoding/json"
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
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/booking"
	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// ============================================================
// Extra payment HTTP handler tests — P9a-5
// internal/payment/handler.go and internal/payment/webhook.go had 0% coverage.
// ============================================================

const payTestUserHeader = "X-Test-User-ID"

// errListRepo fails ListByBooking to drive the handler's 500 branch.
type errListRepo struct {
	*memObligationRepo
	err error
}

func (e *errListRepo) ListByBooking(_ context.Context, _ uuid.UUID) ([]PaymentObligation, error) {
	return nil, e.err
}

// newPayHandler wires a PaymentHandler plus a gin router with a user-id
// injector middleware.
func newPayHandler(t *testing.T, repo PaymentObligationRepository, reader BookingReader, gw adapter.PaymentGateway) (*PaymentHandler, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	idem := NewIdempotencyService(newTestRedis(t), repo)
	svc := NewPaymentService(&fakeBeginner{tx: &fakeTx{}}, repo, reader, gw, idem)
	h := NewPaymentHandler(svc, repo, reader, idem, zap.NewNop())

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if uid := c.GetHeader(payTestUserHeader); uid != "" {
			c.Set("user_id", uid)
		}
	})
	r.POST("/bookings/:booking_id/payments", h.InitiatePayments)
	r.GET("/bookings/:booking_id/status", h.GetBookingStatus)
	r.POST("/payments/bulk/assign", h.BulkAssign)
	r.POST("/webhook/payment", h.HandleWebhook)
	return h, r
}

// payReq issues a request against the router.
func payReq(t *testing.T, r *gin.Engine, method, path, userID, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != "" {
		req.Header.Set(payTestUserHeader, userID)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ============================================================
// Handler: InitiatePayments
// ============================================================

func TestHandler_InitiatePayments_Success(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+bid.String()+"/payments",
		leader.String(), `{"payment_mode":"BULK","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-1"})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"payment_status":"PENDING"`)
}

func TestHandler_InitiatePayments_MissingAuthContext(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+uuid.NewString()+"/payments",
		"", `{"payment_mode":"BULK","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-2"})

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"UNAUTHORIZED"`)
}

func TestHandler_InitiatePayments_InvalidUserID(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+uuid.NewString()+"/payments",
		"not-a-uuid", `{"payment_mode":"BULK","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-3"})

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"UNAUTHORIZED"`)
}

func TestHandler_InitiatePayments_InvalidBookingID(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/not-a-uuid/payments",
		uuid.NewString(), `{"payment_mode":"BULK","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-4"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_InitiatePayments_MissingIdempotencyKey(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+uuid.NewString()+"/payments",
		uuid.NewString(), `{"payment_mode":"BULK","amount":"1000"}`, nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_InitiatePayments_InvalidBody(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+uuid.NewString()+"/payments",
		uuid.NewString(), `{not-json`,
		map[string]string{"X-Idempotency-Key": "idem-h-5"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_InitiatePayments_InvalidModeReturns400(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+uuid.NewString()+"/payments",
		uuid.NewString(), `{"payment_mode":"WIRE","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-6"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_InitiatePayments_InvalidMembersReturns400(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+uuid.NewString()+"/payments",
		uuid.NewString(), `{"payment_mode":"SPLIT","members":[]}`,
		map[string]string{"X-Idempotency-Key": "idem-h-7"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_InitiatePayments_NotLeaderReturns403(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+bid.String()+"/payments",
		uuid.NewString(), `{"payment_mode":"BULK","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-8"})

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"FORBIDDEN"`)
}

func TestHandler_InitiatePayments_BookingNotFoundReturns404(t *testing.T) {
	repo := &memObligationRepo{}
	reader := &fakeBookingReader{err: ErrBookingNotFound}
	_, r := newPayHandler(t, repo, reader, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+uuid.NewString()+"/payments",
		uuid.NewString(), `{"payment_mode":"BULK","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-9"})

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
}

func TestHandler_InitiatePayments_BookingNotPendingReturns409(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPaid}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/bookings/"+bid.String()+"/payments",
		leader.String(), `{"payment_mode":"BULK","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-10"})

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"CONFLICT"`)
}

func TestHandler_InitiatePayments_GatewayErrorReturns500(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	gw := &fakeGateway{initiateErr: errors.New("gateway down")}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, gw)

	w := payReq(t, r, http.MethodPost, "/bookings/"+bid.String()+"/payments",
		leader.String(), `{"payment_mode":"BULK","amount":"1000"}`,
		map[string]string{"X-Idempotency-Key": "idem-h-11"})

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
}

// ============================================================
// Handler: GetBookingStatus
// ============================================================

func TestHandler_GetBookingStatus_LeaderGets200(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/"+bid.String()+"/status", leader.String(), "", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"obligations":[]`)
}

func TestHandler_GetBookingStatus_MemberGets200(t *testing.T) {
	bid, leader, member := uuid.New(), uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	require.NoError(t, repo.Create(context.Background(), &PaymentObligation{
		ID: uuid.New(), BookingID: bid, UserID: member, Amount: "1000.00", Status: StatusPending,
	}))
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/"+bid.String()+"/status", member.String(), "", nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), member.String())
}

func TestHandler_GetBookingStatus_StrangerGets403(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/"+bid.String()+"/status", uuid.NewString(), "", nil)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"FORBIDDEN"`)
}

func TestHandler_GetBookingStatus_MissingAuthContext(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/"+uuid.NewString()+"/status", "", "", nil)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandler_GetBookingStatus_InvalidBookingID(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/not-a-uuid/status", uuid.NewString(), "", nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_GetBookingStatus_NotFoundReturns404(t *testing.T) {
	repo := &memObligationRepo{}
	reader := &fakeBookingReader{err: ErrBookingNotFound}
	_, r := newPayHandler(t, repo, reader, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/"+uuid.NewString()+"/status", uuid.NewString(), "", nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
}

func TestHandler_GetBookingStatus_LookupErrorReturns500(t *testing.T) {
	repo := &memObligationRepo{}
	reader := &fakeBookingReader{err: errors.New("db down")}
	_, r := newPayHandler(t, repo, reader, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/"+uuid.NewString()+"/status", uuid.NewString(), "", nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandler_GetBookingStatus_ListErrorReturns500(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	base := &memObligationRepo{}
	repo := &errListRepo{memObligationRepo: base, err: errors.New("list boom")}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	w := payReq(t, r, http.MethodGet, "/bookings/"+bid.String()+"/status", leader.String(), "", nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ============================================================
// buildStatusResponse
// ============================================================

func TestBuildStatusResponse_AllPaidMarksTicketsGenerated(t *testing.T) {
	bid := uuid.New()
	sp := 3
	obs := []PaymentObligation{
		{ID: uuid.New(), BookingID: bid, UserID: uuid.New(), Amount: "1000.00", Status: StatusPaid,
			SeatPosition: &sp, PaymentLink: strPtr("https://pay/1"), ExternalTransactionID: strPtr("tx-1")},
		{ID: uuid.New(), BookingID: bid, UserID: uuid.New(), Amount: "1000.00", Status: StatusPaid},
	}

	resp := buildStatusResponse(bid, obs)

	require.NotNil(t, resp)
	assert.Equal(t, StatusPaid, resp.PaymentStatus)
	assert.True(t, resp.TicketsGenerated)
	assert.Len(t, resp.Obligations, 2)
	require.NotNil(t, resp.Obligations[0].SeatPosition)
	assert.Equal(t, 3, *resp.Obligations[0].SeatPosition)
	assert.Equal(t, "https://pay/1", resp.Obligations[0].PaymentLink)
	assert.Equal(t, "tx-1", resp.Obligations[0].ExternalTransactionID)
	assert.Nil(t, resp.Obligations[1].SeatPosition)
}

func TestBuildStatusResponse_MixedStatusIsPending(t *testing.T) {
	bid := uuid.New()
	obs := []PaymentObligation{
		{ID: uuid.New(), BookingID: bid, UserID: uuid.New(), Amount: "1000.00", Status: StatusPaid},
		{ID: uuid.New(), BookingID: bid, UserID: uuid.New(), Amount: "1000.00", Status: StatusPending},
	}

	resp := buildStatusResponse(bid, obs)

	require.NotNil(t, resp)
	assert.Equal(t, StatusPending, resp.PaymentStatus)
	assert.False(t, resp.TicketsGenerated)
}

func TestBuildStatusResponse_EmptyObligations_ReportsPending(t *testing.T) {
	// FIXED in P9a-6 (#105): buildStatusResponse used to initialise
	// allPaid=true, so a booking with zero obligations (payment never
	// initiated) was reported as payment_status=PAID with
	// tickets_generated=true. That leaked PAID + ticket generation for
	// bookings nobody had paid for.
	//
	// Now an empty obligation list reports PENDING + tickets_generated=false.
	bid := uuid.New()

	resp := buildStatusResponse(bid, nil)

	require.NotNil(t, resp)
	assert.Empty(t, resp.Obligations)
	assert.NotNil(t, resp.Obligations, "obligations must serialise as [] not null")
	assert.Equal(t, StatusPending, resp.PaymentStatus, "empty obligations => PENDING")
	assert.False(t, resp.TicketsGenerated, "empty obligations => tickets_generated=false")
	assert.Equal(t, bid.String(), resp.BookingID)
}

// ============================================================
// Handler: BulkAssign
// ============================================================

func TestHandler_BulkAssign_Success(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	body := `{"booking_id":"` + bid.String() + `","assignments":[{"ticket_id":"` + uuid.NewString() +
		`","user_id":"` + uuid.NewString() + `"}]}`

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", leader.String(), body,
		map[string]string{"X-Idempotency-Key": "bulk-h-1"})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"assigned":1`)
	assert.Contains(t, w.Body.String(), `"status":"SUCCESS"`)
}

func TestHandler_BulkAssign_MissingAuthContext(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", "", `{}`,
		map[string]string{"X-Idempotency-Key": "bulk-h-2"})

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandler_BulkAssign_InvalidUserID(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", "nope", `{}`,
		map[string]string{"X-Idempotency-Key": "bulk-h-3"})

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandler_BulkAssign_MissingIdempotencyKey(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", uuid.NewString(), `{}`, nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_BulkAssign_InvalidBody(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", uuid.NewString(), `{not-json`,
		map[string]string{"X-Idempotency-Key": "bulk-h-4"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_BulkAssign_EmptyAssignmentsReturns400(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &fakeGateway{})

	body := `{"booking_id":"` + uuid.NewString() + `","assignments":[]}`
	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", uuid.NewString(), body,
		map[string]string{"X-Idempotency-Key": "bulk-h-5"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_BulkAssign_NotLeaderReturns403(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	body := `{"booking_id":"` + bid.String() + `","assignments":[{"ticket_id":"` + uuid.NewString() +
		`","user_id":"` + uuid.NewString() + `"}]}`

	w := payReq(t, r, http.MethodPost, "/payments/bulk/assign", uuid.NewString(), body,
		map[string]string{"X-Idempotency-Key": "bulk-h-6"})

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"FORBIDDEN"`)
}

func TestHandler_BulkAssign_IdempotentReplayReturnsCached(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &memObligationRepo{}
	snap := &BookingSnapshot{ID: bid, LeaderUserID: leader, PaymentStatus: StatusPending}
	_, r := newPayHandler(t, repo, &fakeBookingReader{snap: snap}, &fakeGateway{})

	body := `{"booking_id":"` + bid.String() + `","assignments":[{"ticket_id":"` + uuid.NewString() +
		`","user_id":"` + uuid.NewString() + `"}]}`
	idem := map[string]string{"X-Idempotency-Key": "bulk-h-replay"}

	first := payReq(t, r, http.MethodPost, "/payments/bulk/assign", leader.String(), body, idem)
	require.Equal(t, http.StatusOK, first.Code)

	second := payReq(t, r, http.MethodPost, "/payments/bulk/assign", leader.String(), body, idem)
	assert.Equal(t, http.StatusOK, second.Code)

	// The L1 cache stores the data payload only; the envelope's meta
	// (timestamp/request_id) is regenerated per response, so compare `data`.
	var firstEnv, secondEnv map[string]any
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstEnv))
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondEnv))
	assert.Equal(t, firstEnv["data"], secondEnv["data"],
		"a replayed bulk-assign key must return the identical cached payload")
}

// ============================================================
// Handler: HandleWebhook
// ============================================================

func TestHandler_HandleWebhook_Success(t *testing.T) {
	repo := &memObligationRepo{}
	seedObligation(t, repo, "tx-h", StatusPending)
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &sigGateway{})

	w := payReq(t, r, http.MethodPost, "/webhook/payment", "",
		`{"external_transaction_id":"tx-h","status":"PENDING"}`, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"processed"`)
}

func TestHandler_HandleWebhook_SuccessTransitionsObligation(t *testing.T) {
	repo := &memObligationRepo{}
	ob := seedObligation(t, repo, "tx-h-paid", StatusPending)
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &sigGateway{})

	w := payReq(t, r, http.MethodPost, "/webhook/payment", "",
		`{"external_transaction_id":"tx-h-paid","status":"SUCCESS"}`, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, StatusPaid, ob.Status)
}

func TestHandler_HandleWebhook_EmptyBodyReturns400(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &sigGateway{})

	w := payReq(t, r, http.MethodPost, "/webhook/payment", "", "", nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_HandleWebhook_InvalidSignatureReturns401(t *testing.T) {
	repo := &memObligationRepo{}
	gw := &sigGateway{sigErr: errors.New("bad sig")}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, gw)

	w := payReq(t, r, http.MethodPost, "/webhook/payment", "",
		`{"external_transaction_id":"tx-h","status":"SUCCESS"}`,
		map[string]string{"X-Gateway-Signature": "bogus"})

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"UNAUTHORIZED"`)
}

func TestHandler_HandleWebhook_UnknownTransactionReturns404(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &sigGateway{})

	w := payReq(t, r, http.MethodPost, "/webhook/payment", "",
		`{"external_transaction_id":"tx-unknown","status":"SUCCESS"}`, nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
}

func TestHandler_HandleWebhook_MalformedBodyReturns500(t *testing.T) {
	repo := &memObligationRepo{}
	_, r := newPayHandler(t, repo, &fakeBookingReader{}, &sigGateway{})

	w := payReq(t, r, http.MethodPost, "/webhook/payment", "", `{not-json`, nil)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
}

func TestPaymentMeta_ContainsTimestampAndRequestID(t *testing.T) {
	meta := paymentMeta()

	require.NotNil(t, meta)
	assert.NotEmpty(t, meta["timestamp"])
	assert.NotEmpty(t, meta["request_id"])
}

// ============================================================
// booking_reader adapter
// ============================================================

func TestBookingReaderAdapter_GetByID(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &stubBookingRepo{booking: &booking.Booking{ID: bid, LeaderUserID: leader, TotalAmount: "3000000.00",
		PaymentMode: ModeSplit, PaymentStatus: StatusPending}}
	adapter := NewBookingReaderFromRepo(repo)

	snap, err := adapter.GetByID(context.Background(), bid)

	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, bid, snap.ID)
	assert.Equal(t, leader, snap.LeaderUserID)
	assert.Equal(t, "3000000.00", snap.TotalAmount)
	assert.Equal(t, ModeSplit, snap.PaymentMode)
	assert.Equal(t, StatusPending, snap.PaymentStatus)
}

func TestBookingReaderAdapter_GetByID_TranslatesNotFound(t *testing.T) {
	repo := &stubBookingRepo{err: errBookingNotFound}
	adapter := NewBookingReaderFromRepo(repo)

	_, err := adapter.GetByID(context.Background(), uuid.New())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBookingNotFound)
}

func TestBookingReaderAdapter_GetByIDForUpdate(t *testing.T) {
	bid, leader := uuid.New(), uuid.New()
	repo := &stubBookingRepo{booking: &booking.Booking{ID: bid, LeaderUserID: leader}}
	adapter := NewBookingReaderFromRepo(repo)

	snap, err := adapter.GetByIDForUpdate(context.Background(), nil, bid)

	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, bid, snap.ID)
}

func TestBookingReaderAdapter_GetByIDForUpdate_TranslatesNotFound(t *testing.T) {
	repo := &stubBookingRepo{err: errBookingNotFound}
	adapter := NewBookingReaderFromRepo(repo)

	_, err := adapter.GetByIDForUpdate(context.Background(), nil, uuid.New())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBookingNotFound)
}

func TestBookingReaderAdapter_GetEventStartAt(t *testing.T) {
	want := time.Now().UTC()
	repo := &stubBookingRepo{startAt: want}
	adapter := NewBookingReaderFromRepo(repo)

	got, err := adapter.GetEventStartAt(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestBookingReaderAdapter_GetEventStartAt_TranslatesNotFound(t *testing.T) {
	repo := &stubBookingRepo{err: errBookingNotFound}
	adapter := NewBookingReaderFromRepo(repo)

	_, err := adapter.GetEventStartAt(context.Background(), uuid.New())

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBookingNotFound)
}

func TestTranslateBookingErr_PassesThroughUnrelatedErrors(t *testing.T) {
	other := errors.New("some other failure")

	err := translateBookingErr(other)

	assert.Equal(t, other, err)
}

// ============================================================
// Local helpers
// ============================================================

func strPtr(s string) *string { return &s }

// errBookingNotFound mirrors the sentinel the booking domain uses.
var errBookingNotFound = booking.ErrBookingNotFound

// stubBookingRepo implements booking.BookingRepository for the reader-adapter
// tests.
type stubBookingRepo struct {
	booking *booking.Booking
	err     error
	startAt time.Time
}

func (s *stubBookingRepo) GetSectionByID(context.Context, uuid.UUID) (*booking.SectionRow, error) {
	return nil, s.err
}

func (s *stubBookingRepo) GetTakenSeats(context.Context, uuid.UUID) ([]int, error) {
	return nil, s.err
}

func (s *stubBookingRepo) CreateBooking(context.Context, *booking.Booking) error {
	return s.err
}

func (s *stubBookingRepo) GetByID(context.Context, uuid.UUID) (*booking.Booking, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.booking, nil
}

func (s *stubBookingRepo) GetByIDForUpdate(context.Context, pgx.Tx, uuid.UUID) (*booking.Booking, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.booking, nil
}

func (s *stubBookingRepo) GetEventStartAt(context.Context, uuid.UUID) (time.Time, error) {
	if s.err != nil {
		return time.Time{}, s.err
	}
	return s.startAt, nil
}
