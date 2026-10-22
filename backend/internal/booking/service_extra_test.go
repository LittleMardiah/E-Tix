package booking

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// ============================================================
// Extra booking tests — P9a-5
// Covers the previously-untested service paths:
// ReleaseSeatsForExpiry, readLockLayoutVersion, readLockedBitmap,
// bitAt, lockResultRaw, plus handler error/idempotency branches.
// ============================================================

// expiryFakeRepo serves ReleaseSeatsForExpiry, which needs a real booking row
// (the shared fakeRepo always returns ErrBookingNotFound from GetByID).
type expiryFakeRepo struct {
	booking    *Booking
	section    *SectionRow
	getByIDErr error
	sectErr    error
}

func (f *expiryFakeRepo) GetSectionByID(_ context.Context, _ uuid.UUID) (*SectionRow, error) {
	if f.sectErr != nil {
		return nil, f.sectErr
	}
	if f.section == nil {
		return nil, ErrSectionNotFound
	}
	return f.section, nil
}

func (f *expiryFakeRepo) GetTakenSeats(_ context.Context, _ uuid.UUID) ([]int, error) {
	return nil, nil
}

func (f *expiryFakeRepo) CreateBooking(_ context.Context, _ *Booking) error { return nil }

func (f *expiryFakeRepo) GetByID(_ context.Context, _ uuid.UUID) (*Booking, error) {
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}
	if f.booking == nil {
		return nil, ErrBookingNotFound
	}
	return f.booking, nil
}

func (f *expiryFakeRepo) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*Booking, error) {
	return f.GetByID(context.Background(), uuid.Nil)
}

func (f *expiryFakeRepo) GetEventStartAt(_ context.Context, _ uuid.UUID) (time.Time, error) {
	return time.Now().UTC(), nil
}

// ============================================================
// Service: ReleaseSeatsForExpiry
// ============================================================

func TestReleaseSeatsForExpiry_ClearsOwnedSeatBits(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	bookingID := uuid.New()

	sec := newSection(eid, sid, 10)
	repo := &expiryFakeRepo{
		booking: &Booking{
			ID:            bookingID,
			EventID:       eid,
			SectionID:     sid,
			SeatPositions: []int{1, 2},
			TotalSeats:    2,
		},
		section: sec,
	}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	// Simulate a prior lock: bits set + owner tags + booking mapping.
	key := bitmapKey(eid, sid)
	require.NoError(t, rdb.SetBit(ctx, key, 0, 1).Err())
	require.NoError(t, rdb.SetBit(ctx, key, 1, 1).Err())
	require.NoError(t, rdb.Set(ctx, "owner:"+key+":1", bookingID.String(), 0).Err())
	require.NoError(t, rdb.Set(ctx, "owner:"+key+":2", bookingID.String(), 0).Err())
	require.NoError(t, rdb.Set(ctx, "booking:"+bookingID.String(),
		`{"layout_version":1}`, 0).Err())

	require.NoError(t, svc.ReleaseSeatsForExpiry(ctx, bookingID))

	b0, _ := rdb.GetBit(ctx, key, 0).Result()
	b1, _ := rdb.GetBit(ctx, key, 1).Result()
	assert.Equal(t, int64(0), b0, "seat 1 must be released")
	assert.Equal(t, int64(0), b1, "seat 2 must be released")
}

func TestReleaseSeatsForExpiry_UsesCapturedLayoutVersion(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	bookingID := uuid.New()

	// Section has since been re-laid-out (LayoutVersion=1), but the lock-time
	// snapshot says version 7 — the release must target v7 (H3 FIX).
	sec := newSection(eid, sid, 10)
	sec.LayoutVersion = 1
	repo := &expiryFakeRepo{
		booking: &Booking{ID: bookingID, EventID: eid, SectionID: sid, SeatPositions: []int{3}},
		section: sec,
	}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	v7Key := "seats:" + eid.String() + ":" + sid.String() + ":v7"
	require.NoError(t, rdb.SetBit(ctx, v7Key, 2, 1).Err())
	require.NoError(t, rdb.Set(ctx, "owner:"+v7Key+":3", bookingID.String(), 0).Err())
	require.NoError(t, rdb.Set(ctx, "booking:"+bookingID.String(),
		`{"layout_version":7}`, 0).Err())

	require.NoError(t, svc.ReleaseSeatsForExpiry(ctx, bookingID))

	b2, _ := rdb.GetBit(ctx, v7Key, 2).Result()
	assert.Equal(t, int64(0), b2, "seat 3 on the v7 bitmap must be released")
}

