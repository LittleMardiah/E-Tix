package worker

// FIX P9a-9c — STAGE 2: worker coverage boost (real logic paths).
//
// Before this file the worker suite only exercised the poison-payload
// branch, which left every DB-backed and MQ-backed branch at 0% (worker
// package coverage: 45.4%). The queue.Connection → queue.Consumer and
// *pgxpool.Pool → Pool refactors shipped with this commit make those paths
// injectable, so the Run loops, Handle bodies and RunOnce cycles can now be
// driven without a live RabbitMQ broker or PostgreSQL instance.
//
// R3.1: no external service is required — every collaborator is a local fake.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/config"
	"github.com/LittleMardiah/E-Tix/backend/internal/resale"
)

// ============================================================
// Fakes
// ============================================================

// p9a9cRows is a scriptable pgx.Rows fake. Each row is a []any of column
// values; Scan assigns them positionally into the destination pointers.
type p9a9cRows struct {
	rows    [][]any
	idx     int
	err     error
	scanErr error
}

func (r *p9a9cRows) Close() {}

func (r *p9a9cRows) Err() error { return r.err }

func (r *p9a9cRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *p9a9cRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *p9a9cRows) Next() bool {
	r.idx++
	return r.idx <= len(r.rows)
}

func (r *p9a9cRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.idx < 1 || r.idx > len(r.rows) {
		return errors.New("p9a9cRows: Scan without Next")
	}
	return p9a9cAssign(r.rows[r.idx-1], dest...)
}

func (r *p9a9cRows) Values() ([]any, error) {
	if r.idx < 1 || r.idx > len(r.rows) {
		return nil, errors.New("p9a9cRows: Values without Next")
	}
	return r.rows[r.idx-1], nil
}

func (r *p9a9cRows) RawValues() [][]byte { return nil }

func (r *p9a9cRows) Conn() *pgx.Conn { return nil }

func (r *p9a9cRows) TypeMap() *pgtype.Map { return nil }

// p9a9cRow is a scriptable pgx.Row fake.
type p9a9cRow struct {
	val any
	err error
}

func (r *p9a9cRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	return p9a9cAssign([]any{r.val}, dest...)
}

// p9a9cAssign copies src values into the destination pointers, unwrapping one
// level of pointer indirection when needed (e.g. **string <- *string).
func p9a9cAssign(src []any, dest ...any) error {
	for i := range dest {
		if i >= len(src) {
			return fmt.Errorf("p9a9cAssign: missing column %d", i)
		}
		rv := reflect.ValueOf(dest[i])
		if rv.Kind() != reflect.Ptr || rv.IsNil() {
			return fmt.Errorf("p9a9cAssign: destination %d is not a non-nil pointer", i)
		}
		sv := reflect.ValueOf(src[i])
		if !sv.IsValid() {
			continue
		}
		if sv.Type().AssignableTo(rv.Elem().Type()) {
			rv.Elem().Set(sv)
			continue
		}
		if sv.Kind() == reflect.Ptr && sv.Type().Elem().AssignableTo(rv.Elem().Type()) {
			rv.Elem().Set(sv.Elem())
			continue
		}
		return fmt.Errorf("p9a9cAssign: cannot assign %T into %T", src[i], dest[i])
	}
	return nil
}

// p9a9cQueryResult is one scripted Query response.
type p9a9cQueryResult struct {
	rows [][]any
	err  error
}

// p9a9cPool is a scriptable worker.Pool fake.
type p9a9cPool struct {
	beginErr  error
	tx        pgx.Tx
	queryRes  []p9a9cQueryResult
	execErr   error
	execCalls []string
	row       pgx.Row
}

func (p *p9a9cPool) Begin(context.Context) (pgx.Tx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	if p.tx == nil {
		p.tx = &p9a9cTx{}
	}
	return p.tx, nil
}

func (p *p9a9cPool) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	if len(p.queryRes) == 0 {
		return &p9a9cRows{}, nil
	}
	res := p.queryRes[0]
	p.queryRes = p.queryRes[1:]
	if res.err != nil {
		return nil, res.err
	}
	return &p9a9cRows{rows: res.rows}, nil
}

