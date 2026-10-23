package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
)

func init() { gin.SetMode(gin.TestMode) }

// fakeSvc implements admin.AdminService (11 methods) with programmable results.
type fakeSvc struct {
	bulkResp    *admin.BulkCancelResponse
	bulkErr     error
	ledgerResp  *admin.LedgerResponse
	ledgerErr   error
	reverseResp *admin.LedgerReverseResponse
	reverseErr  error
	stuckResp   *admin.StuckLocksResponse
	stuckErr    error
	forceResp   *admin.ForceReleaseResponse
	forceErr    error
	userResp    *admin.UserListResponse
	userErr     error
	blResp      *admin.BlacklistListResponse
	blErr       error
	addBlResp   *admin.AddBlacklistResponse
	addBlErr    error
	rmBlResp    *admin.RemoveBlacklistResponse
	rmBlErr     error
	susResp     *admin.SuspendUserResponse
	susErr      error
	unsResp     *admin.UnsuspendUserResponse
	unsErr      error

	ledgerCalls    int
	ledgerDateFrom string
	ledgerDateTo   string
	ledgerStatus   string
	ledgerPage     int
	ledgerLimit    int

	userCalls  int
	userRole   string
	userStatus string
	userSearch string
	userPage   int
	userLimit  int

	blCalls int
	blPage  int
	blLimit int
}

func (f *fakeSvc) BulkCancel(_ context.Context, _, _ uuid.UUID, _ *admin.BulkCancelRequest) (*admin.BulkCancelResponse, error) {
	return f.bulkResp, f.bulkErr
}

func (f *fakeSvc) GetLedger(_ context.Context, _ uuid.UUID, dateFrom, dateTo, status string, page, limit int) (*admin.LedgerResponse, error) {
	f.ledgerCalls++
	f.ledgerDateFrom = dateFrom
	f.ledgerDateTo = dateTo
	f.ledgerStatus = status
	f.ledgerPage = page
	f.ledgerLimit = limit
	return f.ledgerResp, f.ledgerErr
}

func (f *fakeSvc) ManualReverse(_ context.Context, _ uuid.UUID, _ *admin.LedgerReverseRequest) (*admin.LedgerReverseResponse, error) {
	return f.reverseResp, f.reverseErr
}

func (f *fakeSvc) GetStuckLocks(_ context.Context, _ uuid.UUID) (*admin.StuckLocksResponse, error) {
	return f.stuckResp, f.stuckErr
}

func (f *fakeSvc) ForceRelease(_ context.Context, _ uuid.UUID, _ *admin.ForceReleaseRequest) (*admin.ForceReleaseResponse, error) {
	return f.forceResp, f.forceErr
}

func (f *fakeSvc) ListUsers(_ context.Context, _ uuid.UUID, role, status, search string, page, limit int) (*admin.UserListResponse, error) {
	f.userCalls++
	f.userRole = role
	f.userStatus = status
	f.userSearch = search
	f.userPage = page
	f.userLimit = limit
	return f.userResp, f.userErr
}

func (f *fakeSvc) ListBlacklist(_ context.Context, _ uuid.UUID, page, limit int) (*admin.BlacklistListResponse, error) {
	f.blCalls++
	f.blPage = page
	f.blLimit = limit
	return f.blResp, f.blErr
}

func (f *fakeSvc) AddBlacklist(_ context.Context, _ uuid.UUID, _ *admin.AddBlacklistRequest) (*admin.AddBlacklistResponse, error) {
	return f.addBlResp, f.addBlErr
}

func (f *fakeSvc) RemoveBlacklist(_ context.Context, _, _ uuid.UUID) (*admin.RemoveBlacklistResponse, error) {
	return f.rmBlResp, f.rmBlErr
}

func (f *fakeSvc) SuspendUser(_ context.Context, _, _ uuid.UUID, _ *admin.SuspendUserRequest) (*admin.SuspendUserResponse, error) {
	return f.susResp, f.susErr
}

func (f *fakeSvc) UnsuspendUser(_ context.Context, _, _ uuid.UUID) (*admin.UnsuspendUserResponse, error) {
	return f.unsResp, f.unsErr
}

