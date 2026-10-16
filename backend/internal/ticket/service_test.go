package ticket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
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

// ---------- fakes ----------

type fakeQRStore struct {
	m map[string]string
}

func newFakeQRStore() *fakeQRStore { return &fakeQRStore{m: map[string]string{}} }

func (f *fakeQRStore) Get(_ context.Context, key string) (string, error) {
	return f.m[key], nil
}

func (f *fakeQRStore) Set(_ context.Context, key, value string, _ time.Duration) error {
	f.m[key] = value
	return nil
}

// fakeTx mirrors payment/worker test pattern (embed nil pgx.Tx).
type fakeTx struct {
	pgx.Tx
	committed  bool
	rolledBack bool
}

func (f *fakeTx) Commit(_ context.Context) error {
	f.committed = true
	return nil
}
func (f *fakeTx) Rollback(_ context.Context) error {
	if f.committed {
		return nil
	}
	f.rolledBack = true
	return nil
}

// fakeBeginner mirrors payment's fakeBeginner (tx + err), auto-creates tx if nil.
type fakeBeginner struct {
	tx  *fakeTx
	err error
}

func (f *fakeBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.tx == nil {
		f.tx = &fakeTx{}
	}
	return f.tx, nil
}

type fakeRepo struct {
	listItems []TicketListItem
	listTotal int
	listErr   error

	detail    *TicketDetail
	detailErr error

	forUpdate    *Ticket
	forUpdateErr error

	updateRows int64
	updateErr  error

	userStatus      string
	userBlacklisted bool
	userErr         error
}

func (f *fakeRepo) ListByOwner(_ context.Context, _ uuid.UUID, _ string, _, _ int) ([]TicketListItem, int, error) {
	return f.listItems, f.listTotal, f.listErr
}
func (f *fakeRepo) GetByID(_ context.Context, _ uuid.UUID) (*TicketDetail, error) {
	return f.detail, f.detailErr
}
func (f *fakeRepo) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*Ticket, error) {
	return f.forUpdate, f.forUpdateErr
}
func (f *fakeRepo) UpdateAssign(_ context.Context, _ pgx.Tx, _, _ uuid.UUID) (int64, error) {
	return f.updateRows, f.updateErr
}
func (f *fakeRepo) GetUserStatusAndBlacklist(_ context.Context, _ pgx.Tx, _ uuid.UUID) (string, bool, error) {
	return f.userStatus, f.userBlacklisted, f.userErr
}
func (f *fakeRepo) MarkFallbackPINUsed(_ context.Context, _ pgx.Tx, _ uuid.UUID) (int64, error) {
	return 1, nil
}

// helper: build service with fakes; return fakeTx for assertions.
func newSvc(repo TicketRepository, qs QRStore) (*TicketService, *fakeTx) {
	tx := &fakeTx{}
	return NewTicketService(repo, qs, &fakeBeginner{tx: tx}), tx
}

// ---------- ListByOwner ----------

func TestListByOwner_InvalidStatusFilter(t *testing.T) {
	s, _ := newSvc(&fakeRepo{}, newFakeQRStore())
	_, _, err := s.ListByOwner(context.Background(), uuid.New(), "GARBAGE", 1, 20)
	assert.ErrorIs(t, err, ErrInvalidStatusFilter)
}

func TestListByOwner_PageAndLimitClamp(t *testing.T) {
	s, _ := newSvc(&fakeRepo{}, newFakeQRStore())
	items, total, err := s.ListByOwner(context.Background(), uuid.New(), "", -5, 999)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.Equal(t, 0, total)
}

func TestListByOwner_DelegateOK(t *testing.T) {
	repo := &fakeRepo{listItems: []TicketListItem{{Status: "CONFIRMED"}}, listTotal: 1}
	s, _ := newSvc(repo, newFakeQRStore())
	items, total, err := s.ListByOwner(context.Background(), uuid.New(), "CONFIRMED", 1, 20)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, 1, total)
}

// ---------- GetByID ----------

func TestGetByID_Success(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uid}}}
	s, _ := newSvc(repo, newFakeQRStore())
	td, err := s.GetByID(context.Background(), uid, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, uid, td.OwnerUserID)
}

func TestGetByID_Forbidden(t *testing.T) {
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uuid.New()}}}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.GetByID(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrTicketForbidden)
}

func TestGetByID_NotFoundPropagates(t *testing.T) {
	repo := &fakeRepo{detailErr: ErrTicketNotFound}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.GetByID(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrTicketNotFound)
}