func (p *p9a9cPool) QueryRow(context.Context, string, ...any) pgx.Row {
	if p.row == nil {
		return &p9a9cRow{err: pgx.ErrNoRows}
	}
	return p.row
}

func (p *p9a9cPool) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	p.execCalls = append(p.execCalls, sql)
	if p.execErr != nil {
		return pgconn.CommandTag{}, p.execErr
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

// p9a9cTx is a pgx.Tx fake that records Exec statements and commit/rollback.
type p9a9cTx struct {
	pgx.Tx
	committed  bool
	rolledBack bool
	execCalls  []string
	execErr    error
	tag        pgconn.CommandTag
	row        pgx.Row
}

func (t *p9a9cTx) Commit(context.Context) error {
	t.committed = true
	return nil
}

func (t *p9a9cTx) Rollback(context.Context) error {
	t.rolledBack = true
	return nil
}

func (t *p9a9cTx) QueryRow(context.Context, string, ...any) pgx.Row {
	if t.row == nil {
		return &p9a9cRow{err: pgx.ErrNoRows}
	}
	return t.row
}

func (t *p9a9cTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	t.execCalls = append(t.execCalls, sql)
	if t.execErr != nil {
		return pgconn.CommandTag{}, t.execErr
	}
	if t.tag == (pgconn.CommandTag{}) {
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return t.tag, nil
}

// p9a9cConsumer is a queue.Consumer fake: it replays scripted deliveries and
// records ack/nack decisions.
type p9a9cConsumer struct {
	deliveries chan amqp.Delivery
	consumeErr error
	acked      []uint64
	nacked     []uint64
	requeues   []bool
}

func (c *p9a9cConsumer) Consume(string) (<-chan amqp.Delivery, error) {
	if c.consumeErr != nil {
		return nil, c.consumeErr
	}
	if c.deliveries == nil {
		c.deliveries = make(chan amqp.Delivery, 8)
	}
	return c.deliveries, nil
}

func (c *p9a9cConsumer) Ack(d amqp.Delivery) error {
	c.acked = append(c.acked, d.DeliveryTag)
	return nil
}

func (c *p9a9cConsumer) Nack(d amqp.Delivery, requeue bool) error {
	c.nacked = append(c.nacked, d.DeliveryTag)
	c.requeues = append(c.requeues, requeue)
	return nil
}

// p9a9cNotifier is a notification.TicketNotification fake.
type p9a9cNotifier struct {
	calls [][]string
	err   error
}

func (n *p9a9cNotifier) SendFallbackPIN(_ context.Context, userID uuid.UUID, email, phone, pin string) error {
	n.calls = append(n.calls, []string{userID.String(), email, phone, pin})
	return n.err
}

// p9a9cAckLog records ack/nack decisions made through the delivery's
// Acknowledger. BulkCancelWorker and PayoutWorker settle messages with
// d.Ack()/d.Nack() on the delivery itself (not through the queue connection),
// so the decision has to be observed here.
type p9a9cAckLog struct {
	acked   []uint64
	nacked  []uint64
	requeue []bool
}

func (a *p9a9cAckLog) Ack(tag uint64, _ bool) error {
	a.acked = append(a.acked, tag)
	return nil
}

func (a *p9a9cAckLog) Nack(tag uint64, _ bool, requeue bool) error {
	a.nacked = append(a.nacked, tag)
	a.requeue = append(a.requeue, requeue)
	return nil
}

func (a *p9a9cAckLog) Reject(tag uint64, requeue bool) error {
	a.nacked = append(a.nacked, tag)
	a.requeue = append(a.requeue, requeue)
	return nil
}

// p9a9cDelivery builds a delivery wired to the given acknowledger.
func p9a9cDelivery(tag uint64, body []byte, ack amqp.Acknowledger) amqp.Delivery {
	return amqp.Delivery{
		DeliveryTag:  tag,
		Body:         body,
		Acknowledger: ack,
	}
}

// p9a9cResaleRepo extends the existing resale fake with a configurable
// UpdateStatus failure.
type p9a9cResaleRepo struct {
	*p9a6ResaleRepo
	updateStatusErr error
}

func (r *p9a9cResaleRepo) UpdateStatus(_ context.Context, _ pgx.Tx, _ uuid.UUID, status string) error {
	r.updatedStat = append(r.updatedStat, status)
	return r.updateStatusErr
}

// ============================================================
// BulkCancelWorker
// ============================================================

func TestP9a9cBulkCancelWorker_Run_ProcessesMessageAndAcks(t *testing.T) {
	client, _ := p9a6GoRedis(t)
	gw := &p9a6Gateway{}
	extTx := "ext-tx-1"
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{
		{rows: [][]any{{uuid.New(), &extTx, "10000"}}},
		{rows: [][]any{{uuid.New()}}},
	}}
	cons := &p9a9cConsumer{}
	w := NewBulkCancelWorker(pool, client, gw, zap.NewNop(), cons)
	ackLog := &p9a9cAckLog{}

	body, err := json.Marshal(BulkCancelPayload{
		EventID: uuid.New(), AdminID: uuid.New(), Reason: "event cancelled",
	})
	require.NoError(t, err)

	cons.deliveries = make(chan amqp.Delivery, 4)
	cons.deliveries <- p9a9cDelivery(7, body, ackLog)
	close(cons.deliveries)

	require.NoError(t, w.Run(context.Background()))

	assert.Equal(t, []uint64{7}, ackLog.acked, "successful message must be acked")
	assert.Empty(t, ackLog.nacked)
	assert.Len(t, gw.refundSeen, 1, "the PAID obligation must be refunded")
	assert.Equal(t, "ext-tx-1", gw.refundSeen[0].ExternalTransactionID)
	assert.Len(t, pool.execCalls, 2, "mark-refunded + cancel-tickets updates")
}

func TestP9a9cBulkCancelWorker_Run_HandleErrorNacksAndRequeues(t *testing.T) {
	client, _ := p9a6GoRedis(t)
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{{err: errors.New("query boom")}}}
	cons := &p9a9cConsumer{}
	w := NewBulkCancelWorker(pool, client, &p9a6Gateway{}, zap.NewNop(), cons)
	ackLog := &p9a9cAckLog{}

	body, err := json.Marshal(BulkCancelPayload{EventID: uuid.New(), AdminID: uuid.New(), Reason: "x"})
	require.NoError(t, err)

	cons.deliveries = make(chan amqp.Delivery, 4)
	cons.deliveries <- p9a9cDelivery(9, body, ackLog)
	close(cons.deliveries)

	require.NoError(t, w.Run(context.Background()))

	assert.Empty(t, ackLog.acked)
	assert.Equal(t, []uint64{9}, ackLog.nacked)
	assert.Equal(t, []bool{true}, ackLog.requeue, "transient DB error must be requeued")
}