// runHandler invokes a gin handler with a synthetic request.
// adminID == uuid.Nil means "no admin in context" (missing user_id).
func runHandler(h gin.HandlerFunc, method, path string, body []byte, adminID uuid.UUID, params map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if adminID != uuid.Nil {
		c.Set("user_id", adminID.String())
	}
	for k, v := range params {
		c.Params = append(c.Params, gin.Param{Key: k, Value: v})
	}
	h(c)
	return w
}

func parseBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

func assertEnvelopeSuccess(t *testing.T, w *httptest.ResponseRecorder, wantCode int) {
	t.Helper()
	require.Equal(t, wantCode, w.Code)
	body := parseBody(t, w)
	assert.Equal(t, true, body["success"])
	assert.Nil(t, body["error"])
	assert.NotNil(t, body["meta"])
}

func assertEnvelopeError(t *testing.T, w *httptest.ResponseRecorder, wantCode int) {
	t.Helper()
	require.Equal(t, wantCode, w.Code)
	body := parseBody(t, w)
	assert.Equal(t, false, body["success"])
	assert.Nil(t, body["data"])
	errObj, ok := body["error"].(map[string]interface{})
	require.True(t, ok, "error field must be an object")
	assert.NotEmpty(t, errObj["code"])
	assert.NotEmpty(t, errObj["message"])
}

// ===== E3 BulkCancel =====

func TestH_BulkCancel_202_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{bulkResp: &admin.BulkCancelResponse{
		EventID:          uuid.New(),
		CancelledTickets: 5,
		RefundTotal:      "5000000",
		Status:           "PROCESSING",
	}})
	body, err := json.Marshal(admin.BulkCancelRequest{Reason: "test"})
	require.NoError(t, err)

	w := runHandler(h.BulkCancel, http.MethodPost, "/admin/events/"+uuid.NewString()+"/bulk-cancel",
		body, uuid.New(), map[string]string{"event_id": uuid.NewString()})
	assertEnvelopeSuccess(t, w, 202)
}

func TestH_BulkCancel_400_InvalidBody(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.BulkCancel, http.MethodPost, "/admin/events/"+uuid.NewString()+"/bulk-cancel",
		[]byte(`{}`), uuid.New(), map[string]string{"event_id": uuid.NewString()})
	assertEnvelopeError(t, w, 400)
}

func TestH_BulkCancel_400_InvalidEventID(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})
	body, err := json.Marshal(admin.BulkCancelRequest{Reason: "test"})
	require.NoError(t, err)

	w := runHandler(h.BulkCancel, http.MethodPost, "/admin/events/bad-id/bulk-cancel",
		body, uuid.New(), map[string]string{"event_id": "bad-id"})
	assertEnvelopeError(t, w, 400)
}

func TestH_BulkCancel_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})
	body, err := json.Marshal(admin.BulkCancelRequest{Reason: "test"})
	require.NoError(t, err)

	w := runHandler(h.BulkCancel, http.MethodPost, "/admin/events/"+uuid.NewString()+"/bulk-cancel",
		body, uuid.Nil, map[string]string{"event_id": uuid.NewString()})
	assertEnvelopeError(t, w, 401)
}

func TestH_BulkCancel_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{bulkErr: assert.AnError})
	body, err := json.Marshal(admin.BulkCancelRequest{Reason: "test"})
	require.NoError(t, err)

	w := runHandler(h.BulkCancel, http.MethodPost, "/admin/events/"+uuid.NewString()+"/bulk-cancel",
		body, uuid.New(), map[string]string{"event_id": uuid.NewString()})
	assertEnvelopeError(t, w, 500)
}

// ===== E4 GetLedger =====

func TestH_GetLedger_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{ledgerResp: &admin.LedgerResponse{
		Entries: []admin.LedgerEntry{},
		Summary: admin.LedgerSummary{TotalPaid: "1000"},
	}})

	w := runHandler(h.GetLedger, http.MethodGet, "/admin/ledger", nil, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_GetLedger_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{ledgerErr: assert.AnError})

	w := runHandler(h.GetLedger, http.MethodGet, "/admin/ledger", nil, uuid.New(), nil)
	assertEnvelopeError(t, w, 500)
}

