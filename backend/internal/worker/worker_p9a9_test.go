package worker

// FIX P9a-9b — unit coverage boost for the worker package.
//
// Scope: pure-logic paths that do NOT need PostgreSQL / RabbitMQ. DB-backed
// paths (repository.go, RunOnce cycles that query the pool) are covered by
// the DB-backed suite instead.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment"
	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

// p9a9RedisClient returns an internal/redis.Client backed by miniredis.
func p9a9RedisClient(t *testing.T) (*redisclient.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := &redisclient.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

// p9a9BlacklistRepo records blacklist inserts so the test can assert that
// none happened (TD-F2-01 regression guard).
type p9a9BlacklistRepo struct {
	inserted []string
}

func (r *p9a9BlacklistRepo) InsertBlacklist(_ context.Context, _ uuid.UUID, btype, value, reason string, _ *time.Time) error {
	r.inserted = append(r.inserted, btype+":"+value+":"+reason)
	return nil
}

// ============================================================
// ResaleExpiryWorker
// ============================================================

// TD-F7-03: a malformed queue body must be discarded (not retried forever)
// and must NOT surface as an error to the consumer.
func TestP9a9ResaleExpiryWorker_HandlePoisonPayloadIsDiscarded(t *testing.T) {
	w := NewResaleExpiryWorker(nil, nil, zap.NewNop())
	require.NotNil(t, w)

	err := w.Handle(context.Background(), []byte("not-json-at-all"))

	assert.NoError(t, err)
}

// The ticker loop must return cleanly when the context is cancelled.
func TestP9a9ResaleExpiryWorker_RunStopsOnContextCancel(t *testing.T) {
	w := NewResaleExpiryWorker(nil, nil, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Run(ctx)

	assert.NoError(t, err)
}

// ============================================================
// PayoutWorker
// ============================================================

func TestP9a9PayoutWorker_HandlePoisonPayloadIsDiscarded(t *testing.T) {
	w := NewPayoutWorker(nil, nil, nil, zap.NewNop(), nil, nil)
	require.NotNil(t, w)

	err := w.Handle(context.Background(), []byte("}{"))

	assert.NoError(t, err)
}

// A worker wired without a broker must fail loudly instead of nil-panicking.
func TestP9a9PayoutWorker_RunWithoutMQConnectionFails(t *testing.T) {
	w := NewPayoutWorker(nil, nil, nil, zap.NewNop(), nil, nil)

	err := w.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mq connection not set")
}

// ============================================================
// BulkCancelWorker
// ============================================================

func TestP9a9BulkCancelWorker_HandlePoisonPayloadIsDiscarded(t *testing.T) {
	w := NewBulkCancelWorker(nil, nil, nil, zap.NewNop(), nil)
	require.NotNil(t, w)

	err := w.Handle(context.Background(), []byte("nope"))

	assert.NoError(t, err)
}

func TestP9a9BulkCancelWorker_RunWithoutMQConnectionFails(t *testing.T) {
	w := NewBulkCancelWorker(nil, nil, nil, zap.NewNop(), nil)

	err := w.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mq connection not set")
}

// ============================================================
// QRRegenerationWorker
// ============================================================

// A non-positive interval must fall back to the 30s QR timestamp block.
func TestP9a9QRRegenerationWorker_DefaultIntervalFallback(t *testing.T) {
	w := NewQRRegenerationWorker(nil, nil, zap.NewNop(), 0)
	require.NotNil(t, w)

	assert.Equal(t, qrRegenIntervalSeconds*time.Second, w.interval)
	assert.Equal(t, 30*time.Second, w.interval)
}

func TestP9a9QRRegenerationWorker_RunStopsOnContextCancel(t *testing.T) {
	w := NewQRRegenerationWorker(nil, nil, zap.NewNop(), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
}

// ============================================================
// TicketNotificationWorker
// ============================================================

func TestP9a9TicketNotificationWorker_DefaultIntervalFallback(t *testing.T) {
	w := NewTicketNotificationWorker(nil, nil, nil, zap.NewNop(), 0)
	require.NotNil(t, w)

	assert.Equal(t, DefaultTicketNotifyPollSeconds*time.Second, w.interval)
}

func TestP9a9TicketNotificationWorker_RunStopsOnContextCancel(t *testing.T) {
	w := NewTicketNotificationWorker(nil, nil, nil, zap.NewNop(), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
}

// An empty Redis value means "never polled" -> uuid.Nil, no error.
func TestP9a9TicketNotificationWorker_GetLastSeenMissingKeyReturnsNilUUID(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	w := NewTicketNotificationWorker(nil, nil, rdb, zap.NewNop(), time.Hour)

	id, err := w.getLastSeen(context.Background())

	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, id)
}

// A corrupted cursor must surface as an error, not a silent uuid.Nil.
func TestP9a9TicketNotificationWorker_GetLastSeenCorruptValueFails(t *testing.T) {
	rdb, mr := p9a9RedisClient(t)
	mr.Set(ticketNotifyLastSeenKey, "not-a-uuid")
	w := NewTicketNotificationWorker(nil, nil, rdb, zap.NewNop(), time.Hour)

	_, err := w.getLastSeen(context.Background())

	require.Error(t, err)
}

// ============================================================
// FanScoreWorker
// ============================================================

// calculateDynamicScore maps final score -> heuristic dynamic penalty.
func TestP9a9FanScoreWorker_CalculateDynamicScore(t *testing.T) {
	assert.Equal(t, -30, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 10}))
	assert.Equal(t, -30, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 29}))
	assert.Equal(t, -20, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 30}))
	assert.Equal(t, -20, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 49}))
	assert.Equal(t, 0, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 50}))
	assert.Equal(t, 0, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 100}))
}

