package payment

// FIX P9a-9c: payment publisher decoupling (queue.Publisher) — covers the
// "resale_payout" enqueue path that was unreachable while the service held a
// concrete *queue.Connection (zero-value panic without a live broker).

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p9a9cPublisher records PublishToQueue calls.
type p9a9cPublisher struct {
	calls   int
	queue   string
	payload []byte
	err     error
}

func (p *p9a9cPublisher) PublishToQueue(_ context.Context, queueName string, payload []byte) error {
	p.calls++
	p.queue = queueName
	p.payload = payload
	return p.err
}

func TestP9a9cHandleWebhook_ResalePaidEnqueuesPayout(t *testing.T) {
	repo := &memObligationRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &fakeGateway{})

	txID := "tx-resale-paid"
	resaleID := uuid.New()
	sellerID := uuid.New()
	rt := &ResaleTxLite{
		ID:                   resaleID,
		SellerUserID:         sellerID,
		SellerPayout:         "45000.00",
		Status:               StatusPending,
		PaymentTransactionID: &txID,
	}
	resaleRepo := &fakeResaleRepo{tx: rt}
	pub := &p9a9cPublisher{}
	svc.SetResaleWebhookDeps(resaleRepo, pub)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, txID, "SUCCESS"), nil)

	require.NoError(t, err)
	assert.Equal(t, "COMPLETED", resaleRepo.updated, "resale row must be completed")
	assert.Equal(t, resaleID, resaleRepo.updatedID)
	require.Equal(t, 1, pub.calls, "payout job must be enqueued exactly once")
	assert.Equal(t, "resale_payout", pub.queue)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(pub.payload, &payload))
	assert.Equal(t, resaleID.String(), payload["resale_id"])
	assert.Equal(t, "45000.00", payload["seller_payout"])
	assert.Equal(t, sellerID.String(), payload["seller_user_id"])
}

func TestP9a9cHandleWebhook_ResaleNonPaidDoesNotEnqueue(t *testing.T) {
	repo := &memObligationRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &fakeGateway{})

	txID := "tx-resale-failed"
	rt := &ResaleTxLite{
		ID:                   uuid.New(),
		Status:               StatusPending,
		PaymentTransactionID: &txID,
	}
	pub := &p9a9cPublisher{}
	svc.SetResaleWebhookDeps(&fakeResaleRepo{tx: rt}, pub)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, txID, "FAILURE"), nil)

	require.NoError(t, err)
	assert.Zero(t, pub.calls, "failed payments must not enqueue a payout job")
}

func TestP9a9cHandleWebhook_ResaleAlreadyTerminalDoesNotEnqueue(t *testing.T) {
	repo := &memObligationRepo{}
	svc, _, _ := newPayEnv(t, repo, &fakeBookingReader{}, &fakeGateway{})

	txID := "tx-resale-terminal"
	rt := &ResaleTxLite{
		ID:                   uuid.New(),
		Status:               StatusPaid,
		PaymentTransactionID: &txID,
	}
	pub := &p9a9cPublisher{}
	svc.SetResaleWebhookDeps(&fakeResaleRepo{tx: rt}, pub)

	err := svc.HandleWebhook(context.Background(), webhookPayload(t, txID, "SUCCESS"), nil)

	require.NoError(t, err)
	assert.Zero(t, pub.calls, "terminal resale rows must be skipped")
}
