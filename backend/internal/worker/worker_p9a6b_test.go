package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/analytics"
	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// ============================================================
// P9a-6 helpers (part 2)
// ============================================================

// p9a6AnalyticsRepo is a no-op analytics.AnalyticsRepository fake that
// records insight inserts.
type p9a6AnalyticsRepo struct {
	existing  map[string]bool
	inserted  []*analytics.InsightRecord
	insertErr error
}

func (r *p9a6AnalyticsRepo) SalesAndGateSummary(context.Context, uuid.UUID) (*analytics.SalesBlock, error) {
	return &analytics.SalesBlock{}, nil
}

func (r *p9a6AnalyticsRepo) GateStatusToday(context.Context, uuid.UUID) (map[string]string, int, error) {
	return map[string]string{}, 0, nil
}

func (r *p9a6AnalyticsRepo) TotalScansToday(context.Context, uuid.UUID) (int, error) { return 0, nil }

func (r *p9a6AnalyticsRepo) AntiBotStats(context.Context, uuid.UUID) (*analytics.AntiBotBlock, error) {
	return &analytics.AntiBotBlock{}, nil
}

func (r *p9a6AnalyticsRepo) RecentActivity(context.Context, uuid.UUID, int) ([]analytics.ActivityItem, error) {
	return nil, nil
}

func (r *p9a6AnalyticsRepo) AttendanceMetrics(context.Context, uuid.UUID) (*analytics.ReportAttendance, error) {
	return &analytics.ReportAttendance{}, nil
}

func (r *p9a6AnalyticsRepo) GateUtilization(context.Context, uuid.UUID) (map[string]analytics.GateUtilization, error) {
	return map[string]analytics.GateUtilization{}, nil
}

func (r *p9a6AnalyticsRepo) ScanTimeline(context.Context, uuid.UUID) ([]analytics.ScanTimelineBucket, error) {
	return nil, nil
}

func (r *p9a6AnalyticsRepo) LoyaltySplit(context.Context, uuid.UUID) (int, int, error) {
	return 0, 0, nil
}

func (r *p9a6AnalyticsRepo) ReportGatePerformance(context.Context, uuid.UUID) (map[string]analytics.ReportGatePerformance, error) {
	return map[string]analytics.ReportGatePerformance{}, nil
}

func (r *p9a6AnalyticsRepo) InsertInsight(_ context.Context, in *analytics.InsightRecord) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	r.inserted = append(r.inserted, in)
	return nil
}

func (r *p9a6AnalyticsRepo) ExistsByEventAndType(_ context.Context, _ uuid.UUID, insightType string) (bool, error) {
	return r.existing[insightType], nil
}

func (r *p9a6AnalyticsRepo) ListByEvent(context.Context, uuid.UUID) ([]analytics.InsightRecord, error) {
	return nil, nil
}

// ============================================================
// refund_adapter / status_fetcher
// ============================================================

func TestP9a6_GatewayRefundAdapter_Refund_Success(t *testing.T) {
	gw := &p9a6Gateway{}
	refund := NewGatewayRefundAdapter(gw)

	id, err := refund.Refund(context.Background(), "tx-1", "1000.00")
	require.NoError(t, err)
	assert.Equal(t, "rf-1", id)
	require.Len(t, gw.refundSeen, 1)
	assert.Equal(t, "tx-1", gw.refundSeen[0].ExternalTransactionID)
	assert.Equal(t, "1000.00", gw.refundSeen[0].Amount)
}

func TestP9a6_GatewayRefundAdapter_Refund_Error(t *testing.T) {
	sentinel := errors.New("provider down")
	gw := &p9a6Gateway{refundErr: sentinel}
	refund := NewGatewayRefundAdapter(gw)

	id, err := refund.Refund(context.Background(), "tx-1", "1000.00")
	require.ErrorIs(t, err, sentinel)
	assert.Empty(t, id)
}

func TestP9a6_GatewayStatusFetcher_GetStatus_Success(t *testing.T) {
	gw := &p9a6Gateway{statusResp: &adapter.StatusResponse{
		TransactionStatus: "settlement", FraudStatus: "clear", Amount: "1000.00",
	}}
	fetcher := NewGatewayStatusFetcher(gw)

	resp, err := fetcher.GetStatus(context.Background(), "tx-1")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "settlement", resp.TransactionStatus)
	assert.Equal(t, "clear", resp.FraudStatus)
	assert.Equal(t, "1000.00", resp.Amount)
}

