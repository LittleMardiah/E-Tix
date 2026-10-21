package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// MockPaymentAdapter — P9a-5
// The mock gateway is the TEST_MODE path used by CI and dev, but it had 0%
// coverage. These tests lock down its contract:
//   - required-field validation
//   - deterministic prefixing of ids/links
//   - dual-source webhook signature acceptance
// ============================================================

func TestMockAdapter_ImplementsPaymentGateway(t *testing.T) {
	var gw PaymentGateway = NewMockPaymentAdapter("")
	require.NotNil(t, gw)
}

func TestNewMockPaymentAdapter_DefaultBaseURL(t *testing.T) {
	m := NewMockPaymentAdapter("")
	require.NotNil(t, m)
	assert.Equal(t, "https://mock.pay.local", m.baseURL)
}

func TestNewMockPaymentAdapter_CustomBaseURL(t *testing.T) {
	m := NewMockPaymentAdapter("https://pay.staging.test")
	require.NotNil(t, m)
	assert.Equal(t, "https://pay.staging.test", m.baseURL)
}

func TestMockAdapter_Initiate_RequiresAmount(t *testing.T) {
	m := NewMockPaymentAdapter("")

	resp, err := m.Initiate(context.Background(), InitiateRequest{UserID: "u", BookingID: "b"})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "amount is required")
}

func TestMockAdapter_Initiate_Success(t *testing.T) {
	m := NewMockPaymentAdapter("https://pay.test")

	resp, err := m.Initiate(context.Background(), InitiateRequest{
		UserID: "u", BookingID: "b", Amount: "1000.00", Method: "VA",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, strings.HasPrefix(resp.TransactionID, "mock-tx-"), "tx id must be prefixed: %s", resp.TransactionID)
	assert.Equal(t, "https://pay.test/pay/"+resp.TransactionID, resp.PaymentLink)
	assert.True(t, resp.ExpiresAt.After(time.Now()), "expiry must be in the future")
}

func TestMockAdapter_Initiate_UniqueTransactionIDs(t *testing.T) {
	m := NewMockPaymentAdapter("")

	r1, err := m.Initiate(context.Background(), InitiateRequest{Amount: "1"})
	require.NoError(t, err)
	r2, err := m.Initiate(context.Background(), InitiateRequest{Amount: "1"})
	require.NoError(t, err)

	assert.NotEqual(t, r1.TransactionID, r2.TransactionID, "each initiate must yield a fresh tx id")
}

func TestMockAdapter_Refund_RequiresExternalTransactionID(t *testing.T) {
	m := NewMockPaymentAdapter("")

	resp, err := m.Refund(context.Background(), RefundRequest{Amount: "100"})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "external_transaction_id is required")
}

func TestMockAdapter_Refund_Success(t *testing.T) {
	m := NewMockPaymentAdapter("")

	resp, err := m.Refund(context.Background(), RefundRequest{
		ExternalTransactionID: "mock-tx-abc", Amount: "100", Reason: "test",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, strings.HasPrefix(resp.RefundID, "mock-refund-"), "refund id must be prefixed: %s", resp.RefundID)
}

func TestMockAdapter_Payout_RequiresRecipient(t *testing.T) {
	m := NewMockPaymentAdapter("")

	resp, err := m.Payout(context.Background(), PayoutRequest{Amount: "100"})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "recipient_id is required")
}

func TestMockAdapter_Payout_Success(t *testing.T) {
	m := NewMockPaymentAdapter("")

	resp, err := m.Payout(context.Background(), PayoutRequest{RecipientID: "seller-1", Amount: "100"})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, strings.HasPrefix(resp.PayoutID, "mock-payout-"), "payout id must be prefixed: %s", resp.PayoutID)
}

func TestMockAdapter_GetStatus_RequiresExternalTransactionID(t *testing.T) {
	m := NewMockPaymentAdapter("")

	resp, err := m.GetStatus(context.Background(), "")

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "external_transaction_id is required")
}

func TestMockAdapter_GetStatus_SettlementSuccess(t *testing.T) {
	m := NewMockPaymentAdapter("")

	resp, err := m.GetStatus(context.Background(), "mock-tx-abc")

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "settlement", resp.TransactionStatus)
	assert.Equal(t, "accept", resp.FraudStatus)
}

func TestMockAdapter_ValidateWebhookSignature_AcceptsHeader(t *testing.T) {
	m := NewMockPaymentAdapter("")

	err := m.ValidateWebhookSignature(map[string]string{"X-Gateway-Signature": "sig"}, nil)

	assert.NoError(t, err)
}

func TestMockAdapter_ValidateWebhookSignature_FallsBackToBody(t *testing.T) {
	m := NewMockPaymentAdapter("")

	err := m.ValidateWebhookSignature(map[string]string{}, []byte(`{"signature":"x"}`))

	assert.NoError(t, err, "mock mode treats any body as carrying a signature")
}

func TestMockAdapter_ValidateWebhookSignature_MissingBoth(t *testing.T) {
	m := NewMockPaymentAdapter("")

	err := m.ValidateWebhookSignature(map[string]string{}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing signature")
}

func TestMockAdapter_ValidateWebhookSignature_EmptyHeaderFallsBackToBody(t *testing.T) {
	m := NewMockPaymentAdapter("")

	err := m.ValidateWebhookSignature(map[string]string{"X-Gateway-Signature": ""}, []byte(`{}`))

	assert.NoError(t, err)
}

func TestNewMidtransPaymentAdapter_SandboxByDefault(t *testing.T) {
	m := NewMidtransPaymentAdapter("server-key", false)
	require.NotNil(t, m)
	assert.Equal(t, midtransSandboxURL, m.baseURL)
	assert.Equal(t, "server-key", m.serverKey)
	assert.NotNil(t, m.httpClient)
}

func TestNewMidtransPaymentAdapter_Production(t *testing.T) {
	m := NewMidtransPaymentAdapter("server-key", true)
	require.NotNil(t, m)
	assert.Equal(t, midtransProductionURL, m.baseURL)
}