func TestH_GetLedger_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.GetLedger, http.MethodGet, "/admin/ledger", nil, uuid.Nil, nil)
	assertEnvelopeError(t, w, 401)
}

func TestH_GetLedger_ParsesQuery(t *testing.T) {
	f := &fakeSvc{ledgerResp: &admin.LedgerResponse{Entries: []admin.LedgerEntry{}}}
	h := admin.NewAdminHandler(f)

	w := runHandler(h.GetLedger, http.MethodGet,
		"/admin/ledger?date_from=2026-01-01&date_to=2026-01-31&status=PAID&page=2&limit=25",
		nil, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
	assert.Equal(t, 1, f.ledgerCalls)
	assert.Equal(t, "2026-01-01", f.ledgerDateFrom)
	assert.Equal(t, "2026-01-31", f.ledgerDateTo)
	assert.Equal(t, "PAID", f.ledgerStatus)
	assert.Equal(t, 2, f.ledgerPage)
	assert.Equal(t, 25, f.ledgerLimit)
}

func TestH_GetLedger_DefaultPagination(t *testing.T) {
	f := &fakeSvc{ledgerResp: &admin.LedgerResponse{Entries: []admin.LedgerEntry{}}}
	h := admin.NewAdminHandler(f)

	w := runHandler(h.GetLedger, http.MethodGet, "/admin/ledger", nil, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
	assert.Equal(t, 1, f.ledgerPage)
	assert.Equal(t, 50, f.ledgerLimit)
}

func TestH_GetLedger_400_InvalidPagination(t *testing.T) {
	cases := []string{
		"/admin/ledger?page=0",
		"/admin/ledger?page=abc",
		"/admin/ledger?limit=0",
		"/admin/ledger?limit=101",
		"/admin/ledger?limit=abc",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			f := &fakeSvc{ledgerResp: &admin.LedgerResponse{Entries: []admin.LedgerEntry{}}}
			h := admin.NewAdminHandler(f)

			w := runHandler(h.GetLedger, http.MethodGet, path, nil, uuid.New(), nil)
			assertEnvelopeError(t, w, 400)
			assert.Zero(t, f.ledgerCalls)
		})
	}
}

// ===== E5 ManualReverse =====

func TestH_ManualReverse_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{reverseResp: &admin.LedgerReverseResponse{
		PaymentObligationID:        uuid.New(),
		Status:                     "REFUNDED",
		GatewayRefundTransactionID: "refund-xyz",
	}})
	body, err := json.Marshal(admin.LedgerReverseRequest{TransactionID: uuid.New(), Reason: "dispute"})
	require.NoError(t, err)

	w := runHandler(h.ManualReverse, http.MethodPost, "/admin/ledger/reverse", body, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_ManualReverse_400_InvalidBody(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.ManualReverse, http.MethodPost, "/admin/ledger/reverse", []byte(`{}`), uuid.New(), nil)
	assertEnvelopeError(t, w, 400)
}

func TestH_ManualReverse_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})
	body, err := json.Marshal(admin.LedgerReverseRequest{TransactionID: uuid.New(), Reason: "dispute"})
	require.NoError(t, err)

	w := runHandler(h.ManualReverse, http.MethodPost, "/admin/ledger/reverse", body, uuid.Nil, nil)
	assertEnvelopeError(t, w, 401)
}

func TestH_ManualReverse_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{reverseErr: assert.AnError})
	body, err := json.Marshal(admin.LedgerReverseRequest{TransactionID: uuid.New(), Reason: "dispute"})
	require.NoError(t, err)

	w := runHandler(h.ManualReverse, http.MethodPost, "/admin/ledger/reverse", body, uuid.New(), nil)
	assertEnvelopeError(t, w, 500)
}

// ===== E6 GetStuckLocks =====

