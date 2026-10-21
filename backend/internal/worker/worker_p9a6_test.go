package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/config"
	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	redisclient "github.com/LittleMardiah/E-Tix/backend/internal/redis"
	"github.com/LittleMardiah/E-Tix/backend/internal/resale"
)

// ============================================================
// P9a-6 helpers
// ============================================================

// p9a6GoRedis returns a raw go-redis client backed by miniredis.
func p9a6GoRedis(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

// p9a6RedisClient returns an internal/redis.Client backed by miniredis.
func p9a6RedisClient(t *testing.T) (*redisclient.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := &redisclient.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

// p9a6Gateway is a configurable adapter.PaymentGateway fake.
type p9a6Gateway struct {
	initiateResp *adapter.InitiateResponse
	initiateErr  error
	refundResp   *adapter.RefundResponse
	refundErr    error
	payoutResp   *adapter.PayoutResponse
	payoutErr    error
	statusResp   *adapter.StatusResponse
	statusErr    error
	initiateSeen []adapter.InitiateRequest
	payoutSeen   []adapter.PayoutRequest
	refundSeen   []adapter.RefundRequest
}

func (g *p9a6Gateway) Initiate(_ context.Context, req adapter.InitiateRequest) (*adapter.InitiateResponse, error) {
	g.initiateSeen = append(g.initiateSeen, req)
	if g.initiateErr != nil {
		return nil, g.initiateErr
	}
	if g.initiateResp == nil {
		return &adapter.InitiateResponse{TransactionID: "tx-1", PaymentLink: "https://pay/1"}, nil
	}
	return g.initiateResp, nil
}

func (g *p9a6Gateway) Refund(_ context.Context, req adapter.RefundRequest) (*adapter.RefundResponse, error) {
	g.refundSeen = append(g.refundSeen, req)
	if g.refundErr != nil {
		return nil, g.refundErr
	}
	if g.refundResp == nil {
		return &adapter.RefundResponse{RefundID: "rf-1"}, nil
	}
	return g.refundResp, nil
}

func (g *p9a6Gateway) Payout(_ context.Context, req adapter.PayoutRequest) (*adapter.PayoutResponse, error) {
	g.payoutSeen = append(g.payoutSeen, req)
	if g.payoutErr != nil {
		return nil, g.payoutErr
	}
	if g.payoutResp == nil {
		return &adapter.PayoutResponse{PayoutID: "po-1"}, nil
	}
	return g.payoutResp, nil
}

func (g *p9a6Gateway) GetStatus(context.Context, string) (*adapter.StatusResponse, error) {
	if g.statusErr != nil {
		return nil, g.statusErr
	}
	return g.statusResp, nil
}

func (g *p9a6Gateway) ValidateWebhookSignature(map[string]string, []byte) error { return nil }

// p9a6ResaleRepo is a minimal resale.ResaleRepository fake.
type p9a6ResaleRepo struct {
	rt          *resale.ResaleTransaction
	getErr      error
	cleared     bool
	updatedStat []string
}

func (r *p9a6ResaleRepo) Create(context.Context, *resale.ResaleTransaction) error { return nil }

func (r *p9a6ResaleRepo) GetByID(context.Context, uuid.UUID) (*resale.ResaleTransaction, error) {
	return r.rt, r.getErr
}

func (r *p9a6ResaleRepo) GetByIDForUpdate(context.Context, pgx.Tx, uuid.UUID) (*resale.ResaleTransaction, error) {
	return r.rt, r.getErr
}

func (r *p9a6ResaleRepo) ListPending(context.Context, uuid.UUID) ([]resale.BrowseListing, error) {
	return nil, nil
}

func (r *p9a6ResaleRepo) UpdateBuyer(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}

func (r *p9a6ResaleRepo) ClearBuyer(context.Context, pgx.Tx, uuid.UUID) error {
	r.cleared = true
	return nil
}

func (r *p9a6ResaleRepo) UpdateStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, status string) error {
	r.updatedStat = append(r.updatedStat, status)
	return nil
}

func (r *p9a6ResaleRepo) HasActiveListing(context.Context, uuid.UUID) (bool, error) {
	return false, nil
}

func (r *p9a6ResaleRepo) SetPaymentTransactionID(context.Context, uuid.UUID, string) error {
	return nil
}

func (r *p9a6ResaleRepo) GetByPaymentTransactionID(context.Context, string) (*resale.ResaleTransaction, error) {
	return r.rt, r.getErr
}

func (r *p9a6ResaleRepo) TicketInfoForResale(context.Context, uuid.UUID) (*resale.TicketForResale, error) {
	return nil, nil
}

// ============================================================
// FanScoreWorker
// ============================================================

func TestP9a6_FanScoreWorker_ProcessMessage_PoisonJSON(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewFanScoreWorker(nil, client, nil, zap.NewNop())

	err := w.processMessage(context.Background(), []byte("not-json"))
	require.Error(t, err)
	assert.ErrorIs(t, err, errPoison)
}

func TestP9a6_FanScoreWorker_ProcessMessage_EmptyUserID(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewFanScoreWorker(nil, client, nil, zap.NewNop())

	err := w.processMessage(context.Background(), []byte(`{"user_id":"","final_score":80}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, errPoison)
}

func TestP9a6_FanScoreWorker_ProcessMessage_SetsDynamicScore(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewFanScoreWorker(nil, client, nil, zap.NewNop())
	userID := uuid.NewString()

	body, err := json.Marshal(queue.FanScoreMetadata{UserID: userID, FinalScore: 80})
	require.NoError(t, err)

	require.NoError(t, w.processMessage(context.Background(), body))

	val, err := client.GetString(context.Background(), "user:score:"+userID+":dynamic")
	require.NoError(t, err)
	assert.Equal(t, "0", val, "score >= 50 => dynamic score 0")
}

func TestP9a6_FanScoreWorker_ProcessMessage_LowScoreSetsNegativeDynamic(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewFanScoreWorker(nil, client, nil, zap.NewNop())
	userID := uuid.NewString()

	body, err := json.Marshal(queue.FanScoreMetadata{UserID: userID, FinalScore: 25})
	require.NoError(t, err)

	require.NoError(t, w.processMessage(context.Background(), body))

	val, err := client.GetString(context.Background(), "user:score:"+userID+":dynamic")
	require.NoError(t, err)
	assert.Equal(t, "-30", val)
}

func TestP9a6_FanScoreWorker_ProcessMessage_MidRangeSetsMinus20(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewFanScoreWorker(nil, client, nil, zap.NewNop())
	userID := uuid.NewString()

	body, err := json.Marshal(queue.FanScoreMetadata{UserID: userID, FinalScore: 40})
	require.NoError(t, err)

	require.NoError(t, w.processMessage(context.Background(), body))

	val, err := client.GetString(context.Background(), "user:score:"+userID+":dynamic")
	require.NoError(t, err)
	assert.Equal(t, "-20", val)
}

func TestP9a6_FanScoreWorker_Constructor(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewFanScoreWorker(nil, client, nil, zap.NewNop())
	require.NotNil(t, w)
}

func TestP9a6_CalculateDynamicScore_Boundaries(t *testing.T) {
	assert.Equal(t, -30, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 0}))
	assert.Equal(t, -30, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 29}))
	assert.Equal(t, -20, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 30}))
	assert.Equal(t, -20, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 49}))
	assert.Equal(t, 0, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 50}))
	assert.Equal(t, 0, calculateDynamicScore(queue.FanScoreMetadata{FinalScore: 100}))
}

func TestP9a6_DynamicScoreTTL_IsOneDay(t *testing.T) {
	assert.Equal(t, 24*time.Hour, dynamicScoreTTL)
}

func TestP9a6_PgxBlacklistRepo_Constructor(t *testing.T) {
	repo := NewPgxBlacklistRepo(nil)
	require.NotNil(t, repo)
}

// ============================================================
// BulkCancelWorker
// ============================================================

func TestP9a6_BulkCancelWorker_Handle_PoisonPayload_Acknowledged(t *testing.T) {
	client, _ := p9a6GoRedis(t)
	w := NewBulkCancelWorker(nil, client, nil, zap.NewNop())

	err := w.Handle(context.Background(), []byte("not-json"))
	require.NoError(t, err, "poison payload must be acknowledged, not requeued")
}

func TestP9a6_BulkCancelWorker_Constructor(t *testing.T) {
	client, _ := p9a6GoRedis(t)
	w := NewBulkCancelWorker(nil, client, nil, zap.NewNop())
	require.NotNil(t, w)
}

func TestP9a6_BulkCancelWorker_Run_ExitsOnContextCancel(t *testing.T) {
	client, _ := p9a6GoRedis(t)
	w := NewBulkCancelWorker(nil, client, nil, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	require.NoError(t, w.Run(ctx))
}

func TestP9a6_BulkCancelPayload_JSONRoundtrip(t *testing.T) {
	p := BulkCancelPayload{
		EventID: uuid.New(), AdminID: uuid.New(), Reason: "event cancelled",
	}

	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "event_id")
	assert.Contains(t, string(raw), "admin_id")
	assert.Contains(t, string(raw), `"reason":"event cancelled"`)

	var decoded BulkCancelPayload
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, p.EventID, decoded.EventID)
	assert.Equal(t, p.AdminID, decoded.AdminID)
	assert.Equal(t, p.Reason, decoded.Reason)
}

// ============================================================
// ResaleExpiryWorker
// ============================================================

func TestP9a6_ResaleExpiryWorker_Handle_PoisonPayload_Acknowledged(t *testing.T) {
	w := NewResaleExpiryWorker(nil, &p9a6ResaleRepo{}, zap.NewNop())

	err := w.Handle(context.Background(), []byte("not-json"))
	require.NoError(t, err)
}

func TestP9a6_ResaleExpiryWorker_Constructor(t *testing.T) {
	w := NewResaleExpiryWorker(nil, nil, zap.NewNop())
	require.NotNil(t, w)
}

func TestP9a6_ResaleExpiryWorker_Run_ExitsOnContextCancel(t *testing.T) {
	w := NewResaleExpiryWorker(nil, nil, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	require.NoError(t, w.Run(ctx))
}

// ============================================================
// PayoutWorker
// ============================================================

func TestP9a6_PayoutWorker_Handle_PoisonPayload_Acknowledged(t *testing.T) {
	w := NewPayoutWorker(nil, &p9a6ResaleRepo{}, &p9a6Gateway{}, zap.NewNop(), &config.Config{}, nil)

	err := w.Handle(context.Background(), []byte("not-json"))
	require.NoError(t, err)
}

func TestP9a6_PayoutWorker_Constructor(t *testing.T) {
	w := NewPayoutWorker(nil, nil, nil, zap.NewNop(), nil, nil)
	require.NotNil(t, w)
}

func TestP9a6_PayoutWorker_Run_WithoutMQConnection_ReturnsError(t *testing.T) {
	w := NewPayoutWorker(nil, nil, nil, zap.NewNop(), nil, nil)

	err := w.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mq connection not set")
}

// ============================================================
// FeedbackLoopWorker
// ============================================================

func TestP9a6_FeedbackLoopWorker_Constructor(t *testing.T) {
	w := NewFeedbackLoopWorker(nil, nil, zap.NewNop())
	require.NotNil(t, w)
}

func TestP9a6_FeedbackLoopWorker_Run_ReturnsContextError(t *testing.T) {
	w := NewFeedbackLoopWorker(nil, nil, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Run(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ============================================================
// QRRegenerationWorker
// ============================================================

func TestP9a6_QRRegenerationWorker_Constructor_DefaultInterval(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewQRRegenerationWorker(nil, client, zap.NewNop(), 0)

	require.NotNil(t, w)
	assert.Equal(t, qrRegenIntervalSeconds*time.Second, w.interval)
}

func TestP9a6_QRRegenerationWorker_Constructor_CustomInterval(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewQRRegenerationWorker(nil, client, zap.NewNop(), 90*time.Second)

	assert.Equal(t, 90*time.Second, w.interval)
}

func TestP9a6_QRRegenerationWorker_Run_ReturnsContextError(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewQRRegenerationWorker(nil, client, zap.NewNop(), 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Run(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestP9a6_QRRegenerationConstants(t *testing.T) {
	assert.Equal(t, 30, qrRegenIntervalSeconds)
	assert.Equal(t, 35, qrRegenCacheTTLSeconds)
	assert.Equal(t, 5000, qrRegenBatchSize)
}

// ============================================================
// TicketNotificationWorker
// ============================================================

func TestP9a6_TicketNotificationWorker_Constructor_DefaultInterval(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewTicketNotificationWorker(nil, nil, client, zap.NewNop(), 0)

	require.NotNil(t, w)
	assert.Equal(t, DefaultTicketNotifyPollSeconds*time.Second, w.interval)
}

func TestP9a6_TicketNotificationWorker_Constructor_CustomInterval(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewTicketNotificationWorker(nil, nil, client, zap.NewNop(), 45*time.Second)

	assert.Equal(t, 45*time.Second, w.interval)
}

func TestP9a6_TicketNotificationWorker_Run_ReturnsContextError(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewTicketNotificationWorker(nil, nil, client, zap.NewNop(), 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := w.Run(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestP9a6_TicketNotifyConstants(t *testing.T) {
	assert.Equal(t, "ticket_notify:last_seen", ticketNotifyLastSeenKey)
	assert.Equal(t, 100, ticketNotifyBatchSize)
	assert.Equal(t, 10, DefaultTicketNotifyPollSeconds)
}

// ============================================================
// Adapters + repo constructors
// ============================================================

func TestP9a6_NewGatewayRefundAdapter_Constructor(t *testing.T) {
	refund := NewGatewayRefundAdapter(&p9a6Gateway{})
	require.NotNil(t, refund)
}

func TestP9a6_NewGatewayStatusFetcher_Constructor(t *testing.T) {
	fetcher := NewGatewayStatusFetcher(&p9a6Gateway{})
	require.NotNil(t, fetcher)
}

func TestP9a6_NewPgxBookingExpiryRepo_Constructor(t *testing.T) {
	repo := NewPgxBookingExpiryRepo(nil)
	require.NotNil(t, repo)
}

func TestP9a6_NewPgxReconciliationRepo_Constructor(t *testing.T) {
	repo := NewPgxReconciliationRepo(nil)
	require.NotNil(t, repo)
}

// ============================================================
// Payload models
// ============================================================

func TestP9a6_BookingExpiryPayload_JSONRoundtrip(t *testing.T) {
	p := BookingExpiryPayload{Type: "release_seat_check", BookingID: uuid.NewString()}

	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "booking_id")
	assert.Contains(t, string(raw), "release_seat_check")

	var decoded BookingExpiryPayload
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, p.Type, decoded.Type)
	assert.Equal(t, p.BookingID, decoded.BookingID)
}

func TestP9a6_BookingExpiryPayload_EmptyBookingID(t *testing.T) {
	p := BookingExpiryPayload{Type: "release_seat_check"}
	assert.Empty(t, p.BookingID)
}

func TestP9a6_ErrPoison_SentinelMessage(t *testing.T) {
	assert.Contains(t, errPoison.Error(), "poison")
}

func TestP9a6_GatewayFake_ImplementsPaymentGateway(t *testing.T) {
	var gw adapter.PaymentGateway = &p9a6Gateway{}
	require.NotNil(t, gw)
}

func TestP9a6_GatewayFake_PayoutRecordsRecipient(t *testing.T) {
	gw := &p9a6Gateway{}
	resp, err := gw.Payout(context.Background(), adapter.PayoutRequest{
		RecipientID: uuid.NewString(), Amount: "1000.00",
	})
	require.NoError(t, err)
	assert.Equal(t, "po-1", resp.PayoutID)
	require.Len(t, gw.payoutSeen, 1)
	assert.Equal(t, "1000.00", gw.payoutSeen[0].Amount)
}

func TestP9a6_GatewayFake_RefundRecordsReason(t *testing.T) {
	gw := &p9a6Gateway{}
	_, err := gw.Refund(context.Background(), adapter.RefundRequest{
		ExternalTransactionID: "tx-9", Amount: "500.00", Reason: "event cancelled",
	})
	require.NoError(t, err)
	require.Len(t, gw.refundSeen, 1)
	assert.Equal(t, "tx-9", gw.refundSeen[0].ExternalTransactionID)
	assert.Equal(t, "event cancelled", gw.refundSeen[0].Reason)
}

func TestP9a6_GatewayFake_ErrorsArePropagated(t *testing.T) {
	sentinel := errors.New("provider down")
	gw := &p9a6Gateway{
		initiateErr: sentinel,
		refundErr:   sentinel,
		payoutErr:   sentinel,
		statusErr:   sentinel,
	}

	_, err := gw.Initiate(context.Background(), adapter.InitiateRequest{})
	require.ErrorIs(t, err, sentinel)
	_, err = gw.Refund(context.Background(), adapter.RefundRequest{})
	require.ErrorIs(t, err, sentinel)
	_, err = gw.Payout(context.Background(), adapter.PayoutRequest{})
	require.ErrorIs(t, err, sentinel)
	_, err = gw.GetStatus(context.Background(), "tx")
	require.ErrorIs(t, err, sentinel)
}

func TestP9a6_ResaleRepoFake_ImplementsInterface(t *testing.T) {
	var repo resale.ResaleRepository = &p9a6ResaleRepo{}
	require.NotNil(t, repo)
}
