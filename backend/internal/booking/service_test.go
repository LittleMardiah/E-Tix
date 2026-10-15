package booking

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// ============================================================
// Helpers
// ============================================================

func newBookingTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

func newBookingTestQueueToken(t *testing.T) *queue.QueueTokenService {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	dir := t.TempDir()

	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	privPath := filepath.Join(dir, "priv.pem")
	require.NoError(t, os.WriteFile(privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600))

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPath := filepath.Join(dir, "pub.pem")
	require.NoError(t, os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o600))

	svc, err := queue.NewQueueTokenService(privPath, pubPath, "etix-booking-test")
	require.NoError(t, err)
	return svc
}

// fakeRepo implements BookingRepository.
type fakeRepo struct {
	section        *SectionRow
	taken          []int
	createErr      error
	createdBooking *Booking
}

func (f *fakeRepo) GetSectionByID(_ context.Context, _ uuid.UUID) (*SectionRow, error) {
	if f.section == nil {
		return nil, ErrSectionNotFound
	}
	return f.section, nil
}

func (f *fakeRepo) GetTakenSeats(_ context.Context, _ uuid.UUID) ([]int, error) {
	return f.taken, nil
}

func (f *fakeRepo) CreateBooking(_ context.Context, b *Booking) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.createdBooking = b
	return nil
}

func (f *fakeRepo) GetByID(_ context.Context, _ uuid.UUID) (*Booking, error) {
	return nil, ErrBookingNotFound
}

func (f *fakeRepo) GetByIDForUpdate(_ context.Context, _ pgx.Tx, _ uuid.UUID) (*Booking, error) {
	return nil, ErrBookingNotFound
}

func (f *fakeRepo) GetEventStartAt(_ context.Context, _ uuid.UUID) (time.Time, error) {
	return time.Now().UTC().Add(24 * time.Hour), nil
}

// fakePublisher implements DelayedPublisher.
type fakePublisher struct {
	calls []publishCall
	err   error
}

type publishCall struct {
	queue string
	delay time.Duration
}

func (f *fakePublisher) PublishDelayed(_ context.Context, queueName string, _ []byte, delay time.Duration) error {
	f.calls = append(f.calls, publishCall{queue: queueName, delay: delay})
	return f.err
}

func newTestService(t *testing.T, repo BookingRepository, rdb *redis.Client, pub DelayedPublisher) (*BookingService, *queue.QueueTokenService) {
	t.Helper()
	scripts := &LuaScripts{Lock: lockSeatsLua, Release: releaseSeatsLua}
	qt := newBookingTestQueueToken(t)
	svc := NewBookingService(repo, rdb, scripts, qt, pub)
	return svc, qt
}

func newValidToken(t *testing.T, qt *queue.QueueTokenService) string {
	t.Helper()
	tok, err := qt.Generate(uuid.New(), "TIER_2", 80)
	require.NoError(t, err)
	return tok
}

func newSection(eventID, sectionID uuid.UUID, totalSeats int) *SectionRow {
	return &SectionRow{
		ID:            sectionID,
		EventID:       eventID,
		Name:          "VIP",
		Price:         "1000000",
		TotalSeats:    totalSeats,
		LayoutVersion: 1,
	}
}

func bitmapKey(eventID, sectionID uuid.UUID) string {
	return "seats:" + eventID.String() + ":" + sectionID.String() + ":v1"
}

// ============================================================
// Tests — UT-BOOK-001..007
// ============================================================

func TestLockSeats_AllAvailable_ReturnsSuccess(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	pub := &fakePublisher{}
	svc, qt := newTestService(t, repo, rdb, pub)

	tok := newValidToken(t, qt)
	resp, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 2, 3}, PaymentMode: "SPLIT"}, tok)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 3, resp.TotalSeats)
	assert.Equal(t, "3000000", resp.TotalAmount)
	assert.Equal(t, "PENDING", resp.PaymentStatus)
	assert.Equal(t, "SPLIT", resp.PaymentMode)
	assert.NotEmpty(t, resp.BookingID)
	assert.NotEmpty(t, resp.ExpiresAt)

	require.NotNil(t, repo.createdBooking)
	assert.Equal(t, resp.BookingID, repo.createdBooking.ID.String())

	require.Len(t, pub.calls, 1)
	assert.Equal(t, "release_seat_check", pub.calls[0].queue)
	assert.Equal(t, 180*time.Second, pub.calls[0].delay)
}

// UT-BOOK-002: pre-lock seat 2, then attempt lock [1,2,3] → CONFLICT.
func TestLockSeats_Conflict_ReturnsErrSeatsNotAvailable(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	ctx := context.Background()
	// Seat 2 → 0-based offset 1
	rdb.SetBit(ctx, bitmapKey(eid, sid), 1, 1)

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(ctx, uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 2, 3}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSeatsNotAvailable)

	// Verify rollback: no bit should have been flipped from 0 to 1 (Lua checks first)
	b0, _ := rdb.GetBit(ctx, bitmapKey(eid, sid), 0).Result()
	assert.Equal(t, int64(0), b0, "seat 1 should remain unlocked after conflict")
}