func TestH_GetStuckLocks_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{stuckResp: &admin.StuckLocksResponse{StuckLocks: []admin.StuckLock{}}})

	w := runHandler(h.GetStuckLocks, http.MethodGet, "/admin/stuck-locks", nil, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_GetStuckLocks_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{stuckErr: assert.AnError})

	w := runHandler(h.GetStuckLocks, http.MethodGet, "/admin/stuck-locks", nil, uuid.New(), nil)
	assertEnvelopeError(t, w, 500)
}

func TestH_GetStuckLocks_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.GetStuckLocks, http.MethodGet, "/admin/stuck-locks", nil, uuid.Nil, nil)
	assertEnvelopeError(t, w, 401)
}

// ===== E6 ForceRelease =====

func TestH_ForceRelease_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{forceResp: &admin.ForceReleaseResponse{
		BookingID:     uuid.New(),
		ReleasedSeats: []int{1, 2},
		NewStatus:     "CANCELLED",
	}})
	body, err := json.Marshal(admin.ForceReleaseRequest{BookingID: uuid.New(), Reason: "stuck"})
	require.NoError(t, err)

	w := runHandler(h.ForceRelease, http.MethodPost, "/admin/force-release", body, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_ForceRelease_400_InvalidBody(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.ForceRelease, http.MethodPost, "/admin/force-release", []byte(`{}`), uuid.New(), nil)
	assertEnvelopeError(t, w, 400)
}

func TestH_ForceRelease_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})
	body, err := json.Marshal(admin.ForceReleaseRequest{BookingID: uuid.New(), Reason: "stuck"})
	require.NoError(t, err)

	w := runHandler(h.ForceRelease, http.MethodPost, "/admin/force-release", body, uuid.Nil, nil)
	assertEnvelopeError(t, w, 401)
}

func TestH_ForceRelease_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{forceErr: assert.AnError})
	body, err := json.Marshal(admin.ForceReleaseRequest{BookingID: uuid.New(), Reason: "stuck"})
	require.NoError(t, err)

	w := runHandler(h.ForceRelease, http.MethodPost, "/admin/force-release", body, uuid.New(), nil)
	assertEnvelopeError(t, w, 500)
}

// ===== E7 ListUsers =====

func TestH_ListUsers_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{userResp: &admin.UserListResponse{Users: []admin.AdminUser{}}})

	w := runHandler(h.ListUsers, http.MethodGet, "/admin/users", nil, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_ListUsers_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{userErr: assert.AnError})

	w := runHandler(h.ListUsers, http.MethodGet, "/admin/users", nil, uuid.New(), nil)
	assertEnvelopeError(t, w, 500)
}

func TestH_ListUsers_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.ListUsers, http.MethodGet, "/admin/users", nil, uuid.Nil, nil)
	assertEnvelopeError(t, w, 401)
}

func TestH_ListUsers_ParsesQuery(t *testing.T) {
	f := &fakeSvc{userResp: &admin.UserListResponse{Users: []admin.AdminUser{}}}
	h := admin.NewAdminHandler(f)

	w := runHandler(h.ListUsers, http.MethodGet,
		"/admin/users?role=customer&status=ACTIVE&search=alice&page=3&limit=100",
		nil, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
	assert.Equal(t, 1, f.userCalls)
	assert.Equal(t, "customer", f.userRole)
	assert.Equal(t, "ACTIVE", f.userStatus)
	assert.Equal(t, "alice", f.userSearch)
	assert.Equal(t, 3, f.userPage)
	assert.Equal(t, 100, f.userLimit)
}

func TestH_ListUsers_400_InvalidPagination(t *testing.T) {
	f := &fakeSvc{userResp: &admin.UserListResponse{Users: []admin.AdminUser{}}}
	h := admin.NewAdminHandler(f)

	w := runHandler(h.ListUsers, http.MethodGet, "/admin/users?limit=101", nil, uuid.New(), nil)
	assertEnvelopeError(t, w, 400)
	assert.Zero(t, f.userCalls)
}

// ===== E8 ListBlacklist =====

func TestH_ListBlacklist_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{blResp: &admin.BlacklistListResponse{Entries: []admin.BlacklistEntry{}}})

	w := runHandler(h.ListBlacklist, http.MethodGet, "/admin/blacklist", nil, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_ListBlacklist_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{blErr: assert.AnError})

	w := runHandler(h.ListBlacklist, http.MethodGet, "/admin/blacklist", nil, uuid.New(), nil)
	assertEnvelopeError(t, w, 500)
}