// ---------- GetQR ----------

func TestGetQR_CacheMissThenHit(t *testing.T) {
	uid := uuid.New()
	tid := uuid.New()
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{
		OwnerUserID: uid, Status: "CONFIRMED", QRSecretKey: "secret",
	}}}
	qs := newFakeQRStore()
	s, _ := newSvc(repo, qs)

	r1, err := s.GetQR(context.Background(), uid, tid)
	require.NoError(t, err)
	assert.NotEmpty(t, r1.QRData)
	assert.Greater(t, r1.RefreshIn, 0)

	r2, err := s.GetQR(context.Background(), uid, tid)
	require.NoError(t, err)
	assert.Equal(t, r1.QRData, r2.QRData)
}

func TestGetQR_Forbidden(t *testing.T) {
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uuid.New(), Status: "CONFIRMED"}}}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.GetQR(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrTicketForbidden)
}

func TestGetQR_NotActive(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uid, Status: "USED"}}}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.GetQR(context.Background(), uid, uuid.New())
	assert.ErrorIs(t, err, ErrTicketNotActive)
}

// ---------- Assign ----------

func TestAssign_Forbidden(t *testing.T) {
	repo := &fakeRepo{forUpdate: &Ticket{OwnerUserID: uuid.New(), Status: "CONFIRMED"}}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), uuid.New(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrTicketForbidden)
}

func TestAssign_NotConfirmed(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{forUpdate: &Ticket{OwnerUserID: owner, Status: "USED"}}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), owner, uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrTicketNotConfirmed)
}

func TestAssign_AlreadyAssigned(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{forUpdate: &Ticket{OwnerUserID: owner, Status: "CONFIRMED", IsAssigned: true}}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), owner, uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrTicketAlreadyAssigned)
}

func TestAssign_TargetInactive(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{
		forUpdate:  &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userStatus: "SUSPENDED",
	}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), owner, uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrTargetUserInactive)
}

func TestAssign_TargetBlacklisted(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{
		forUpdate:       &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userStatus:      "ACTIVE",
		userBlacklisted: true,
	}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), owner, uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrTargetUserInactive)
}

func TestAssign_ConcurrentConflict(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{
		forUpdate:  &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userStatus: "ACTIVE",
		updateRows: 0,
	}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), owner, uuid.New(), uuid.New())
	assert.ErrorIs(t, err, ErrAssignConflict)
}

func TestAssign_Success(t *testing.T) {
	owner := uuid.New()
	target := uuid.New()
	repo := &fakeRepo{
		forUpdate:  &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userStatus: "ACTIVE",
		updateRows: 1,
	}
	s, tx := newSvc(repo, newFakeQRStore())
	ticket, err := s.Assign(context.Background(), owner, uuid.New(), target)
	require.NoError(t, err)
	assert.Equal(t, target, ticket.OwnerUserID)
	assert.True(t, ticket.IsAssigned)
	require.NotNil(t, ticket.OriginalOwnerUserID)
	assert.Equal(t, owner, *ticket.OriginalOwnerUserID)
	assert.True(t, tx.committed, "success path must commit")
}

func TestAssign_ErrorRollsBack(t *testing.T) {
	repo := &fakeRepo{forUpdate: &Ticket{OwnerUserID: uuid.New(), Status: "CONFIRMED"}}
	s, tx := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), uuid.New(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.False(t, tx.committed)
}

// ---------- service error branches (E9-A2 gate) ----------

func TestGetQR_RepoError(t *testing.T) {
	repo := &fakeRepo{detailErr: errors.New("db down")}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.GetQR(context.Background(), uuid.New(), uuid.New())
	assert.Error(t, err)
}

func TestGetQR_EmptySecretKey(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uid, Status: "CONFIRMED", QRSecretKey: ""}}}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.GetQR(context.Background(), uid, uuid.New())
	assert.Error(t, err)
}

func TestAssign_BeginError(t *testing.T) {
	s := NewTicketService(&fakeRepo{}, newFakeQRStore(), &fakeBeginner{err: errors.New("begin fail")})
	_, err := s.Assign(context.Background(), uuid.New(), uuid.New(), uuid.New())
	assert.Error(t, err)
}

func TestAssign_ForUpdateError(t *testing.T) {
	repo := &fakeRepo{forUpdateErr: errors.New("row gone")}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), uuid.New(), uuid.New(), uuid.New())
	assert.Error(t, err)
}