func TestP9a9cBulkCancelWorker_Run_ConsumeErrorPropagates(t *testing.T) {
	client, _ := p9a6GoRedis(t)
	cons := &p9a9cConsumer{consumeErr: errors.New("mq down")}
	w := NewBulkCancelWorker(&p9a9cPool{}, client, &p9a6Gateway{}, zap.NewNop(), cons)

	err := w.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consume")
}

func TestP9a9cBulkCancelWorker_Handle_SkipsNilExternalTxID(t *testing.T) {
	client, _ := p9a6GoRedis(t)
	gw := &p9a6Gateway{}
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{
		{rows: [][]any{{uuid.New(), nil, "10000"}}},
		{rows: [][]any{{uuid.New()}}},
	}}
	w := NewBulkCancelWorker(pool, client, gw, zap.NewNop(), &p9a9cConsumer{})

	body, err := json.Marshal(BulkCancelPayload{EventID: uuid.New(), AdminID: uuid.New(), Reason: "x"})
	require.NoError(t, err)

	require.NoError(t, w.Handle(context.Background(), body))
	assert.Empty(t, gw.refundSeen, "nil external_transaction_id must be skipped")
}

func TestP9a9cBulkCancelWorker_Handle_RefundFailureDoesNotMarkRefunded(t *testing.T) {
	client, _ := p9a6GoRedis(t)
	gw := &p9a6Gateway{refundErr: errors.New("gateway down")}
	extTx := "ext-tx-2"
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{
		{rows: [][]any{{uuid.New(), &extTx, "10000"}}},
		{rows: [][]any{{uuid.New()}}},
	}}
	w := NewBulkCancelWorker(pool, client, gw, zap.NewNop(), &p9a9cConsumer{})

	body, err := json.Marshal(BulkCancelPayload{EventID: uuid.New(), AdminID: uuid.New(), Reason: "x"})
	require.NoError(t, err)

	require.NoError(t, w.Handle(context.Background(), body))
	assert.Len(t, gw.refundSeen, 1)
	assert.Len(t, pool.execCalls, 1, "only the cancel-tickets update must run")
}

