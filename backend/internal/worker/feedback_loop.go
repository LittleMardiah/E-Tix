package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/analytics"
)

// FeedbackLoopWorker generates insights for COMPLETED events (daily ticker).
// Idempotent: ExistsByEventAndType check before insert.
type FeedbackLoopWorker struct {
	pool     Pool
	repo     analytics.AnalyticsRepository
	logger   *zap.Logger
	interval time.Duration
}

// NewFeedbackLoopWorker constructs a FeedbackLoopWorker.
func NewFeedbackLoopWorker(pool Pool, repo analytics.AnalyticsRepository, logger *zap.Logger) *FeedbackLoopWorker {
	return &FeedbackLoopWorker{pool: pool, repo: repo, logger: logger, interval: 24 * time.Hour}
}

// Run runs the ticker loop.
func (w *FeedbackLoopWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				w.logger.Warn("feedback loop cycle failed", zap.Error(err))
			}
		}
	}
}

// RunOnce processes COMPLETED events in last 48h.
func (w *FeedbackLoopWorker) RunOnce(ctx context.Context) error {
	rows, err := w.pool.Query(ctx, `
		SELECT id FROM events
		WHERE status::text = 'COMPLETED'
		  AND updated_at >= NOW() - INTERVAL '48 hours'`)
	if err != nil {
		return fmt.Errorf("feedback: query events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var eid uuid.UUID
		if err := rows.Scan(&eid); err != nil {
			continue
		}
		w.generateGATE_STAFFING(ctx, eid)
		w.generateBOT_BLACKLIST(ctx, eid)
		w.generatePRICE_RECOMMENDATION(ctx, eid)
	}
	return rows.Err()
}

func (w *FeedbackLoopWorker) generateGATE_STAFFING(ctx context.Context, eventID uuid.UUID) {
	exists, _ := w.repo.ExistsByEventAndType(ctx, eventID, "GATE_STAFFING")
	if exists {
		return
	}
	rec := json.RawMessage(`{"recommendation":"Add staff at busy gates"}`)
	// FIX P9a-7 TD-F6-13: never swallow InsertInsight errors — a silent
	// failure leaves the EO dashboard empty without any alert.
	if err := w.repo.InsertInsight(ctx, &analytics.InsightRecord{
		EventID: eventID, InsightType: "GATE_STAFFING",
		Title: "Gate staff optimization", Description: "Auto-generated", Recommendation: rec,
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		w.logger.Warn("feedback loop: InsertInsight failed",
			zap.String("insight_type", "GATE_STAFFING"),
			zap.String("event_id", eventID.String()),
			zap.Error(err))
	}
}

func (w *FeedbackLoopWorker) generateBOT_BLACKLIST(ctx context.Context, eventID uuid.UUID) {
	exists, _ := w.repo.ExistsByEventAndType(ctx, eventID, "BOT_BLACKLIST")
	if exists {
		return
	}
	rec := json.RawMessage(`{"recommendation":"Review bot patterns"}`)
	// FIX P9a-7 TD-F6-13: never swallow InsertInsight errors.
	if err := w.repo.InsertInsight(ctx, &analytics.InsightRecord{
		EventID: eventID, InsightType: "BOT_BLACKLIST",
		Title: "Bot blacklist report", Description: "Auto-generated", Recommendation: rec,
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		w.logger.Warn("feedback loop: InsertInsight failed",
			zap.String("insight_type", "BOT_BLACKLIST"),
			zap.String("event_id", eventID.String()),
			zap.Error(err))
	}
}

func (w *FeedbackLoopWorker) generatePRICE_RECOMMENDATION(ctx context.Context, eventID uuid.UUID) {
	exists, _ := w.repo.ExistsByEventAndType(ctx, eventID, "PRICE_RECOMMENDATION")
	if exists {
		return
	}
	rec := json.RawMessage(`{"recommendation":"Consider price adjustment"}`)
	// FIX P9a-7 TD-F6-13: never swallow InsertInsight errors.
	if err := w.repo.InsertInsight(ctx, &analytics.InsightRecord{
		EventID: eventID, InsightType: "PRICE_RECOMMENDATION",
		Title: "Pricing recommendation", Description: "Auto-generated", Recommendation: rec,
		GeneratedAt: time.Now().UTC(),
	}); err != nil {
		w.logger.Warn("feedback loop: InsertInsight failed",
			zap.String("insight_type", "PRICE_RECOMMENDATION"),
			zap.String("event_id", eventID.String()),
			zap.Error(err))
	}
}