func TestReleaseSeatsForExpiry_FallsBackToCurrentLayoutVersion(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	bookingID := uuid.New()

	sec := newSection(eid, sid, 10)
	sec.LayoutVersion = 4
	repo := &expiryFakeRepo{
		booking: &Booking{ID: bookingID, EventID: eid, SectionID: sid, SeatPositions: []int{1}},
		section: sec,
	}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	// No booking:{id} mapping → cache miss → fallback to section version.
	v4Key := "seats:" + eid.String() + ":" + sid.String() + ":v4"
	require.NoError(t, rdb.SetBit(ctx, v4Key, 0, 1).Err())
	require.NoError(t, rdb.Set(ctx, "owner:"+v4Key+":1", bookingID.String(), 0).Err())

	require.NoError(t, svc.ReleaseSeatsForExpiry(ctx, bookingID))

	b0, _ := rdb.GetBit(ctx, v4Key, 0).Result()
	assert.Equal(t, int64(0), b0, "fallback path must still release the seat")
}

func TestReleaseSeatsForExpiry_GetByIDError(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	repo := &expiryFakeRepo{getByIDErr: errors.New("db down")}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	err := svc.ReleaseSeatsForExpiry(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get by id")
}

func TestReleaseSeatsForExpiry_SectionLookupError(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	repo := &expiryFakeRepo{
		booking: &Booking{ID: uuid.New(), SectionID: uuid.New()},
		sectErr: errors.New("section boom"),
	}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	err := svc.ReleaseSeatsForExpiry(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get section")
}

// ============================================================
// Service: readLockLayoutVersion
// ============================================================

func TestReadLockLayoutVersion_CacheMissReturnsZero(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	svc, _ := newTestService(t, &fakeRepo{}, rdb, &fakePublisher{})

	v, err := svc.readLockLayoutVersion(context.Background(), uuid.New())

	require.NoError(t, err, "cache miss must not be an error")
	assert.Equal(t, 0, v, "cache miss must yield 0 so the caller falls back")
}

func TestReadLockLayoutVersion_ParsesSnapshot(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	bookingID := uuid.New()
	svc, _ := newTestService(t, &fakeRepo{}, rdb, &fakePublisher{})

	require.NoError(t, rdb.Set(ctx, "booking:"+bookingID.String(),
		`{"layout_version":9,"user_id":"x"}`, 0).Err())

	v, err := svc.readLockLayoutVersion(ctx, bookingID)

	require.NoError(t, err)
	assert.Equal(t, 9, v)
}

func TestReadLockLayoutVersion_InvalidJSON(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	bookingID := uuid.New()
	svc, _ := newTestService(t, &fakeRepo{}, rdb, &fakePublisher{})

	require.NoError(t, rdb.Set(ctx, "booking:"+bookingID.String(), `not-json`, 0).Err())

	_, err := svc.readLockLayoutVersion(ctx, bookingID)

	require.Error(t, err)
}

func TestReadLockLayoutVersion_RedisError(t *testing.T) {
	// FIX P9a-12 TD-F9-16: exclusively owned dead port (see deadRedisAddr).
	svc, _ := newTestService(t, &fakeRepo{}, newDeadRedis(t), &fakePublisher{})

	_, err := svc.readLockLayoutVersion(context.Background(), uuid.New())

	require.Error(t, err)
	assert.NotErrorIs(t, err, goredis.Nil)
}

// ============================================================
// Service: readLockedBitmap + bitAt
// ============================================================

func TestReadLockedBitmap_KeyAbsentReturnsEmptyMap(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	svc, _ := newTestService(t, &fakeRepo{}, rdb, &fakePublisher{})

	bits, err := svc.readLockedBitmap(context.Background(), eid, sid, 1, 10)

	require.NoError(t, err)
	assert.Empty(t, bits)
}

func TestReadLockedBitmap_ReturnsSetBitsOnly(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	svc, _ := newTestService(t, &fakeRepo{}, rdb, &fakePublisher{})

	key := bitmapKey(eid, sid)
	require.NoError(t, rdb.SetBit(ctx, key, 0, 1).Err()) // seat 1
	require.NoError(t, rdb.SetBit(ctx, key, 4, 1).Err()) // seat 5

	bits, err := svc.readLockedBitmap(ctx, eid, sid, 1, 10)

	require.NoError(t, err)
	assert.True(t, bits[1])
	assert.True(t, bits[5])
	assert.False(t, bits[2])
	assert.Len(t, bits, 2, "only set bits are reported")
}

func TestReadLockedBitmap_RedisError(t *testing.T) {
	// FIX P9a-12 TD-F9-16: exclusively owned dead port (see deadRedisAddr).
	eid, sid := uuid.New(), uuid.New()
	svc, _ := newTestService(t, &fakeRepo{}, newDeadRedis(t), &fakePublisher{})

	_, err := svc.readLockedBitmap(context.Background(), eid, sid, 1, 10)

	require.Error(t, err)
}

func TestBitAt_FirstByteMSBIsBitZero(t *testing.T) {
	// Redis SETBIT is MSB-first inside each byte: offset 0 == 0x80.
	assert.Equal(t, 1, bitAt([]byte{0x80}, 0))
	assert.Equal(t, 0, bitAt([]byte{0x80}, 1))
	assert.Equal(t, 1, bitAt([]byte{0x01}, 7))
	assert.Equal(t, 1, bitAt([]byte{0x40}, 1))
}

func TestBitAt_OutOfRangeIndexReturnsZero(t *testing.T) {
	assert.Equal(t, 0, bitAt([]byte{0xFF}, 8))
	assert.Equal(t, 0, bitAt(nil, 0))
}

func TestBitAt_MultipleBytes(t *testing.T) {
	// Redis numbering: offset 8 is the MSB of byte 1, offset 15 is its LSB.
	raw := []byte{0x00, 0x01}
	assert.Equal(t, 1, bitAt(raw, 15))
	assert.Equal(t, 0, bitAt(raw, 14))
	assert.Equal(t, 0, bitAt(raw, 16))
}

// ============================================================
// Service: lockResultRaw
// ============================================================

func TestLockResultRaw_String(t *testing.T) {
	raw, err := lockResultRaw(`{"status":"SUCCESS"}`)
	require.NoError(t, err)
	assert.Equal(t, `{"status":"SUCCESS"}`, raw)
}

func TestLockResultRaw_Bytes(t *testing.T) {
	raw, err := lockResultRaw([]byte(`{"status":"CONFLICT"}`))
	require.NoError(t, err)
	assert.Equal(t, `{"status":"CONFLICT"}`, raw)
}

func TestLockResultRaw_UnexpectedTypeIsError(t *testing.T) {
	_, err := lockResultRaw(int64(42))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected lock result type")
}

// ============================================================
// Service: LockSeats error branches
// ============================================================

func TestLockSeats_InvalidQueueToken_ReturnsErrQueueTokenInvalid(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, "not-a-jwt")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQueueTokenInvalid)
}