func TestP9a6_GatewayStatusFetcher_GetStatus_Error(t *testing.T) {
	sentinel := errors.New("timeout")
	gw := &p9a6Gateway{statusErr: sentinel}
	fetcher := NewGatewayStatusFetcher(gw)

	resp, err := fetcher.GetStatus(context.Background(), "tx-1")
	require.ErrorIs(t, err, sentinel)
	assert.Nil(t, resp)
}

func TestP9a6_GatewayStatusFetcher_GetStatus_NilResponse(t *testing.T) {
	gw := &p9a6Gateway{}
	fetcher := NewGatewayStatusFetcher(gw)

	resp, err := fetcher.GetStatus(context.Background(), "tx-1")
	require.NoError(t, err)
	assert.Nil(t, resp)
}

// ============================================================
// TicketNotificationWorker — Redis-only helpers
// ============================================================

func TestP9a6_TicketNotify_GetLastSeen_EmptyReturnsNilUUID(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewTicketNotificationWorker(nil, nil, client, zap.NewNop(), time.Second)

	seen, err := w.getLastSeen(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, seen)
}

func TestP9a6_TicketNotify_SetThenGetLastSeen(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewTicketNotificationWorker(nil, nil, client, zap.NewNop(), time.Second)
	id := uuid.New()

	require.NoError(t, w.setLastSeen(context.Background(), id))

	seen, err := w.getLastSeen(context.Background())
	require.NoError(t, err)
	assert.Equal(t, id, seen)
}

func TestP9a6_TicketNotify_GetLastSeen_CorruptValueReturnsError(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewTicketNotificationWorker(nil, nil, client, zap.NewNop(), time.Second)

	require.NoError(t, client.SetValue(context.Background(), ticketNotifyLastSeenKey, "not-a-uuid", 0))

	_, err := w.getLastSeen(context.Background())
	require.Error(t, err)
}

// ============================================================
// FeedbackLoopWorker — insight generators (no DB needed)
// ============================================================

func TestP9a6_FeedbackLoop_GenerateGateStaffing_Inserts(t *testing.T) {
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{}}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())

	w.generateGATE_STAFFING(context.Background(), uuid.New())

	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "GATE_STAFFING", repo.inserted[0].InsightType)
	assert.NotEmpty(t, repo.inserted[0].Title)
	assert.NotEmpty(t, repo.inserted[0].Recommendation)
}

func TestP9a6_FeedbackLoop_GenerateGateStaffing_SkipsWhenExists(t *testing.T) {
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{"GATE_STAFFING": true}}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())

	w.generateGATE_STAFFING(context.Background(), uuid.New())

	assert.Empty(t, repo.inserted, "idempotent: existing insight must not be duplicated")
}

func TestP9a6_FeedbackLoop_GenerateBotBlacklist_Inserts(t *testing.T) {
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{}}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())

	w.generateBOT_BLACKLIST(context.Background(), uuid.New())

	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "BOT_BLACKLIST", repo.inserted[0].InsightType)
}

func TestP9a6_FeedbackLoop_GenerateBotBlacklist_SkipsWhenExists(t *testing.T) {
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{"BOT_BLACKLIST": true}}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())

	w.generateBOT_BLACKLIST(context.Background(), uuid.New())

	assert.Empty(t, repo.inserted)
}

func TestP9a6_FeedbackLoop_GeneratePriceRecommendation_Inserts(t *testing.T) {
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{}}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())

	w.generatePRICE_RECOMMENDATION(context.Background(), uuid.New())

	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "PRICE_RECOMMENDATION", repo.inserted[0].InsightType)
}

func TestP9a6_FeedbackLoop_GeneratePriceRecommendation_SkipsWhenExists(t *testing.T) {
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{"PRICE_RECOMMENDATION": true}}
	w := NewFeedbackLoopWorker(nil, repo, zap.NewNop())

	w.generatePRICE_RECOMMENDATION(context.Background(), uuid.New())

	assert.Empty(t, repo.inserted)
}

func TestP9a6_AnalyticsRepoFake_ImplementsInterface(t *testing.T) {
	var repo analytics.AnalyticsRepository = &p9a6AnalyticsRepo{}
	require.NotNil(t, repo)
}