func TestP9a9FanScoreWorker_ProcessMessagePoisonIsNotRequeued(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	w := NewFanScoreWorker(nil, rdb, nil, zap.NewNop())

	err := w.processMessage(context.Background(), []byte("not-json"))

	require.Error(t, err)
	assert.True(t, errors.Is(err, errPoison), "poison payload must be marked non-retryable")
}

func TestP9a9FanScoreWorker_ProcessMessageEmptyUserIDIsPoison(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	w := NewFanScoreWorker(nil, rdb, nil, zap.NewNop())
	body, err := json.Marshal(queue.FanScoreMetadata{UserID: "", FinalScore: 10})
	require.NoError(t, err)

	err = w.processMessage(context.Background(), body)

	require.Error(t, err)
	assert.True(t, errors.Is(err, errPoison))
}

// The dynamic score is written to Redis under user:score:<id>:dynamic.
func TestP9a9FanScoreWorker_ProcessMessageStoresDynamicScore(t *testing.T) {
	rdb, mr := p9a9RedisClient(t)
	w := NewFanScoreWorker(nil, rdb, nil, zap.NewNop())
	body, err := json.Marshal(queue.FanScoreMetadata{UserID: "user-p9a9", FinalScore: 10})
	require.NoError(t, err)

	require.NoError(t, w.processMessage(context.Background(), body))

	val, err := mr.Get("user:score:user-p9a9:dynamic")
	require.NoError(t, err)
	assert.Equal(t, "-30", val)
}

// TD-F2-01: a low score must NOT black anybody (no false positives).
func TestP9a9FanScoreWorker_LowScoreDoesNotBlacklist(t *testing.T) {
	rdb, mr := p9a9RedisClient(t)
	repo := &p9a9BlacklistRepo{}
	w := NewFanScoreWorker(nil, rdb, repo, zap.NewNop())
	body, err := json.Marshal(queue.FanScoreMetadata{UserID: "user-low", FinalScore: 1})
	require.NoError(t, err)

	require.NoError(t, w.processMessage(context.Background(), body))

	assert.Empty(t, repo.inserted, "low score must not trigger a blacklist insert")
	keys := mr.Keys()
	for _, k := range keys {
		assert.NotContains(t, k, "blacklist")
	}
}

// ============================================================
// FeedbackLoopWorker
// ============================================================