func TestLockSeats_DuplicateSeatPosition(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{5, 5}, PaymentMode: "SPLIT"}, tok)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicateSeatPosition)
}

func TestLockSeats_MarkUsedRedisFailure(t *testing.T) {
	// FIX P9a-12 TD-F9-16: exclusively owned dead port (see deadRedisAddr).
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, newDeadRedis(t), &fakePublisher{})
	tok := newValidToken(t, qt)

	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mark queue token used")
}

func TestLockSeats_UnparsableSectionPrice(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	sec := newSection(eid, sid, 10)
	sec.Price = "not-a-number"
	repo := &fakeRepo{section: sec}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse section price")
}

func TestLockSeats_LuaUnexpectedStatusIsRejected(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	scripts := &LuaScripts{
		Lock:    `return cjson.encode({status='WEIRD'})`,
		Release: releaseSeatsLua,
	}
	qt := newBookingTestQueueToken(t)
	svc := NewBookingService(repo, rdb, scripts, qt, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected lock status")
}

func TestLockSeats_LuaEvalFailure(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	scripts := &LuaScripts{
		Lock:    `this is not valid lua`,
		Release: releaseSeatsLua,
	}
	qt := newBookingTestQueueToken(t)
	svc := NewBookingService(repo, rdb, scripts, qt, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "lock seats")
}

func TestLockSeats_LuaNonStringResultIsError(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	scripts := &LuaScripts{
		Lock:    `return 42`,
		Release: releaseSeatsLua,
	}
	qt := newBookingTestQueueToken(t)
	svc := NewBookingService(repo, rdb, scripts, qt, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected lock result type")
}

func TestLockSeats_BulkModeAccepted(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	tok := newValidToken(t, qt)
	resp, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 2}, PaymentMode: "BULK"}, tok)

	require.NoError(t, err)
	assert.Equal(t, "BULK", resp.PaymentMode)
}