func TestH_ListBlacklist_ParsesPagination(t *testing.T) {
	f := &fakeSvc{blResp: &admin.BlacklistListResponse{Entries: []admin.BlacklistEntry{}}}
	h := admin.NewAdminHandler(f)

	w := runHandler(h.ListBlacklist, http.MethodGet, "/admin/blacklist?page=4&limit=10", nil, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 200)
	assert.Equal(t, 1, f.blCalls)
	assert.Equal(t, 4, f.blPage)
	assert.Equal(t, 10, f.blLimit)
}

func TestH_ListBlacklist_400_InvalidPagination(t *testing.T) {
	f := &fakeSvc{blResp: &admin.BlacklistListResponse{Entries: []admin.BlacklistEntry{}}}
	h := admin.NewAdminHandler(f)

	w := runHandler(h.ListBlacklist, http.MethodGet, "/admin/blacklist?page=-1", nil, uuid.New(), nil)
	assertEnvelopeError(t, w, 400)
	assert.Zero(t, f.blCalls)
}

func TestH_ListBlacklist_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.ListBlacklist, http.MethodGet, "/admin/blacklist", nil, uuid.Nil, nil)
	assertEnvelopeError(t, w, 401)
}

// ===== E8 AddBlacklist =====

func TestH_AddBlacklist_201_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{addBlResp: &admin.AddBlacklistResponse{
		ID:    uuid.New(),
		Type:  "ip",
		Value: "1.2.3.4",
	}})
	body, err := json.Marshal(admin.AddBlacklistRequest{Type: "ip", Value: "1.2.3.4", Reason: "bot"})
	require.NoError(t, err)

	w := runHandler(h.AddBlacklist, http.MethodPost, "/admin/blacklist", body, uuid.New(), nil)
	assertEnvelopeSuccess(t, w, 201)
}

func TestH_AddBlacklist_400_InvalidBody(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.AddBlacklist, http.MethodPost, "/admin/blacklist", []byte(`{}`), uuid.New(), nil)
	assertEnvelopeError(t, w, 400)
}

func TestH_AddBlacklist_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})
	body, err := json.Marshal(admin.AddBlacklistRequest{Type: "ip", Value: "1.2.3.4", Reason: "bot"})
	require.NoError(t, err)

	w := runHandler(h.AddBlacklist, http.MethodPost, "/admin/blacklist", body, uuid.Nil, nil)
	assertEnvelopeError(t, w, 401)
}

func TestH_AddBlacklist_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{addBlErr: assert.AnError})
	body, err := json.Marshal(admin.AddBlacklistRequest{Type: "ip", Value: "1.2.3.4", Reason: "bot"})
	require.NoError(t, err)

	w := runHandler(h.AddBlacklist, http.MethodPost, "/admin/blacklist", body, uuid.New(), nil)
	assertEnvelopeError(t, w, 500)
}

// ===== E8 RemoveBlacklist =====

func TestH_RemoveBlacklist_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{rmBlResp: &admin.RemoveBlacklistResponse{ID: uuid.New()}})
	id := uuid.NewString()

	w := runHandler(h.RemoveBlacklist, http.MethodDelete, "/admin/blacklist/"+id, nil, uuid.New(),
		map[string]string{"id": id})
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_RemoveBlacklist_400_InvalidID(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.RemoveBlacklist, http.MethodDelete, "/admin/blacklist/bad-uuid", nil, uuid.New(),
		map[string]string{"id": "bad-uuid"})
	assertEnvelopeError(t, w, 400)
}

func TestH_RemoveBlacklist_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})
	id := uuid.NewString()

	w := runHandler(h.RemoveBlacklist, http.MethodDelete, "/admin/blacklist/"+id, nil, uuid.Nil,
		map[string]string{"id": id})
	assertEnvelopeError(t, w, 401)
}