// ============================================================
// PayoutWorker
// ============================================================

func TestP9a9cPayoutWorker_Run_SuccessAcks(t *testing.T) {
	tx := &p9a9cTx{}
	pool := &p9a9cPool{tx: tx}
	repo := &p9a6ResaleRepo{}
	cons := &p9a9cConsumer{}
	w := NewPayoutWorker(pool, repo, &p9a6Gateway{}, zap.NewNop(),
		&config.Config{SystemUserUUID: uuid.New().String()}, cons)
	ackLog := &p9a9cAckLog{}

	body, err := json.Marshal(resale.PayoutPayload{
		ResaleID: uuid.New(), SellerPayout: "9000", SellerUserID: uuid.New(),
	})
	require.NoError(t, err)

	cons.deliveries = make(chan amqp.Delivery, 4)
	cons.deliveries <- p9a9cDelivery(11, body, ackLog)
	close(cons.deliveries)

	require.NoError(t, w.Run(context.Background()))

	assert.Equal(t, []uint64{11}, ackLog.acked)
	assert.Contains(t, repo.updatedStat, "COMPLETED")
	assert.True(t, tx.committed)
}

func TestP9a9cPayoutWorker_Run_ExhaustedRetriesMarksPendingPayout(t *testing.T) {
	tx := &p9a9cTx{}
	pool := &p9a9cPool{tx: tx}
	repo := &p9a6ResaleRepo{}
	gw := &p9a6Gateway{payoutErr: errors.New("payout rejected")}
	cons := &p9a9cConsumer{}
	w := NewPayoutWorker(pool, repo, gw, zap.NewNop(),
		&config.Config{SystemUserUUID: "11111111-1111-1111-1111-111111111111"}, cons)
	ackLog := &p9a9cAckLog{}

	body, err := json.Marshal(resale.PayoutPayload{
		ResaleID: uuid.New(), SellerPayout: "9000", SellerUserID: uuid.New(),
	})
	require.NoError(t, err)

	cons.deliveries = make(chan amqp.Delivery, 4)
	cons.deliveries <- p9a9cDelivery(12, body, ackLog)
	close(cons.deliveries)

	require.NoError(t, w.Run(context.Background()))

	assert.Len(t, gw.payoutSeen, 3, "payout must be retried 3 times")
	assert.Contains(t, repo.updatedStat, "PENDING_PAYOUT")
	assert.Len(t, tx.execCalls, 1, "admin_actions audit row must be written")
	assert.Contains(t, tx.execCalls[0], "admin_actions")
	assert.True(t, tx.committed)
}

func TestP9a9cPayoutWorker_Handle_BeginError(t *testing.T) {
	pool := &p9a9cPool{beginErr: errors.New("cannot begin")}
	w := NewPayoutWorker(pool, &p9a6ResaleRepo{}, &p9a6Gateway{}, zap.NewNop(),
		&config.Config{}, &p9a9cConsumer{})

	body, err := json.Marshal(resale.PayoutPayload{ResaleID: uuid.New(), SellerPayout: "1", SellerUserID: uuid.New()})
	require.NoError(t, err)

	require.Error(t, w.Handle(context.Background(), body))
}

