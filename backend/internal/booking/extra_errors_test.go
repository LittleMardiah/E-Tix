package booking

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

	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// ============================================================
// Extra booking tests — P9a-5 (round 2)
// Targets the remaining uncovered error/compensating paths in
// BookingService.LockSeats / GetSeatAvailability /
// ReleaseSeatsForExpiry and in BookingHandler.LockSeats.
// ============================================================

// errRepo is a BookingRepository whose every method can be made to fail.
type errRepo struct {
	section   *SectionRow
	sectErr   error
	taken     []int
	takenErr  error
	createErr error
	onCreate  func(ctx context.Context)
}

func (f *errRepo) GetSectionByID(_ context.Context, _ uuid.UUID) (*SectionRow, error) {
	if f.sectErr != nil {
		return nil, f.sectErr
	}
	if f.section == nil {
		return nil, ErrSectionNotFound
	}
	return f.section, nil
}

func (f *errRepo) GetTakenSeats(_ context.Context, _ uuid.UUID) ([]int, error) {
	if f.takenErr != nil {
		return nil, f.takenErr
	}
	return f.taken, nil
}

func (f *errRepo) CreateBooking(ctx context.Context, _ *Booking) error {
	if f.onCreate != nil {
		f.onCreate(ctx)
	}
	return f.createErr
}

func (f *errRepo) GetByID(_ context.Context, _ uuid.UUID) (*Booking, error) {
	return nil, ErrBookingNotFound
}

func (f *errRepo) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*Booking, error) {
	return nil, ErrBookingNotFound
}

func (f *errRepo) GetEventStartAt(_ context.Context, _ uuid.UUID) (time.Time, error) {
	return time.Now().UTC().Add(24 * time.Hour), nil
}

// newSvcWithScripts builds a service with a custom Lua script set and
// returns the queue-token service that issued its tokens.
func newSvcWithScripts(t *testing.T, repo BookingRepository, rdb *redis.Client, pub DelayedPublisher, lockLua string) (*BookingService, *queue.QueueTokenService) {
	t.Helper()
	qt := newBookingTestQueueToken(t)
	scripts := &LuaScripts{Lock: lockLua, Release: releaseSeatsLua}
	return NewBookingService(repo, rdb, scripts, qt, pub), qt
}

// ============================================================
// Service: LockSeats error paths
// ============================================================

func TestLockSeats_SectionLookupErrorPropagates(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &errRepo{sectErr: errors.New("section query failed")}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"},
		newValidToken(t, qt))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "section query failed")
}

func TestLockSeats_InvalidLockResultJSON(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &errRepo{section: newSection(eid, sid, 10)}
	// The Lua script answers with a non-JSON payload, so the service must
	// reject it instead of treating it as a successful lock.
	svc, qt := newSvcWithScripts(t, repo, rdb, &fakePublisher{}, "return 'not-json'")

	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"},
		newValidToken(t, qt))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse lock result")
}

func TestLockSeats_CompensatingReleaseFailureIsLogged(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	repo := &errRepo{
		section:   newSection(eid, sid, 10),
		createErr: errors.New("insert booking failed"),
	}
	// Turn the per-seat owner key into a hash so the release script's
	// GET raises a WRONGTYPE error, exercising the compensating-release
	// failure log branch.
	ownerKey := "owner:seats:" + eid.String() + ":" + sid.String() + ":v1:1"
	repo.onCreate = func(cctx context.Context) {
		require.NoError(t, rdb.Del(cctx, ownerKey).Err())
		require.NoError(t, rdb.Client.HSet(cctx, ownerKey, "f", "x").Err())
	}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	_, err := svc.LockSeats(ctx, uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"},
		newValidToken(t, qt))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert booking failed")
}

func TestLockSeats_PublishDelayedFailureDoesNotFailBooking(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &errRepo{section: newSection(eid, sid, 10)}
	pub := &fakePublisher{err: errors.New("broker down")}
	svc, qt := newTestService(t, repo, rdb, pub)

	resp, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"},
		newValidToken(t, qt))

	// The delayed release-seat check is best-effort: the booking stands.
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.BookingID)
	assert.Len(t, pub.calls, 1)
	assert.Equal(t, "release_seat_check", pub.calls[0].queue)
}

// ============================================================
// Service: GetSeatAvailability / ReleaseSeatsForExpiry
// ============================================================

func TestGetSeatAvailability_TakenSeatsLookupError(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &errRepo{section: newSection(eid, sid, 10), takenErr: errors.New("tickets query failed")}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	_, err := svc.GetSeatAvailability(context.Background(), eid, sid)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tickets query failed")
}