func TestH_RemoveBlacklist_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{rmBlErr: assert.AnError})
	id := uuid.NewString()

	w := runHandler(h.RemoveBlacklist, http.MethodDelete, "/admin/blacklist/"+id, nil, uuid.New(),
		map[string]string{"id": id})
	assertEnvelopeError(t, w, 500)
}

// ===== E7 SuspendUser =====

func TestH_SuspendUser_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{susResp: &admin.SuspendUserResponse{
		UserID:         uuid.New(),
		Status:         "SUSPENDED",
		PreviousStatus: "ACTIVE",
	}})
	body, err := json.Marshal(admin.SuspendUserRequest{Reason: "spam"})
	require.NoError(t, err)

	w := runHandler(h.SuspendUser, http.MethodPatch, "/admin/users/"+uuid.NewString()+"/suspend", body,
		uuid.New(), map[string]string{"user_id": uuid.NewString()})
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_SuspendUser_400_InvalidBody(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.SuspendUser, http.MethodPatch, "/admin/users/"+uuid.NewString()+"/suspend", []byte(`{}`),
		uuid.New(), map[string]string{"user_id": uuid.NewString()})
	assertEnvelopeError(t, w, 400)
}

func TestH_SuspendUser_400_InvalidUserID(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})
	body, err := json.Marshal(admin.SuspendUserRequest{Reason: "spam"})
	require.NoError(t, err)

	w := runHandler(h.SuspendUser, http.MethodPatch, "/admin/users/bad-id/suspend", body,
		uuid.New(), map[string]string{"user_id": "bad-id"})
	assertEnvelopeError(t, w, 400)
}

func TestH_SuspendUser_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})
	body, err := json.Marshal(admin.SuspendUserRequest{Reason: "spam"})
	require.NoError(t, err)

	w := runHandler(h.SuspendUser, http.MethodPatch, "/admin/users/"+uuid.NewString()+"/suspend", body,
		uuid.Nil, map[string]string{"user_id": uuid.NewString()})
	assertEnvelopeError(t, w, 401)
}

func TestH_SuspendUser_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{susErr: assert.AnError})
	body, err := json.Marshal(admin.SuspendUserRequest{Reason: "spam"})
	require.NoError(t, err)

	w := runHandler(h.SuspendUser, http.MethodPatch, "/admin/users/"+uuid.NewString()+"/suspend", body,
		uuid.New(), map[string]string{"user_id": uuid.NewString()})
	assertEnvelopeError(t, w, 500)
}

// ===== E7 UnsuspendUser =====

func TestH_UnsuspendUser_200_Success(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{unsResp: &admin.UnsuspendUserResponse{
		UserID:         uuid.New(),
		Status:         "ACTIVE",
		PreviousStatus: "SUSPENDED",
	}})

	w := runHandler(h.UnsuspendUser, http.MethodPatch, "/admin/users/"+uuid.NewString()+"/unsuspend", nil,
		uuid.New(), map[string]string{"user_id": uuid.NewString()})
	assertEnvelopeSuccess(t, w, 200)
}

func TestH_UnsuspendUser_400_InvalidUserID(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.UnsuspendUser, http.MethodPatch, "/admin/users/bad-id/unsuspend", nil,
		uuid.New(), map[string]string{"user_id": "bad-id"})
	assertEnvelopeError(t, w, 400)
}

func TestH_UnsuspendUser_401_MissingAdmin(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{})

	w := runHandler(h.UnsuspendUser, http.MethodPatch, "/admin/users/"+uuid.NewString()+"/unsuspend", nil,
		uuid.Nil, map[string]string{"user_id": uuid.NewString()})
	assertEnvelopeError(t, w, 401)
}

func TestH_UnsuspendUser_500_SvcError(t *testing.T) {
	h := admin.NewAdminHandler(&fakeSvc{unsErr: assert.AnError})

	w := runHandler(h.UnsuspendUser, http.MethodPatch, "/admin/users/"+uuid.NewString()+"/unsuspend", nil,
		uuid.New(), map[string]string{"user_id": uuid.NewString()})
	assertEnvelopeError(t, w, 500)
}