func TestAssign_UserStatusError(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{
		forUpdate: &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userErr:   errors.New("user query fail"),
	}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), owner, uuid.New(), uuid.New())
	assert.Error(t, err)
}

func TestAssign_UpdateError(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{
		forUpdate:  &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userStatus: "ACTIVE",
		updateErr:  errors.New("update fail"),
	}
	s, _ := newSvc(repo, newFakeQRStore())
	_, err := s.Assign(context.Background(), owner, uuid.New(), uuid.New())
	assert.Error(t, err)
}

// ---------- Cache helpers ----------

func TestCacheGetSet(t *testing.T) {
	qs := newFakeQRStore()
	s, _ := newSvc(&fakeRepo{}, qs)
	ctx := context.Background()

	v, err := s.CacheGet(ctx, "k1")
	require.NoError(t, err)
	assert.Empty(t, v)

	require.NoError(t, s.CacheSet(ctx, "k1", "v1", time.Minute))

	v, err = s.CacheGet(ctx, "k1")
	require.NoError(t, err)
	assert.Equal(t, "v1", v)
}

// ============================================================
// Handler tests (kept in service_test.go per scope E9-A2:
// only internal/ticket/service_test.go may be created)
// ============================================================

// newCtx builds a gin test context + recorder, setting method/target/body,
// optional auth user_id, :ticket_id param, and X-Idempotency-Key header.
func newCtx(method, target, body, userID, ticketID, idemKey string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	if idemKey != "" {
		req.Header.Set("X-Idempotency-Key", idemKey)
	}
	c.Request = req
	if userID != "" {
		c.Set("user_id", userID)
	}
	if ticketID != "" {
		c.Params = gin.Params{{Key: "ticket_id", Value: ticketID}}
	}
	return c, w
}

func newTestHandler(repo TicketRepository, qs QRStore) (*TicketHandler, *fakeTx) {
	s, tx := newSvc(repo, qs)
	return NewTicketHandler(s), tx
}

// assignCacheKey mirrors handler.go:215 cache key derivation.
func assignCacheKey(idemKey string, ownerID, ticketID uuid.UUID) string {
	sum := sha256.Sum256([]byte(idemKey + ":" + ownerID.String() + ":" + ticketID.String()))
	return "idem:assign:" + hex.EncodeToString(sum[:])
}

// ---------- List ----------

