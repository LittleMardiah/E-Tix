package adapter

import (
	"bytes"
	"context"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Midtrans base URLs (Core API v2).
const (
	midtransSandboxURL    = "https://api.sandbox.midtrans.com"
	midtransProductionURL = "https://api.midtrans.com"
	midtransDefaultBank   = "bca"
	midtransChargePath    = "/v2/charge"
)

// MidtransPaymentAdapter implements PaymentGateway against Midtrans Core API v2.
//
// FASE 4 MVP scope: Bank Transfer (VA) only. E_WALLET / CREDIT_CARD deferred.
// Sandbox by default; set isProduction=true for live.
type MidtransPaymentAdapter struct {
	serverKey  string
	baseURL    string
	httpClient *http.Client
}

// NewMidtransPaymentAdapter creates an adapter bound to Sandbox or Production.
func NewMidtransPaymentAdapter(serverKey string, isProduction bool) *MidtransPaymentAdapter {
	baseURL := midtransSandboxURL
	if isProduction {
		baseURL = midtransProductionURL
	}
	return &MidtransPaymentAdapter{
		serverKey:  serverKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// ---------- internal shapes (subset for MVP) ----------

type midtransChargeReq struct {
	PaymentType       string                    `json:"payment_type"`
	TransactionDetail midtransTransactionDetail `json:"transaction_details"`
	BankTransfer      *midtransBankTransfer     `json:"bank_transfer,omitempty"`
	CustomExpiry      *midtransCustomExpiry     `json:"custom_expiry,omitempty"`
}

type midtransTransactionDetail struct {
	OrderID     string `json:"order_id"`
	GrossAmount string `json:"gross_amount"`
}

type midtransBankTransfer struct {
	Bank string `json:"bank"`
}

type midtransCustomExpiry struct {
	ExpiryDuration int    `json:"expiry_duration"`
	Unit           string `json:"unit"`
}

type midtransChargeResp struct {
	StatusCode        string             `json:"status_code"`
	StatusMessage     string             `json:"status_message"`
	TransactionID     string             `json:"transaction_id"`
	OrderID           string             `json:"order_id"`
	GrossAmount       string             `json:"gross_amount"`
	TransactionStatus string             `json:"transaction_status"`
	FraudStatus       string             `json:"fraud_status"`
	VANumbers         []midtransVANumber `json:"va_numbers"`
	ExpiryTime        string             `json:"expiry_time"`
}

type midtransVANumber struct {
	Bank     string `json:"bank"`
	VANumber string `json:"va_number"`
}

// ---------- HTTP helpers ----------

func (m *MidtransPaymentAdapter) setAuth(req *http.Request) {
	token := base64.StdEncoding.EncodeToString([]byte(m.serverKey + ":"))
	req.Header.Set("Authorization", "Basic "+token)
	req.Header.Set("Accept", "application/json")
}

func (m *MidtransPaymentAdapter) doJSON(ctx context.Context, method, path string, body interface{}) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("midtrans: marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.baseURL+path, reader)
	if err != nil {
		return nil, 0, fmt.Errorf("midtrans: new request: %w", err)
	}
	m.setAuth(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("midtrans: http do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("midtrans: read body: %w", err)
	}
	return data, resp.StatusCode, nil
}

// ---------- Public API ----------

// Initiate creates a VA charge via POST /v2/charge.
//
// TransactionID stored in payment_obligations.external_transaction_id is
// the Midtrans order_id we generate (unique per charge attempt). It is used
// as the lookup key for webhook + refund + status.
func (m *MidtransPaymentAdapter) Initiate(ctx context.Context, req InitiateRequest) (*InitiateResponse, error) {
	if req.Amount == "" {
		return nil, fmt.Errorf("midtrans: amount is required")
	}
	if req.BookingID == "" {
		return nil, fmt.Errorf("midtrans: booking_id is required")
	}
	if req.Method != "" && req.Method != "VA" {
		return nil, fmt.Errorf("midtrans: method %q not supported (VA only in FASE 4)", req.Method)
	}
	// order_id must be unique per Midtrans charge attempt. SPLIT mode may
	// fire N charges on the same booking within the same second, so a
	// second-resolution timestamp alone collides. Append 4-byte random hex.
	orderID := "etix-" + req.BookingID + "-" + randomHex(4)
	chargeReq := midtransChargeReq{
		PaymentType: "bank_transfer",
		TransactionDetail: midtransTransactionDetail{
			OrderID:     orderID,
			GrossAmount: req.Amount,
		},
		BankTransfer: &midtransBankTransfer{Bank: midtransDefaultBank},
		CustomExpiry: &midtransCustomExpiry{ExpiryDuration: 15, Unit: "minute"},
	}

	data, status, err := m.doJSON(ctx, http.MethodPost, midtransChargePath, chargeReq)
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return nil, fmt.Errorf("midtrans: charge http %d: %s", status, truncate(data, 200))
	}

	var resp midtransChargeResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("midtrans: unmarshal charge: %w", err)
	}
	if resp.TransactionID == "" {
		return nil, fmt.Errorf("midtrans: empty transaction_id in response")
	}

	paymentLink := "VA " + strings.ToUpper(midtransDefaultBank) + ": unavailable"
	if len(resp.VANumbers) > 0 {
		paymentLink = "VA " + strings.ToUpper(resp.VANumbers[0].Bank) + ": " + resp.VANumbers[0].VANumber
	}

	expiresAt := time.Now().Add(15 * time.Minute)
	if resp.ExpiryTime != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", resp.ExpiryTime); err == nil {
			expiresAt = t
		}
	}

	return &InitiateResponse{
		TransactionID: orderID,
		PaymentLink:   paymentLink,
		ExpiresAt:     expiresAt,
	}, nil
}

