package worker

import (
	"context"

	"github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
)

// gatewayRefundAdapter adapts adapter.PaymentGateway into refundGateway.
type gatewayRefundAdapter struct {
	gw adapter.PaymentGateway
}

// NewGatewayRefundAdapter wraps a PaymentGateway for the BookingExpiryWorker.
func NewGatewayRefundAdapter(gw adapter.PaymentGateway) refundGateway {
	return &gatewayRefundAdapter{gw: gw}
}

func (a *gatewayRefundAdapter) Refund(ctx context.Context, externalTxID, amount string) (string, error) {
	resp, err := a.gw.Refund(ctx, adapter.RefundRequest{
		ExternalTransactionID: externalTxID,
		Amount:                amount,
		Reason:                "booking_expiry_timeout",
	})
	if err != nil {
		return "", err
	}
	return resp.RefundID, nil
}
