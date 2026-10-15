package worker

import (
	"context"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// gatewayStatusFetcher adapts adapter.PaymentGateway into statusFetcher.
type gatewayStatusFetcher struct {
	gw adapter.PaymentGateway
}

// NewGatewayStatusFetcher wraps a PaymentGateway for the reconciliation worker.
func NewGatewayStatusFetcher(gw adapter.PaymentGateway) statusFetcher {
	return &gatewayStatusFetcher{gw: gw}
}

func (f *gatewayStatusFetcher) GetStatus(ctx context.Context, externalTxID string) (*adapter.StatusResponse, error) {
	return f.gw.GetStatus(ctx, externalTxID)
}
