package adapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// MockPaymentAdapter is a development / CI-only PaymentGateway that
// auto-approves every call. It never contacts a real provider.
//
// Activation: set TEST_MODE=true in the environment and pick Mock in
// the composition root (cmd/api/main.go).
//
// Webhook signature: MockValidateSignature accepts ANY non-empty signature
// and logs a warning at the call site.
type MockPaymentAdapter struct {
	// baseURL is used only to build deterministic-looking payment links.
	baseURL string
}

// NewMockPaymentAdapter returns a new mock bound to a base payment URL.
func NewMockPaymentAdapter(baseURL string) *MockPaymentAdapter {
	if baseURL == "" {
		baseURL = "https://mock.pay.local"
	}
	return &MockPaymentAdapter{baseURL: baseURL}
}

func (m *MockPaymentAdapter) Initiate(ctx context.Context, req InitiateRequest) (*InitiateResponse, error) {
	if req.Amount == "" {
		return nil, fmt.Errorf("mock: amount is required")
	}
	txID := "mock-tx-" + randomHex(8)
	return &InitiateResponse{
		TransactionID: txID,
		PaymentLink:   m.baseURL + "/pay/" + txID,
		ExpiresAt:     time.Now().Add(15 * time.Minute),
	}, nil
}

func (m *MockPaymentAdapter) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	if req.ExternalTransactionID == "" {
		return nil, fmt.Errorf("mock: external_transaction_id is required")
	}
	return &RefundResponse{RefundID: "mock-refund-" + randomHex(8)}, nil
}

func (m *MockPaymentAdapter) Payout(ctx context.Context, req PayoutRequest) (*PayoutResponse, error) {
	if req.RecipientID == "" {
		return nil, fmt.Errorf("mock: recipient_id is required")
	}
	return &PayoutResponse{PayoutID: "mock-payout-" + randomHex(8)}, nil
}

// ValidateWebhookSignature in mock mode accepts any non-empty signature
// found in headers (primary) or payload body (fallback).
func (m *MockPaymentAdapter) ValidateWebhookSignature(headers map[string]string, payload []byte) error {
	if v := headers["X-Gateway-Signature"]; v != "" {
		return nil
	}
	if len(payload) > 0 {
		return nil // mock: body signature treated as present
	}
	return fmt.Errorf("mock: missing signature (neither header nor body)")
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
