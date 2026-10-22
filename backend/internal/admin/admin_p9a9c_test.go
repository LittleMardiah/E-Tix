package admin_test

// FIX P9a-9c — TD-F9-08: close the uncovered publish branch of
// AdminServiceImpl.BulkCancel (internal/admin/service.go:118).
//
// The branch was unreachable in unit tests because AdminServiceImpl held a
// concrete *queue.Connection: a zero-value Connection panics as soon as it
// tries to open an AMQP channel, so the "publish succeeded" path could never
// be exercised without a live RabbitMQ broker. The queue.Connection →
// queue.Publisher refactor (shipped with this commit) makes the publisher
// injectable, which closes the 2-statement gap recorded in the TD register.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
)

// p9a9cPublisher is a queue.Publisher fake that records the published message.
type p9a9cPublisher struct {
	queueName string
	payload   []byte
	calls     int
	err       error
}

func (p *p9a9cPublisher) PublishToQueue(_ context.Context, queueName string, payload []byte) error {
	p.calls++
	p.queueName = queueName
	p.payload = payload
	return p.err
}

func TestP9a9cAdminBulkCancel_PublishesToQueue(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.countVal = 3
	fx.repo.sumVal = "150000"
	pub := &p9a9cPublisher{}
	svc := admin.NewAdminService(fx.repo, fx.db, fx.rdb, fx.gw, pub, fx.log, fx.sys)

	eventID := uuid.New()
	adminID := uuid.New()

	resp, err := svc.BulkCancel(context.Background(), adminID, eventID,
		&admin.BulkCancelRequest{Reason: "event cancelled"})
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, 1, pub.calls, "the bulk cancel job must be enqueued exactly once")
	assert.Equal(t, "admin_bulk_cancel", pub.queueName)

	var payload struct {
		EventID string `json:"event_id"`
		AdminID string `json:"admin_id"`
		Reason  string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(pub.payload, &payload))
	assert.Equal(t, eventID.String(), payload.EventID)
	assert.Equal(t, adminID.String(), payload.AdminID)
	assert.Equal(t, "event cancelled", payload.Reason)
}

func TestP9a9cAdminBulkCancel_PublishFailureDoesNotFailTheRequest(t *testing.T) {
	fx := newSvcFixture(t)
	fx.repo.countVal = 1
	fx.repo.sumVal = "100"
	pub := &p9a9cPublisher{err: assert.AnError}
	svc := admin.NewAdminService(fx.repo, fx.db, fx.rdb, fx.gw, pub, fx.log, fx.sys)

	resp, err := svc.BulkCancel(context.Background(), uuid.New(), uuid.New(),
		&admin.BulkCancelRequest{Reason: "publish fails"})
	require.NoError(t, err, "the enqueue is best-effort; the admin request already succeeded")
	assert.Equal(t, "PROCESSING", resp.Status)
	assert.Equal(t, 1, pub.calls)
}