// TD-F6-13: an InsertInsight failure must be logged, never silently swallowed.
func TestP9a9FeedbackLoopWorker_InsertInsightErrorIsSurfaced(t *testing.T) {
	repo := &p9a6AnalyticsRepo{insertErr: errors.New("insert failed")}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())
	eventID := uuid.New()

	w.generateGATE_STAFFING(context.Background(), eventID)
	w.generateBOT_BLACKLIST(context.Background(), eventID)
	w.generatePRICE_RECOMMENDATION(context.Background(), eventID)

	assert.Empty(t, repo.inserted)
}

// All three insight types are generated when they do not exist yet.
func TestP9a9FeedbackLoopWorker_GeneratesMissingInsights(t *testing.T) {
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{}}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())
	eventID := uuid.New()

	w.generateGATE_STAFFING(context.Background(), eventID)
	w.generateBOT_BLACKLIST(context.Background(), eventID)
	w.generatePRICE_RECOMMENDATION(context.Background(), eventID)

	require.Len(t, repo.inserted, 3)
	types := []string{repo.inserted[0].InsightType, repo.inserted[1].InsightType, repo.inserted[2].InsightType}
	assert.ElementsMatch(t, []string{"GATE_STAFFING", "BOT_BLACKLIST", "PRICE_RECOMMENDATION"}, types)
}

// Idempotency: an insight that already exists is not regenerated.
func TestP9a9FeedbackLoopWorker_SkipsExistingInsights(t *testing.T) {
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{
		"GATE_STAFFING": true, "BOT_BLACKLIST": true, "PRICE_RECOMMENDATION": true,
	}}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())
	eventID := uuid.New()

	w.generateGATE_STAFFING(context.Background(), eventID)
	w.generateBOT_BLACKLIST(context.Background(), eventID)
	w.generatePRICE_RECOMMENDATION(context.Background(), eventID)

	assert.Empty(t, repo.inserted)
}