// UT-BOOK-003: verify Lua uses 0-based offset (seat 1 → bit 0, seat 5 → bit 4).
func TestLockSeats_LuaScript_0BasedOffset(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	ctx := context.Background()
	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(ctx, uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 5}, PaymentMode: "SPLIT"}, tok)
	require.NoError(t, err)

	key := bitmapKey(eid, sid)
	b0, _ := rdb.GetBit(ctx, key, 0).Result()
	assert.Equal(t, int64(1), b0, "seat 1 must set bit at offset 0")
	b4, _ := rdb.GetBit(ctx, key, 4).Result()
	assert.Equal(t, int64(1), b4, "seat 5 must set bit at offset 4")
	b1, _ := rdb.GetBit(ctx, key, 1).Result()
	assert.Equal(t, int64(0), b1, "offset 1 (seat 2) must be untouched")
}

// UT-BOOK-004: DB insert fail → LockSeats must release bits granularly,
// preserving unrelated pre-locked bits.
func TestLockSeats_DBFail_ReleasesGranularBits(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{
		section:   newSection(eid, sid, 10),
		createErr: errors.New("simulated db failure"),
	}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	ctx := context.Background()
	key := bitmapKey(eid, sid)
	// Pre-lock seat 5 (offset 4) to verify no-overwrite.
	rdb.SetBit(ctx, key, 4, 1)

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(ctx, uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 2}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)

	b0, _ := rdb.GetBit(ctx, key, 0).Result()
	b1, _ := rdb.GetBit(ctx, key, 1).Result()
	b4, _ := rdb.GetBit(ctx, key, 4).Result()
	assert.Equal(t, int64(0), b0, "seat 1 must be released on DB fail")
	assert.Equal(t, int64(0), b1, "seat 2 must be released on DB fail")
	assert.Equal(t, int64(1), b4, "unrelated pre-locked seat 5 must be preserved")
}

// UT-BOOK-005: 11 seats exceeds MaxGroupSeats.
func TestLockSeats_11Seats_ReturnsMaxGroupSeatsExceeded(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 20)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMaxGroupSeatsExceeded)
}

// UT-BOOK-006: same queue token reused → second call fails TOKEN_ALREADY_USED.
func TestLockSeats_TokenSingleUse_SecondUseReturnsErrTokenAlreadyUsed(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	ctx := context.Background()
	tok := newValidToken(t, qt)

	_, err := svc.LockSeats(ctx, uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 2}, PaymentMode: "SPLIT"}, tok)
	require.NoError(t, err)

	_, err = svc.LockSeats(ctx, uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{3, 4}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenAlreadyUsed)
}

// UT-BOOK-007: path eventID != section.EventID → ErrSectionNotFound.
func TestLockSeats_EventMismatch_ReturnsErrSectionNotFound(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	wrongEid := uuid.New()
	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), wrongEid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSectionNotFound)
}

// ============================================================
// Extra — input validation
// ============================================================

func TestLockSeats_EmptySeats_ReturnsErrEmptySeatPositions(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptySeatPositions)
}

func TestLockSeats_InvalidPaymentMode_ReturnsErrInvalidPaymentMode(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1}, PaymentMode: "WIRE"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPaymentMode)
}

func TestLockSeats_SeatOutOfRange_ReturnsErrSeatOutOfRange(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 5)}
	svc, qt := newTestService(t, repo, rdb, &fakePublisher{})

	tok := newValidToken(t, qt)
	_, err := svc.LockSeats(context.Background(), uuid.New(), eid, sid,
		&LockSeatsRequest{SeatPositions: []int{1, 6}, PaymentMode: "SPLIT"}, tok)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSeatOutOfRange)
}

// ============================================================
// Service: GetSeatAvailability
// ============================================================

func TestGetSeatAvailability_Success(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10), taken: []int{1, 3}}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	resp, err := svc.GetSeatAvailability(context.Background(), eid, sid)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, eid.String(), resp.EventID)
	assert.Equal(t, sid.String(), resp.SectionID)
	assert.Equal(t, "VIP", resp.SectionName)
	assert.Equal(t, 1, resp.LayoutVersion)
	assert.Equal(t, 10, resp.TotalSeats)
	assert.Equal(t, 8, resp.AvailableSeats)
	require.Len(t, resp.Seats, 10)

	assert.Equal(t, "TAKEN", resp.Seats[0].Status)
	assert.Equal(t, "AVAILABLE", resp.Seats[1].Status)
	assert.Equal(t, "TAKEN", resp.Seats[2].Status)
	assert.NotEmpty(t, resp.LastUpdatedAt)
}

func TestGetSeatAvailability_EventMismatch(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	eid, sid := uuid.New(), uuid.New()
	repo := &fakeRepo{section: newSection(eid, sid, 10)}
	svc, _ := newTestService(t, repo, rdb, &fakePublisher{})

	wrongEid := uuid.New()
	_, err := svc.GetSeatAvailability(context.Background(), wrongEid, sid)
	require.Error(t, err)
}