func TestLockSeats_ExactlyMaxGroupSeatsAccepted(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 20)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	positions := make([]int, MaxGroupSeats)
	for i := range positions {
		positions[i] = i + 1
	}

	tok := newValidToken(t, qt)
	resp, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: positions, PaymentMode: "SPLIT"}, tok)

	require.NoError(t, err)
	assert.Equal(t, MaxGroupSeats, resp.TotalSeats)
}

// ============================================================
// Service: GetSeatAvailability — LOCKED status + fallbacks
// ============================================================

func TestGetSeatAvailability_LockedSeatsFromBitmap(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 5)}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	require.NoError(t, rdb.SetBit(ctx, bitmapKey(eid, sid), 1, 1).Err())

	resp, err := svc.GetSeatAvailability(ctx, eid, sid)

	require.NoError(t, err)
	assert.Equal(t, "LOCKED", resp.Seats[1].Status)
	assert.Equal(t, 4, resp.AvailableSeats)
}

func TestGetSeatAvailability_TakenTakesPrecedenceOverLocked(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx := context.Background()
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 5), taken: []int{2}}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	require.NoError(t, rdb.SetBit(ctx, bitmapKey(eid, sid), 1, 1).Err())

	resp, err := svc.GetSeatAvailability(ctx, eid, sid)

	require.NoError(t, err)
	assert.Equal(t, "TAKEN", resp.Seats[1].Status, "a paid ticket outranks an in-flight lock")
}

func TestGetSeatAvailability_BitmapFailureFallsBackToTicketsOnly(t *testing.T) {
	// FIX P9a-12 TD-F9-16: exclusively owned dead port (see deadRedisAddr).
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 5), taken: []int{1}}
	svc, _ := newTestService(t, repo, newDeadRedis(t), &fakePublisher{})

	resp, err := svc.GetSeatAvailability(context.Background(), eid, sid)

	require.NoError(t, err, "a Redis outage must not 500 the whole seat map")
	assert.Equal(t, "TAKEN", resp.Seats[0].Status)
	assert.Equal(t, 4, resp.AvailableSeats)
}

// ============================================================
// Handler: extra branches
// ============================================================

func TestHandler_GetSeatAvailability_InternalErrorReturns500(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	repo := &boomRepo{}
	h, _ := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodGet,
		"/events/"+uuid.NewString()+"/sections/"+uuid.NewString()+"/seats",
		uuid.NewString(), "", "")

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INTERNAL_ERROR"`)
}

