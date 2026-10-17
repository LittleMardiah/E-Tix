// Package adapter defines the PaymentGateway abstraction used by the
// payment service to talk to external payment providers (Midtrans, Xendit,
// or the in-process mock for dev/CI).
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// InitiateRequest is the provider-agnostic charge request.
type InitiateRequest struct {
	UserID    string
	BookingID string
	Amount    string // decimal string, 2 precision
	Method    string // VA | E_WALLET | CREDIT_CARD
}

// InitiateResponse is what the provider returns after a charge request.
type InitiateResponse struct {
	TransactionID string
	PaymentLink   string
	ExpiresAt     time.Time
}

// RefundRequest is the provider-agnostic refund request.
type RefundRequest struct {
	ExternalTransactionID string
	Amount                string
	Reason                string
}

// RefundResponse is what the provider returns after a refund request.
type RefundResponse struct {
	RefundID string
}

// PayoutRequest is the provider-agnostic payout request (used by FASE 7 resale).
type PayoutRequest struct {
	RecipientID string
	Amount      string
}

// PayoutResponse is what the provider returns after a payout request.
type PayoutResponse struct {
	PayoutID string
}

// StatusResponse is the normalized transaction status for reconciliation.
// Provider-specific statuses are mapped to the transaction_status field
// (pending, settlement, capture, deny, cancel, expire, refund, etc.).
type StatusResponse struct {
	TransactionStatus string
	FraudStatus       string
	Amount            string
}

// PaymentGateway is the provider-agnostic payment interface.
//
// Implementations:
//   - MockPaymentAdapter  (dev/CI, TEST_MODE=true)
//   - MidtransPaymentAdapter (production, Sandbox + Live)
type PaymentGateway interface {
	// Initiate starts a charge and returns the external transaction ID + link.
	Initiate(ctx context.Context, req InitiateRequest) (*InitiateResponse, error)

	// Refund reverses a charge identified by its external_transaction_id.
	Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error)

	// Payout sends funds to a seller (used by resale, FASE 7). Interface-only
	// for FASE 4 — never called by the FASE 4 acceptance tests.
	Payout(ctx context.Context, req PayoutRequest) (*PayoutResponse, error)

	// GetStatus queries the provider for the current status of a transaction.
	// Used by the WebhookReconciliationWorker (FASE 4 P4).
	GetStatus(ctx context.Context, externalTxID string) (*StatusResponse, error)

	// ValidateWebhookSignature verifies the raw signature for an inbound
	// webhook. Implementations may extract from headers (primary) or fall
	// back to the body signature field (Midtrans native: "signature_key";
	// generic: "signature") — defensive dual-source, per ROADMAP FASE 4 §3.5.
	ValidateWebhookSignature(headers map[string]string, payload []byte) error
}

// ErrTransient indicates a retryable failure (5xx, timeout, network).
// Callers MAY retry with backoff.
var ErrTransient = errors.New("gateway: transient error")

// ErrPermanent indicates a non-retryable failure (4xx, validation).
// Callers MUST NOT retry.
var ErrPermanent = errors.New("gateway: permanent error")

// ClassifyHTTPError wraps an HTTP response as either ErrTransient (5xx,
// status==0 for network/timeout) or ErrPermanent (4xx). Callers use
// errors.Is to decide retry policy.
func ClassifyHTTPError(status int, body []byte) error {
	if status == 0 || status >= 500 {
		return fmt.Errorf("%w: http %d: %s", ErrTransient, status, truncateForErr(body))
	}
	return fmt.Errorf("%w: http %d: %s", ErrPermanent, status, truncateForErr(body))
}

func truncateForErr(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