func TestP9a9cPayoutWorker_Handle_UpdateStatusErrorAborts(t *testing.T) {
	tx := &p9a9cTx{}
	pool := &p9a9cPool{tx: tx}
	repo := &p9a9cResaleRepo{
		p9a6ResaleRepo:  &p9a6ResaleRepo{},
		updateStatusErr: errors.New("update failed"),
	}
	w := NewPayoutWorker(pool, repo, &p9a6Gateway{}, zap.NewNop(),
		&config.Config{}, &p9a9cConsumer{})

	body, err := json.Marshal(resale.PayoutPayload{ResaleID: uuid.New(), SellerPayout: "1", SellerUserID: uuid.New()})
	require.NoError(t, err)

	err = w.Handle(context.Background(), body)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
	assert.False(t, tx.committed, "a failed status update must not commit")
}

// ============================================================
// ResaleExpiryWorker
// ============================================================

func TestP9a9cResaleExpiryWorker_Handle_ClearsBuyer(t *testing.T) {
	tx := &p9a9cTx{}
	pool := &p9a9cPool{tx: tx}
	buyer := uuid.New()
	repo := &p9a6ResaleRepo{rt: &resale.ResaleTransaction{
		ID: uuid.New(), Status: "PENDING", BuyerUserID: &buyer,
	}}
	w := NewResaleExpiryWorker(pool, repo, zap.NewNop())

	body, err := json.Marshal(resale.ResaleExpiryPayload{ResaleID: uuid.New()})
	require.NoError(t, err)

	require.NoError(t, w.Handle(context.Background(), body))
	assert.True(t, repo.cleared, "the stale buyer must be cleared")
	assert.True(t, tx.committed)
}

func TestP9a9cResaleExpiryWorker_Handle_NonPendingIsNoOp(t *testing.T) {
	tx := &p9a9cTx{}
	pool := &p9a9cPool{tx: tx}
	repo := &p9a6ResaleRepo{rt: &resale.ResaleTransaction{ID: uuid.New(), Status: "COMPLETED"}}
	w := NewResaleExpiryWorker(pool, repo, zap.NewNop())

	body, err := json.Marshal(resale.ResaleExpiryPayload{ResaleID: uuid.New()})
	require.NoError(t, err)

	require.NoError(t, w.Handle(context.Background(), body))
	assert.False(t, repo.cleared)
	assert.False(t, tx.committed)
}

func TestP9a9cResaleExpiryWorker_Handle_RepoLookupErrorIsSwallowed(t *testing.T) {
	pool := &p9a9cPool{}
	repo := &p9a6ResaleRepo{getErr: errors.New("not found")}
	w := NewResaleExpiryWorker(pool, repo, zap.NewNop())

	body, err := json.Marshal(resale.ResaleExpiryPayload{ResaleID: uuid.New()})
	require.NoError(t, err)

	require.NoError(t, w.Handle(context.Background(), body))
	assert.False(t, repo.cleared)
}

// ============================================================
// BookingExpiryWorker (MQ loop)
// ============================================================

func TestP9a9cBookingExpiryWorker_Run_AcksProcessedMessage(t *testing.T) {
	repo := &fakeBookingExpiryRepo{
		status: "PENDING", statusForUpdate: "PENDING", markExpiredRows: 1,
	}
	w, _, rel, _ := newExpiryWorker(t, repo, &fakeObligations{})
	cons := &p9a9cConsumer{}
	w.conn = cons

	bookingID := uuid.New()
	cons.deliveries = make(chan amqp.Delivery, 4)
	cons.deliveries <- p9a9cDelivery(21, mustJSON(bookingID), &p9a9cAckLog{})
	close(cons.deliveries)

	require.NoError(t, w.Run(context.Background()))

	assert.Equal(t, []uint64{21}, cons.acked)
	assert.Equal(t, 1, rel.calls, "seats must be released after commit")
}