func TestHandlerList_MissingAuth(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/tickets", "", "", "", "")
	h.List(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerList_InvalidUserID(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/tickets", "", "not-a-uuid", "", "")
	h.List(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerList_InvalidPage(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/tickets?page=abc", "", uuid.NewString(), "", "")
	h.List(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerList_InvalidLimit(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/tickets?page=1&limit=xyz", "", uuid.NewString(), "", "")
	h.List(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerList_InvalidStatusFilter(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/tickets?status=GARBAGE", "", uuid.NewString(), "", "")
	h.List(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerList_NotFound(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{listErr: ErrTicketNotFound}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/tickets", "", uuid.NewString(), "", "")
	h.List(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerList_InternalError(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{listErr: errors.New("db down")}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/tickets", "", uuid.NewString(), "", "")
	h.List(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandlerList_Success(t *testing.T) {
	repo := &fakeRepo{listItems: []TicketListItem{{Status: "CONFIRMED"}}, listTotal: 1}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/tickets?status=CONFIRMED&page=1&limit=20", "", uuid.NewString(), "", "")
	h.List(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}

// ---------- GetByID ----------

func TestHandlerGetByID_MissingAuth(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", "", uuid.NewString(), "")
	h.GetByID(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerGetByID_InvalidUserID(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", "bad-uuid", uuid.NewString(), "")
	h.GetByID(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerGetByID_InvalidTicketID(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uuid.NewString(), "not-a-uuid", "")
	h.GetByID(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerGetByID_NotFound(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{detailErr: ErrTicketNotFound}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uuid.NewString(), uuid.NewString(), "")
	h.GetByID(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerGetByID_Forbidden(t *testing.T) {
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uuid.New()}}}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uuid.NewString(), uuid.NewString(), "")
	h.GetByID(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandlerGetByID_InternalError(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{detailErr: errors.New("boom")}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uuid.NewString(), uuid.NewString(), "")
	h.GetByID(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandlerGetByID_Success(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uid}}}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uid.String(), uuid.NewString(), "")
	h.GetByID(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}

// ---------- GetQR ----------

func TestHandlerGetQR_MissingAuth(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", "", uuid.NewString(), "")
	h.GetQR(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerGetQR_InvalidUserID(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", "bad-uuid", uuid.NewString(), "")
	h.GetQR(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerGetQR_InvalidTicketID(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uuid.NewString(), "not-a-uuid", "")
	h.GetQR(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerGetQR_NotFound(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{detailErr: ErrTicketNotFound}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uuid.NewString(), uuid.NewString(), "")
	h.GetQR(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerGetQR_Forbidden(t *testing.T) {
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uuid.New(), Status: "CONFIRMED"}}}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uuid.NewString(), uuid.NewString(), "")
	h.GetQR(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandlerGetQR_NotActive(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uid, Status: "USED"}}}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uid.String(), uuid.NewString(), "")
	h.GetQR(c)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandlerGetQR_InternalError(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{detailErr: errors.New("boom")}, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uuid.NewString(), uuid.NewString(), "")
	h.GetQR(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandlerGetQR_Success(t *testing.T) {
	uid := uuid.New()
	repo := &fakeRepo{detail: &TicketDetail{Ticket: Ticket{OwnerUserID: uid, Status: "CONFIRMED", QRSecretKey: "s3cret"}}}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodGet, "/x", "", uid.String(), uuid.NewString(), "")
	h.GetQR(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"qr_data"`)
}

// ---------- Assign ----------

func assignBody(userID uuid.UUID) string {
	return `{"user_id":"` + userID.String() + `"}`
}

func TestHandlerAssign_MissingIdempotencyKey(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), uuid.NewString(), uuid.NewString(), "")
	h.Assign(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerAssign_MissingAuth(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), "", uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerAssign_InvalidUserID(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), "bad-uuid", uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandlerAssign_InvalidTicketID(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), uuid.NewString(), "not-a-uuid", "key")
	h.Assign(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerAssign_InvalidBody(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", "not-json", uuid.NewString(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerAssign_NilUserID(t *testing.T) {
	body := `{"user_id":"00000000-0000-0000-0000-000000000000"}`
	h, _ := newTestHandler(&fakeRepo{}, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", body, uuid.NewString(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandlerAssign_CacheHit(t *testing.T) {
	owner := uuid.New()
	tid := uuid.New()
	qs := newFakeQRStore()
	h, _ := newTestHandler(&fakeRepo{}, qs)
	require.NoError(t, qs.Set(context.Background(), assignCacheKey("key", owner, tid), `{"success":true}`, time.Minute))

	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), owner.String(), tid.String(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}

func TestHandlerAssign_NotFound(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{forUpdateErr: ErrTicketNotFound}, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), uuid.NewString(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandlerAssign_Forbidden(t *testing.T) {
	repo := &fakeRepo{forUpdate: &Ticket{OwnerUserID: uuid.New(), Status: "CONFIRMED"}}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), uuid.NewString(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandlerAssign_NotConfirmed(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{forUpdate: &Ticket{OwnerUserID: owner, Status: "USED"}}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), owner.String(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandlerAssign_Conflict(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{
		forUpdate:  &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userStatus: "ACTIVE",
		updateRows: 0,
	}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), owner.String(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandlerAssign_AlreadyAssignedConflict(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{forUpdate: &Ticket{OwnerUserID: owner, Status: "CONFIRMED", IsAssigned: true}}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), owner.String(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandlerAssign_TargetInactive(t *testing.T) {
	owner := uuid.New()
	repo := &fakeRepo{
		forUpdate:  &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userStatus: "SUSPENDED",
	}
	h, _ := newTestHandler(repo, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), owner.String(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandlerAssign_InternalError(t *testing.T) {
	h, _ := newTestHandler(&fakeRepo{forUpdateErr: errors.New("boom")}, newFakeQRStore())
	c, w := newCtx(http.MethodPatch, "/x", assignBody(uuid.New()), uuid.NewString(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandlerAssign_Success(t *testing.T) {
	owner := uuid.New()
	target := uuid.New()
	qs := newFakeQRStore()
	repo := &fakeRepo{
		forUpdate:  &Ticket{OwnerUserID: owner, Status: "CONFIRMED"},
		userStatus: "ACTIVE",
		updateRows: 1,
	}
	h, tx := newTestHandler(repo, qs)
	c, w := newCtx(http.MethodPatch, "/x", assignBody(target), owner.String(), uuid.NewString(), "key")
	h.Assign(c)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, tx.committed)
	assert.Contains(t, w.Body.String(), `"success":true`)
}