func TestHandler_LockSeats_MissingIdempotencyKey(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	tok := newValidToken(t, qt)
	req := httptest.NewRequest(http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		bytesReader(`{"seat_positions":[1],"payment_mode":"SPLIT"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(testUserIDHeader, uuid.NewString())
	req.Header.Set(testQueueTokenHdr, tok)
	// deliberately no X-Idempotency-Key

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_LockSeats_IdempotentReplayReturnsCachedBody(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	tok := newValidToken(t, qt)
	idemKey := "idem-replay-" + uuid.NewString()
	userID := uuid.NewString()

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
			bytesReader(`{"seat_positions":[1],"payment_mode":"SPLIT"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(testUserIDHeader, userID)
		req.Header.Set(testQueueTokenHdr, tok)
		req.Header.Set("X-Idempotency-Key", idemKey)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	first := send()
	require.Equal(t, http.StatusCreated, first.Code)

	second := send()
	assert.Equal(t, http.StatusCreated, second.Code)
	assert.JSONEq(t, first.Body.String(), second.Body.String(),
		"a replayed idempotency key must return the identical cached body")
}

func TestHandler_LockSeats_DuplicateSeatPositionReturns400(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	tok := newValidToken(t, qt)
	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), tok, `{"seat_positions":[2,2],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_LockSeats_InvalidPaymentModeReturns400(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	tok := newValidToken(t, qt)
	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), tok, `{"seat_positions":[1],"payment_mode":"WIRE"}`)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"INVALID_REQUEST"`)
}

func TestHandler_LockSeats_SeatOutOfRangeReturns422(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 3)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	tok := newValidToken(t, qt)
	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), tok, `{"seat_positions":[9],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"UNPROCESSABLE_ENTITY"`)
}

func TestHandler_LockSeats_InvalidQueueTokenReturns401(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	h, _ := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	w := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), "garbage-token", `{"seat_positions":[1],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"UNAUTHORIZED"`)
}

func TestHandler_LockSeats_TokenAlreadyUsedReturns409(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	h, qt := newTestBookingHandler(t, repo, rdb, &fakePublisher{})
	router := buildBookingTestRouter(h)

	tok := newValidToken(t, qt)
	first := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), tok, `{"seat_positions":[1],"payment_mode":"SPLIT"}`)
	require.Equal(t, http.StatusCreated, first.Code)

	second := doBookingRequest(t, router, http.MethodPost,
		"/events/"+eid.String()+"/sections/"+sid.String()+"/seats",
		uuid.NewString(), tok, `{"seat_positions":[2],"payment_mode":"SPLIT"}`)

	assert.Equal(t, http.StatusConflict, second.Code)
	assert.Contains(t, second.Body.String(), `"code":"TOKEN_ALREADY_USED"`)
}

// ============================================================
// Lua script loader
// ============================================================

func TestLoadLuaScripts_RedisFailure(t *testing.T) {
	// FIX P9a-12 TD-F9-16: exclusively owned dead port (see deadRedisAddr).
	_, err := LoadLuaScripts(context.Background(), newDeadRedis(t))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "lua_scripts")
}

// ============================================================
// Helpers local to this file
// ============================================================

// deadRedisAddr returns the address of a TCP port that this test owns
// exclusively for its whole lifetime and that never speaks the Redis
// protocol: every accepted connection is closed immediately, so any client
// command fails (EOF) instead of hanging.
//
// FIX P9a-12 TD-F9-16: miniredis binds an OS-assigned ephemeral port and
// mr.Close() releases it back to the OS. During a full-suite run
// (go test ./internal/... executes packages concurrently) a miniredis started
// by another package can re-bind that exact port inside the window between
// mr.Close() and the assertion. The client then talks to the WRONG server,
// receives +PONG and the "server is down" assertion fails -> flaky test.
// Owning the port with our own listener removes the race: no other process
// can bind it while the test runs.
func deadRedisAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})

	return ln.Addr().String()
}

// newDeadRedis returns a client pointed at a server that is already gone.
// MaxRetries=-1 disables the go-redis retry ladder so the outage-path tests
// fail fast instead of burning seconds on backoff.
//
// FIX P9a-12 TD-F9-16: the target port is now owned exclusively by this test
// (see deadRedisAddr) instead of being a miniredis port that was closed and
// released back to the OS.
func newDeadRedis(t *testing.T) *redis.Client {
	t.Helper()
	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{
		Addr:        deadRedisAddr(t),
		MaxRetries:  -1,
		DialTimeout: 50 * time.Millisecond,
	})}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// boomRepo always fails GetSectionByID, to drive the handler's 500 branch.
type boomRepo struct{ fakeRepo }

func (b *boomRepo) GetSectionByID(_ context.Context, _ uuid.UUID) (*SectionRow, error) {
	return nil, errors.New("db exploded")
}

func bytesReader(s string) *bytes.Reader { return bytes.NewReader([]byte(s)) }