func TestP9a9cBookingExpiryWorker_Run_NacksPoisonWithoutRequeue(t *testing.T) {
	repo := &fakeBookingExpiryRepo{status: "PENDING"}
	w, _, _, _ := newExpiryWorker(t, repo, &fakeObligations{})
	cons := &p9a9cConsumer{}
	w.conn = cons

	cons.deliveries = make(chan amqp.Delivery, 4)
	cons.deliveries <- p9a9cDelivery(22, []byte("not-json"), &p9a9cAckLog{})
	close(cons.deliveries)

	require.NoError(t, w.Run(context.Background()))

	assert.Empty(t, cons.acked)
	assert.Equal(t, []uint64{22}, cons.nacked)
	assert.Equal(t, []bool{false}, cons.requeues, "poison messages must not be requeued")
}

func TestP9a9cBookingExpiryWorker_Run_ConsumeErrorPropagates(t *testing.T) {
	repo := &fakeBookingExpiryRepo{}
	w, _, _, _ := newExpiryWorker(t, repo, &fakeObligations{})
	w.conn = &p9a9cConsumer{consumeErr: errors.New("mq down")}

	err := w.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "consume")
}

// ============================================================
// FanScoreWorker (MQ loop)
// ============================================================

func TestP9a9cFanScoreWorker_Run_AcksValidMessage(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	cons := &p9a9cConsumer{}
	w := NewFanScoreWorker(cons, client, nil, zap.NewNop())

	body, err := json.Marshal(map[string]any{"user_id": uuid.NewString(), "final_score": 80})
	require.NoError(t, err)

	cons.deliveries = make(chan amqp.Delivery, 4)
	cons.deliveries <- p9a9cDelivery(31, body, &p9a9cAckLog{})
	close(cons.deliveries)

	require.NoError(t, w.Run(context.Background()))

	assert.Equal(t, []uint64{31}, cons.acked)
	assert.Empty(t, cons.nacked)
}

func TestP9a9cFanScoreWorker_Run_NacksPoison(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	cons := &p9a9cConsumer{}
	w := NewFanScoreWorker(cons, client, nil, zap.NewNop())

	cons.deliveries = make(chan amqp.Delivery, 4)
	cons.deliveries <- p9a9cDelivery(32, []byte("not-json"), &p9a9cAckLog{})
	close(cons.deliveries)

	require.NoError(t, w.Run(context.Background()))

	assert.Empty(t, cons.acked)
	assert.Equal(t, []uint64{32}, cons.nacked)
	assert.Equal(t, []bool{false}, cons.requeues)
}

func TestP9a9cFanScoreWorker_Run_ConsumeErrorPropagates(t *testing.T) {
	client, _ := p9a6RedisClient(t)
	w := NewFanScoreWorker(&p9a9cConsumer{consumeErr: errors.New("mq down")}, client, nil, zap.NewNop())

	require.Error(t, w.Run(context.Background()))
}

// ============================================================
// FeedbackLoopWorker
// ============================================================

func TestP9a9cFeedbackLoopWorker_RunOnce_GeneratesInsights(t *testing.T) {
	eventID := uuid.New()
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{{rows: [][]any{{eventID}}}}}
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{}}
	w := NewFeedbackLoopWorker(pool, repo, zap.NewNop())

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Len(t, repo.inserted, 3, "GATE_STAFFING + BOT_BLACKLIST + PRICE_RECOMMENDATION")
}

func TestP9a9cFeedbackLoopWorker_RunOnce_SkipsExistingInsight(t *testing.T) {
	eventID := uuid.New()
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{{rows: [][]any{{eventID}}}}}
	repo := &p9a6AnalyticsRepo{existing: map[string]bool{"GATE_STAFFING": true}}
	w := NewFeedbackLoopWorker(pool, repo, zap.NewNop())

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Len(t, repo.inserted, 2)
}

func TestP9a9cFeedbackLoopWorker_RunOnce_QueryError(t *testing.T) {
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{{err: errors.New("query boom")}}}
	w := NewFeedbackLoopWorker(pool, &p9a6AnalyticsRepo{}, zap.NewNop())

	err := w.RunOnce(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query events")
}

// ============================================================
// QRRegenerationWorker
// ============================================================

