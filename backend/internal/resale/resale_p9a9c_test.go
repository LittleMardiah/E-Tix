package resale

// FIX P9a-9c: resale publisher decoupling (queue.DelayedPublisher) — covers the
// "resale_expiry_check" scheduling path that was unreachable while the service
// held a concrete *queue.Connection (zero-value panic without a live broker).

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p9a9cDelayedPublisher records PublishDelayed calls.
type p9a9cDelayedPublisher struct {
	calls   int
	queue   string
	payload []byte
	delay   time.Duration
	err     error
}

func (p *p9a9cDelayedPublisher) PublishDelayed(_ context.Context, queueName string, payload []byte, delay time.Duration) error {
	p.calls++
	p.queue = queueName
	p.payload = payload
	p.delay = delay
	return p.err
}

func TestP9a9cBuyResaleTicket_SchedulesExpiryCheck(t *testing.T) {
	resaleID := uuid.New()
	sellerID := uuid.New()
	buyerID := uuid.New()
	rt := &ResaleTransaction{
		ID:           resaleID,
		SellerUserID: sellerID,
		ResalePrice:  "50000",
		Status:       "PENDING",
	}
	repo := &p9a6ResaleRepo{byID: rt}
	db := &p9a6ResaleDB{}
	pub := &p9a9cDelayedPublisher{}
	svc := NewResaleService(repo, db, pub, nil, "0.05")

	resp, err := svc.BuyResaleTicket(context.Background(), buyerID, resaleID)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "RESERVED", resp.Status)
	require.Equal(t, 1, pub.calls, "expiry check must be scheduled exactly once")
	assert.Equal(t, "resale_expiry_check", pub.queue)
	assert.Equal(t, 180*time.Second, pub.delay)

	var payload ResaleExpiryPayload
	require.NoError(t, json.Unmarshal(pub.payload, &payload))
	assert.Equal(t, resaleID, payload.ResaleID)
}

func TestP9a9cBuyResaleTicket_WithoutPublisherStillReserves(t *testing.T) {
	resaleID := uuid.New()
	rt := &ResaleTransaction{
		ID:           resaleID,
		SellerUserID: uuid.New(),
		ResalePrice:  "50000",
		Status:       "PENDING",
	}
	repo := &p9a6ResaleRepo{byID: rt}
	db := &p9a6ResaleDB{}
	svc := NewResaleService(repo, db, nil, nil, "0.05")

	resp, err := svc.BuyResaleTicket(context.Background(), uuid.New(), resaleID)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "RESERVED", resp.Status)
}