func TestReleaseSeatsForExpiry_InvalidBookingMappingWarnsAndFallsBack(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	bookingID := uuid.New()

	sec := newSection(eid, sid, 10)
	sec.LayoutVersion = 3
	repo := &expiryFakeRepo{
		booking: &Booking{ID: bookingID, EventID: eid, SectionID: sid, SeatPositions: []int{1}},
		section: sec,
	}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	// A corrupt lock mapping must not abort the release: the service logs
	// and falls back to the section's current layout version.
	require.NoError(t, rdb.Set(ctx, "booking:"+bookingID.String(), "not-json", 0).Err())

	require.NoError(t, svc.ReleaseSeatsForExpiry(ctx, bookingID))
}

func TestReleaseSeatsForExpiry_ReleaseScriptError(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	bookingID := uuid.New()

	sec := newSection(eid, sid, 10)
	repo := &expiryFakeRepo{
		booking: &Booking{ID: bookingID, EventID: eid, SectionID: sid, SeatPositions: []int{1}},
		section: sec,
	}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	// A hash (not a string) at the owner key makes the release script fail.
	ownerKey := "owner:seats:" + eid.String() + ":" + sid.String() + ":v1:1"
	require.NoError(t, rdb.Client.HSet(ctx, ownerKey, "f", "x").Err())

	err := svc.ReleaseSeatsForExpiry(ctx, bookingID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "eval script")
}

// ============================================================
// Handler: LockSeats error mapping
// ============================================================

// doBookingRequestWithIdem lets the caller pin the idempotency key.
func doBookingRequestWithIdem(t *testing.T, router *gin.Engine, method, path, userID, queueToken, idemKey, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(method, path, bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(testUserIDHeader, userID)
	req.Header.Set(testQueueTokenHdr, queueToken)
	req.Header.Set("X-Idempotency-Key", idemKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestHandlerLockSeats_SectionNotFoundReturns404(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	// The section belongs to a different event.
	other := newSection(uuid.New(), sid, 10)
	repo := &errRepo{section: other}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	r := buildBookingTestRouter(h)

	w := doBookingRequestWithIdem(t, r, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), newValidToken(t, qt), "idem-404",
		`{"seat_positions":[1],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"NOT_FOUND"`)
}

func TestHandlerLockSeats_EmptySeatPositionsReturns400(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &errRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	r := buildBookingTestRouter(h)

	w := doBookingRequestWithIdem(t, r, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), newValidToken(t, qt), "idem-empty",
		`{"seat_positions":[],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandlerLockSeats_UnknownErrorReturns500(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &errRepo{sectErr: errors.New("unexpected db failure")}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	r := buildBookingTestRouter(h)

	w := doBookingRequestWithIdem(t, r, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), newValidToken(t, qt), "idem-500",
		`{"seat_positions":[1],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
}

func TestHandlerLockSeats_CorruptCacheEntryFallsThroughToNormalPath(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	userID := uuid.New()
	idemKey := "idem-corrupt"
	repo := &errRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	r := buildBookingTestRouter(h)

	// Seed a corrupt L1 entry for this exact key. The handler must ignore it
	// and perform a real lock instead of failing.
	hash := computeBookingIdemHash(idemKey, userID, eid, sid)
	require.NoError(t, rdb.Set(ctx, bookingIdemPrefix+hash, "not-json", 0).Err())

	w := doBookingRequestWithIdem(t, r, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		userID.String(), newValidToken(t, qt), idemKey,
		`{"seat_positions":[1],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
}

func TestHandlerLockSeats_ReplayReturnsCachedBody(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	userID := uuid.New()
	idemKey := "idem-replay"
	repo := &errRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	r := buildBookingTestRouter(h)

	path := "/events/" + eid.String() + "/sections/" + sid.String() + "/seats"
	body := `{"seat_positions":[1],"payment_mode":"SPLIT"}`

	first := doBookingRequestWithIdem(t, r, http.MethodPost, path, userID.String(), newValidToken(t, qt), idemKey, body)
	require.Equal(t, http.StatusCreated, first.Code)

	second := doBookingRequestWithIdem(t, r, http.MethodPost, path, userID.String(), newValidToken(t, qt), idemKey, body)
	require.Equal(t, http.StatusCreated, second.Code)
	assert.Equal(t, first.Body.String(), second.Body.String(),
		"an idempotent replay must return the identical cached body")
}