func TestP9a9cQRRegenerationWorker_RunOnce_WritesQRPairs(t *testing.T) {
	rdb, mr := p9a9RedisClient(t)
	ticketID := uuid.New()
	ownerID := uuid.New()
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{
		{rows: [][]any{{ticketID, ownerID, "secret-key"}}},
	}}
	w := NewQRRegenerationWorker(pool, rdb, zap.NewNop(), 0)

	require.NoError(t, w.RunOnce(context.Background()))

	val, err := mr.Get("qr:" + ticketID.String())
	require.NoError(t, err)
	assert.NotEmpty(t, val, "the regenerated QR must be written to Redis")
}

func TestP9a9cQRRegenerationWorker_RunOnce_EmptyRowsIsNoOp(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	pool := &p9a9cPool{}
	w := NewQRRegenerationWorker(pool, rdb, zap.NewNop(), 0)

	require.NoError(t, w.RunOnce(context.Background()))
}

func TestP9a9cQRRegenerationWorker_RunOnce_QueryError(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{{err: errors.New("query boom")}}}
	w := NewQRRegenerationWorker(pool, rdb, zap.NewNop(), 0)

	err := w.RunOnce(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list active")
}

func TestP9a9cQRRegenerationWorker_RunOnce_SkipsEmptySecretKey(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	ticketID := uuid.New()
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{
		{rows: [][]any{{ticketID, uuid.New(), ""}}},
	}}
	w := NewQRRegenerationWorker(pool, rdb, zap.NewNop(), 0)

	require.NoError(t, w.RunOnce(context.Background()))
}

// ============================================================
// TicketNotificationWorker
// ============================================================

func TestP9a9cTicketNotificationWorker_RunOnce_DispatchesPIN(t *testing.T) {
	rdb, mr := p9a9RedisClient(t)
	ticketID := uuid.New()
	ownerID := uuid.New()
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{
		{rows: [][]any{{ticketID, ownerID, "a@b.c", "0812", "123456"}}},
	}}
	notifier := &p9a9cNotifier{}
	w := NewTicketNotificationWorker(pool, notifier, rdb, zap.NewNop(), 0)

	require.NoError(t, w.RunOnce(context.Background()))

	require.Len(t, notifier.calls, 1)
	assert.Equal(t, ownerID.String(), notifier.calls[0][0])
	assert.Equal(t, "123456", notifier.calls[0][3])

	lastSeen, err := mr.Get("ticket_notify:last_seen")
	require.NoError(t, err)
	assert.Equal(t, ticketID.String(), lastSeen)
}

func TestP9a9cTicketNotificationWorker_RunOnce_NoRowsIsNoOp(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	pool := &p9a9cPool{}
	notifier := &p9a9cNotifier{}
	w := NewTicketNotificationWorker(pool, notifier, rdb, zap.NewNop(), 0)

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Empty(t, notifier.calls)
}

func TestP9a9cTicketNotificationWorker_RunOnce_ListError(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{{err: errors.New("query boom")}}}
	w := NewTicketNotificationWorker(pool, &p9a9cNotifier{}, rdb, zap.NewNop(), 0)

	err := w.RunOnce(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list")
}

func TestP9a9cTicketNotificationWorker_RunOnce_DispatchErrorContinues(t *testing.T) {
	rdb, _ := p9a9RedisClient(t)
	ticketID := uuid.New()
	pool := &p9a9cPool{queryRes: []p9a9cQueryResult{
		{rows: [][]any{{ticketID, uuid.New(), "a@b.c", "0812", "123456"}}},
	}}
	notifier := &p9a9cNotifier{err: errors.New("smtp down")}
	w := NewTicketNotificationWorker(pool, notifier, rdb, zap.NewNop(), 0)

	require.NoError(t, w.RunOnce(context.Background()))
	assert.Len(t, notifier.calls, 1)
}

// ============================================================
// Pgx repositories (worker package)
// ============================================================

func TestP9a9cPgxBookingExpiryRepo_GetPaymentStatus(t *testing.T) {
	pool := &p9a9cPool{row: &p9a9cRow{val: "PENDING"}}
	repo := NewPgxBookingExpiryRepo(pool)

	status, err := repo.GetPaymentStatus(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "PENDING", status)
}