// Refund calls POST /v2/{order_id}/refund.
func (m *MidtransPaymentAdapter) Refund(ctx context.Context, req RefundRequest) (*RefundResponse, error) {
	if req.ExternalTransactionID == "" {
		return nil, fmt.Errorf("midtrans: external_transaction_id is required")
	}
	payload := map[string]interface{}{
		"refund_key": "refund-" + req.ExternalTransactionID,
		"reason":     req.Reason,
	}
	if req.Amount != "" {
		payload["amount"] = req.Amount
	}
	data, status, err := m.doJSON(ctx, http.MethodPost, "/v2/"+req.ExternalTransactionID+"/refund", payload)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return nil, fmt.Errorf("midtrans: refund http %d: %s", status, truncate(data, 200))
	}
	var resp struct {
		StatusCode         string `json:"status_code"`
		RefundChargebackID int64  `json:"refund_chargeback_id"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("midtrans: unmarshal refund: %w", err)
	}
	return &RefundResponse{RefundID: fmt.Sprintf("midtrans-refund-%d", resp.RefundChargebackID)}, nil
}

// Payout is not implemented in FASE 4 (used by FASE 7 resale).
func (m *MidtransPaymentAdapter) Payout(ctx context.Context, req PayoutRequest) (*PayoutResponse, error) {
	return nil, fmt.Errorf("midtrans: payout not implemented in FASE 4 (see FASE 7)")
}

// GetStatus calls GET /v2/{order_id}/status for reconciliation.
func (m *MidtransPaymentAdapter) GetStatus(ctx context.Context, externalTxID string) (*StatusResponse, error) {
	if externalTxID == "" {
		return nil, fmt.Errorf("midtrans: external_transaction_id is required")
	}
	data, status, err := m.doJSON(ctx, http.MethodGet, "/v2/"+externalTxID+"/status", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("midtrans: status http %d: %s", status, truncate(data, 200))
	}
	var resp midtransChargeResp
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("midtrans: unmarshal status: %w", err)
	}
	return &StatusResponse{
		TransactionStatus: resp.TransactionStatus,
		FraudStatus:       resp.FraudStatus,
		Amount:            resp.GrossAmount,
	}, nil
}

// ValidateWebhookSignature checks the Midtrans webhook signature using the
// SHA512 formula: SHA512(order_id + status_code + gross_amount + ServerKey).
//
// Signature source (defensive, per ROADMAP FASE 4 §3.5 + SEC_SPEC §9.1):
//  1. PRIMARY: header "X-Gateway-Signature" (generic) or "X-Midtrans-Signature"
//  2. FALLBACK: body field "signature_key" (Midtrans native)
func (m *MidtransPaymentAdapter) ValidateWebhookSignature(headers map[string]string, payload []byte) error {
	sig := headers["X-Gateway-Signature"]
	if sig == "" {
		sig = headers["X-Midtrans-Signature"]
	}

	var body struct {
		OrderID      string `json:"order_id"`
		StatusCode   string `json:"status_code"`
		GrossAmount  string `json:"gross_amount"`
		SignatureKey string `json:"signature_key"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return fmt.Errorf("midtrans: unmarshal webhook payload: %w", err)
	}
	if sig == "" {
		sig = body.SignatureKey
	}
	if sig == "" {
		return fmt.Errorf("midtrans: signature missing (header and body)")
	}
	if body.OrderID == "" || body.StatusCode == "" || body.GrossAmount == "" {
		return fmt.Errorf("midtrans: webhook missing required fields for signature")
	}

	raw := body.OrderID + body.StatusCode + body.GrossAmount + m.serverKey
	sum := sha512.Sum512([]byte(raw))
	expected := hex.EncodeToString(sum[:])

	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return fmt.Errorf("midtrans: signature mismatch")
	}
	return nil
}

// ---------- helpers ----------

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