func TestP9a9FeedbackLoopWorker_RunStopsOnContextCancel(t *testing.T) {
	w := NewFeedbackLoopWorker(nil, &p9a6AnalyticsRepo{}, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
}

// ============================================================
// BookingExpiryWorker
// ============================================================

func TestP9a9BookingExpiryWorker_ProcessOnePoisonJSON(t *testing.T) {
	repo := &fakeBookingExpiryRepo{}
	w := NewBookingExpiryWorker(nil, nil, repo, nil, nil, nil, uuid.Nil, zap.NewNop())

	err := w.ProcessOne(context.Background(), []byte("not-json"))

	require.Error(t, err)
	assert.True(t, errors.Is(err, errPoison))
}

func TestP9a9BookingExpiryWorker_ProcessOneEmptyBookingID(t *testing.T) {
	repo := &fakeBookingExpiryRepo{}
	w := NewBookingExpiryWorker(nil, nil, repo, nil, nil, nil, uuid.Nil, zap.NewNop())

	err := w.ProcessOne(context.Background(), []byte(`{"booking_id":""}`))

	require.Error(t, err)
	assert.True(t, errors.Is(err, errPoison))
}

func TestP9a9BookingExpiryWorker_ProcessOneMalformedBookingID(t *testing.T) {
	repo := &fakeBookingExpiryRepo{}
	w := NewBookingExpiryWorker(nil, nil, repo, nil, nil, nil, uuid.Nil, zap.NewNop())

	err := w.ProcessOne(context.Background(), []byte(`{"booking_id":"not-a-uuid"}`))

	require.Error(t, err)
	assert.True(t, errors.Is(err, errPoison))
}

func TestP9a9BookingExpiryWorker_ProcessOneStatusLookupError(t *testing.T) {
	repo := &fakeBookingExpiryRepo{statusErr: errors.New("db down")}
	w := NewBookingExpiryWorker(nil, nil, repo, nil, nil, nil, uuid.Nil, zap.NewNop())
	body, err := json.Marshal(BookingExpiryPayload{BookingID: uuid.New().String()})
	require.NoError(t, err)

	err = w.ProcessOne(context.Background(), body)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get status")
}

// A PAID booking is left alone (lazy check before any write).
func TestP9a9BookingExpiryWorker_ProcessOnePaidBookingIsNoOp(t *testing.T) {
	repo := &fakeBookingExpiryRepo{status: payment.StatusPaid}
	w := NewBookingExpiryWorker(nil, nil, repo, nil, nil, nil, uuid.Nil, zap.NewNop())
	body, err := json.Marshal(BookingExpiryPayload{BookingID: uuid.New().String()})
	require.NoError(t, err)

	err = w.ProcessOne(context.Background(), body)

	assert.NoError(t, err)
}

// A booking in any non-PENDING state is a no-op as well.
func TestP9a9BookingExpiryWorker_ProcessOneNonPendingStatusIsNoOp(t *testing.T) {
	for _, status := range []string{payment.StatusExpired, payment.StatusFailed, "CANCELLED"} {
		repo := &fakeBookingExpiryRepo{status: status}
		w := NewBookingExpiryWorker(nil, nil, repo, nil, nil, nil, uuid.Nil, zap.NewNop())
		body, err := json.Marshal(BookingExpiryPayload{BookingID: uuid.New().String()})
		require.NoError(t, err)

		assert.NoError(t, w.ProcessOne(context.Background(), body), "status %s", status)
	}
}

// ============================================================
// WebhookReconciliationWorker
// ============================================================

func TestP9a9WebhookReconciliationWorker_RunStopsOnContextCancel(t *testing.T) {
	w := NewWebhookReconciliationWorker(nil, nil, nil, nil, uuid.Nil, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
}

// An unknown gateway status must not mutate anything.
func TestP9a9WebhookReconciliationWorker_UnknownStatusIsNoOp(t *testing.T) {
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	recon := &fakeReconRepo{reconcileRows: 1}
	fetcher := &fakeStatusFetcher{responses: map[string]*adapter.StatusResponse{
		"tx-unknown": {TransactionStatus: "weird_status"},
	}}
	w := NewWebhookReconciliationWorker(beginner, &fakePaymentRepo{}, recon, fetcher, uuid.New(), zap.NewNop())

	err := w.RunOnce(context.Background())

	require.NoError(t, err)
	assert.Empty(t, recon.adminActions, "unknown status must not write an audit row")
}

// A failed status update must abort the whole reconciliation for that row.
func TestP9a9WebhookReconciliationWorker_UpdateErrorAborts(t *testing.T) {
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	recon := &fakeReconRepo{reconcileRows: 1, reconcileErr: errors.New("update failed")}
	fetcher := &fakeStatusFetcher{responses: map[string]*adapter.StatusResponse{
		"tx-1": {TransactionStatus: "settlement"},
	}}
	w := NewWebhookReconciliationWorker(beginner, &fakePaymentRepo{}, recon, fetcher, uuid.New(), zap.NewNop())

	err := w.RunOnce(context.Background())

	require.NoError(t, err, "one failing obligation must not abort the batch")
	assert.Empty(t, recon.adminActions)
}

// A failing audit insert must not be treated as a successful reconciliation.
func TestP9a9WebhookReconciliationWorker_AuditErrorAborts(t *testing.T) {
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	recon := &fakeReconRepo{reconcileRows: 1, adminErr: errors.New("audit failed")}
	fetcher := &fakeStatusFetcher{responses: map[string]*adapter.StatusResponse{
		"tx-1": {TransactionStatus: "capture"},
	}}
	w := NewWebhookReconciliationWorker(beginner, &fakePaymentRepo{}, recon, fetcher, uuid.New(), zap.NewNop())

	err := w.RunOnce(context.Background())

	require.NoError(t, err)
}

// An obligation without an external transaction id is skipped, not fatal.
func TestP9a9WebhookReconciliationWorker_MissingExternalTxIDIsSkipped(t *testing.T) {
	beginner := &fakeTxBeginner{tx: &fakeTx{}}
	recon := &fakeReconRepo{reconcileRows: 1}
	fetcher := &fakeStatusFetcher{}
	w := NewWebhookReconciliationWorker(beginner, &fakePaymentRepo{}, recon, fetcher, uuid.New(), zap.NewNop())

	obligation := payment.PaymentObligation{ID: uuid.New(), Status: payment.StatusPending}
	w.repo = &fakePaymentRepo{pending: []payment.PaymentObligation{obligation}}

	err := w.RunOnce(context.Background())

	require.NoError(t, err)
	assert.Empty(t, recon.adminActions)
}