func TestP9a9cPgxBookingExpiryRepo_GetPaymentStatusNoRows(t *testing.T) {
	pool := &p9a9cPool{row: &p9a9cRow{err: pgx.ErrNoRows}}
	repo := NewPgxBookingExpiryRepo(pool)

	_, err := repo.GetPaymentStatus(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "booking not found")
}

func TestP9a9cPgxBookingExpiryRepo_GetPaymentStatusError(t *testing.T) {
	pool := &p9a9cPool{row: &p9a9cRow{err: errors.New("db down")}}
	repo := NewPgxBookingExpiryRepo(pool)

	_, err := repo.GetPaymentStatus(context.Background(), uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestP9a9cPgxBookingExpiryRepo_InsertAdminActionStandalone(t *testing.T) {
	pool := &p9a9cPool{}
	repo := NewPgxBookingExpiryRepo(pool)

	require.NoError(t, repo.InsertAdminActionStandalone(
		context.Background(), uuid.New(), "FORCE_RELEASE_SEAT", "booking", uuid.New(), "reason"))
	assert.Len(t, pool.execCalls, 1)
	assert.Contains(t, pool.execCalls[0], "admin_actions")
}

func TestP9a9cPgxBookingExpiryRepo_MarkBookingExpired(t *testing.T) {
	tx := &p9a9cTx{tag: pgconn.NewCommandTag("UPDATE 1")}
	repo := NewPgxBookingExpiryRepo(&p9a9cPool{})

	rows, err := repo.MarkBookingExpired(context.Background(), tx, uuid.New())
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows)
	assert.Len(t, tx.execCalls, 1)
}

func TestP9a9cPgxBookingExpiryRepo_MarkBookingExpiredError(t *testing.T) {
	tx := &p9a9cTx{execErr: errors.New("db down")}
	repo := NewPgxBookingExpiryRepo(&p9a9cPool{})

	_, err := repo.MarkBookingExpired(context.Background(), tx, uuid.New())
	require.Error(t, err)
}

func TestP9a9cPgxBookingExpiryRepo_GetPaymentStatusForUpdate(t *testing.T) {
	tx := &p9a9cTx{}
	repo := NewPgxBookingExpiryRepo(&p9a9cPool{})

	_, err := repo.GetPaymentStatusForUpdate(context.Background(), tx, uuid.New())
	require.Error(t, err, "the fake tx does not implement QueryRow")
}

func TestP9a9cPgxReconciliationRepo_ReconcileUpdate(t *testing.T) {
	tx := &p9a9cTx{tag: pgconn.NewCommandTag("UPDATE 1")}
	repo := NewPgxReconciliationRepo(&p9a9cPool{})

	rows, err := repo.ReconcileUpdate(context.Background(), tx, uuid.New(), "PAID", "note")
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows)
	assert.Len(t, tx.execCalls, 1)
}

func TestP9a9cPgxReconciliationRepo_ReconcileUpdateError(t *testing.T) {
	tx := &p9a9cTx{execErr: errors.New("db down")}
	repo := NewPgxReconciliationRepo(&p9a9cPool{})

	_, err := repo.ReconcileUpdate(context.Background(), tx, uuid.New(), "PAID", "note")
	require.Error(t, err)
}

func TestP9a9cPgxReconciliationRepo_InsertAdminAction(t *testing.T) {
	tx := &p9a9cTx{}
	repo := NewPgxReconciliationRepo(&p9a9cPool{})

	require.NoError(t, repo.InsertAdminAction(
		context.Background(), tx, uuid.New(), "RECONCILE", "payment_obligation", uuid.New(), "note"))
	assert.Len(t, tx.execCalls, 1)
	assert.Contains(t, tx.execCalls[0], "admin_actions")
}

func TestP9a9cPgxBlacklistRepo_InsertBlacklist(t *testing.T) {
	pool := &p9a9cPool{}
	repo := NewPgxBlacklistRepo(pool)

	require.NoError(t, repo.InsertBlacklist(
		context.Background(), uuid.New(), "ip", "1.2.3.4", "bot", nil))
	assert.Len(t, pool.execCalls, 1)
	assert.Contains(t, pool.execCalls[0], "blacklist")
}
