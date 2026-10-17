package adapter

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testServerKey = "test-server-key"

var _ PaymentGateway = (*MidtransPaymentAdapter)(nil)

type capturedRequest struct {
	method      string
	path        string
	auth        string
	contentType string
	body        []byte
}

func captureRequest(r *http.Request) capturedRequest {
	b, _ := io.ReadAll(r.Body)
	return capturedRequest{
		method:      r.Method,
		path:        r.URL.Path,
		auth:        r.Header.Get("Authorization"),
		contentType: r.Header.Get("Content-Type"),
		body:        b,
	}
}

func basicAuthHeader(key string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(key+":"))
}

func newTestAdapter(t *testing.T, handler http.HandlerFunc) *MidtransPaymentAdapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &MidtransPaymentAdapter{
		serverKey:  testServerKey,
		baseURL:    srv.URL,
		httpClient: srv.Client(),
	}
}

func newNoCallAdapter(t *testing.T) *MidtransPaymentAdapter {
	t.Helper()
	return newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP call: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	})
}

func TestMidtransInitiate_Success(t *testing.T) {
	reqCh := make(chan capturedRequest, 1)
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		reqCh <- captureRequest(r)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status_code":"201","status_message":"success","transaction_id":"mid-1","order_id":"o-1","gross_amount":"100000.00","transaction_status":"pending","fraud_status":"accept","va_numbers":[{"bank":"bca","va_number":"1234567890"}],"expiry_time":"2030-01-02 03:04:05"}`))
	})

	resp, err := m.Initiate(context.Background(), InitiateRequest{
		UserID:    "u-1",
		BookingID: "o-1",
		Amount:    "100000.00",
		Method:    "VA",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	got := <-reqCh
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, midtransChargePath, got.path)
	assert.Equal(t, basicAuthHeader(testServerKey), got.auth)
	assert.Equal(t, "application/json", got.contentType)

	var body midtransChargeReq
	require.NoError(t, json.Unmarshal(got.body, &body))
	assert.Equal(t, "bank_transfer", body.PaymentType)
	require.NotNil(t, body.BankTransfer)
	assert.Equal(t, "bca", body.BankTransfer.Bank)
	require.NotNil(t, body.CustomExpiry)
	assert.Equal(t, 15, body.CustomExpiry.ExpiryDuration)
	assert.Equal(t, "minute", body.CustomExpiry.Unit)
	assert.Equal(t, "100000.00", body.TransactionDetail.GrossAmount)
	assert.True(t, strings.HasPrefix(body.TransactionDetail.OrderID, "etix-o-1-"))
	assert.Len(t, strings.TrimPrefix(body.TransactionDetail.OrderID, "etix-o-1-"), 8)

	assert.True(t, strings.HasPrefix(resp.TransactionID, "etix-o-1-"))
	assert.Equal(t, "VA BCA: 1234567890", resp.PaymentLink)
	wantExpiry, err := time.Parse("2006-01-02 15:04:05", "2030-01-02 03:04:05")
	require.NoError(t, err)
	assert.Equal(t, wantExpiry, resp.ExpiresAt)
}

func TestMidtransInitiate_Success200Fallbacks(t *testing.T) {
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status_code":"200","transaction_id":"mid-2","gross_amount":"50000","transaction_status":"pending","expiry_time":"not-a-time"}`))
	})

	resp, err := m.Initiate(context.Background(), InitiateRequest{
		BookingID: "o-2",
		Amount:    "50000",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "VA BCA: unavailable", resp.PaymentLink)
	assert.True(t, resp.ExpiresAt.After(time.Now().Add(14*time.Minute)))
	assert.True(t, resp.ExpiresAt.Before(time.Now().Add(16*time.Minute)))
}

func TestMidtransInitiate_FieldValidation(t *testing.T) {
	m := newNoCallAdapter(t)
	ctx := context.Background()

	t.Run("amount required", func(t *testing.T) {
		_, err := m.Initiate(ctx, InitiateRequest{BookingID: "o-1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "amount is required")
	})

	t.Run("booking_id required", func(t *testing.T) {
		_, err := m.Initiate(ctx, InitiateRequest{Amount: "1000"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "booking_id is required")
	})

	t.Run("unsupported method", func(t *testing.T) {
		_, err := m.Initiate(ctx, InitiateRequest{BookingID: "o-1", Amount: "1000", Method: "E_WALLET"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not supported (VA only in FASE 4)")
	})
}

func TestMidtransInitiate_4xx_Permanent(t *testing.T) {
	longBody := `{"error_messages":["` + strings.Repeat("a", 250) + `"]}`
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(longBody))
	})

	_, err := m.Initiate(context.Background(), InitiateRequest{BookingID: "o-1", Amount: "100000.00"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanent), "want ErrPermanent, got: %v", err)
	assert.False(t, errors.Is(err, ErrTransient))
	assert.Contains(t, err.Error(), "http 400")
	assert.Contains(t, err.Error(), "...", "error body must be truncated at 200 bytes")
}

func TestMidtransInitiate_5xx_Transient(t *testing.T) {
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error_messages":["internal"]}`))
	})

	_, err := m.Initiate(context.Background(), InitiateRequest{BookingID: "o-1", Amount: "100000.00"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTransient), "want ErrTransient, got: %v", err)
	assert.False(t, errors.Is(err, ErrPermanent))
	assert.Contains(t, err.Error(), "http 500")
}

func TestMidtransInitiate_MalformedJSON(t *testing.T) {
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status_code":`))
	})

	_, err := m.Initiate(context.Background(), InitiateRequest{BookingID: "o-1", Amount: "100000.00"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal charge")
	assert.False(t, errors.Is(err, ErrPermanent))
	assert.False(t, errors.Is(err, ErrTransient))
}

func TestMidtransInitiate_EmptyTransactionID(t *testing.T) {
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status_code":"201","transaction_status":"pending","gross_amount":"100000.00"}`))
	})

	_, err := m.Initiate(context.Background(), InitiateRequest{BookingID: "o-1", Amount: "100000.00"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty transaction_id")
}

func TestMidtransInitiate_RejectedTransactionStatus(t *testing.T) {
	for _, status := range []string{"deny", "cancel", "expire", "failure"} {
		t.Run(status, func(t *testing.T) {
			m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"status_code":"201","transaction_id":"mid-3","transaction_status":"` + status + `","gross_amount":"100000.00"}`))
			})
			_, err := m.Initiate(context.Background(), InitiateRequest{BookingID: "o-1", Amount: "100000.00"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "charge rejected")
			assert.Contains(t, err.Error(), "transaction_status=")
		})
	}
}

func TestMidtransInitiate_AmountCrossCheck(t *testing.T) {
	t.Run("mismatch", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"status_code":"201","transaction_id":"mid-4","transaction_status":"pending","gross_amount":"99999.00"}`))
		})
		_, err := m.Initiate(context.Background(), InitiateRequest{BookingID: "o-1", Amount: "100000.00"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "amount mismatch")
	})

	t.Run("unparseable gross_amount", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"status_code":"201","transaction_id":"mid-5","transaction_status":"pending","gross_amount":"abc"}`))
		})
		_, err := m.Initiate(context.Background(), InitiateRequest{BookingID: "o-1", Amount: "100000.00"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse amount")
	})
}

func TestMidtransInitiate_EmptyGrossAmount_Permanent(t *testing.T) {
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status_code":"201","transaction_id":"mid-6","transaction_status":"pending","gross_amount":""}`))
	})

	_, err := m.Initiate(context.Background(), InitiateRequest{BookingID: "o-1", Amount: "100000.00"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanent), "want ErrPermanent, got: %v", err)
	assert.Contains(t, err.Error(), "empty gross_amount")
}

func TestMidtransDoJSON_TimeoutAndErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("timeout classified transient", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		m := &MidtransPaymentAdapter{
			serverKey:  testServerKey,
			baseURL:    srv.URL,
			httpClient: &http.Client{Timeout: 50 * time.Millisecond},
		}
		_, _, err := m.doJSON(ctx, http.MethodGet, "/slow", nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrTransient), "want ErrTransient, got: %v", err)
		assert.Contains(t, err.Error(), "http do")
	})

	t.Run("network error classified transient", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		deadURL := srv.URL
		srv.Close()
		m := &MidtransPaymentAdapter{
			serverKey:  testServerKey,
			baseURL:    deadURL,
			httpClient: &http.Client{Timeout: time.Second},
		}
		_, _, err := m.doJSON(ctx, http.MethodGet, "/x", nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrTransient), "want ErrTransient, got: %v", err)
	})

	t.Run("marshal error", func(t *testing.T) {
		m := newNoCallAdapter(t)
		body, status, err := m.doJSON(ctx, http.MethodPost, midtransChargePath, make(chan int))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshal request")
		assert.Equal(t, 0, status)
		assert.Nil(t, body)
	})

	t.Run("invalid request error", func(t *testing.T) {
		m := newNoCallAdapter(t)
		_, _, err := m.doJSON(ctx, "bad\nmethod", midtransChargePath, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "new request")
	})
}

func TestMidtransRefund_Success(t *testing.T) {
	reqCh := make(chan capturedRequest, 1)
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		reqCh <- captureRequest(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status_code":"200","refund_chargeback_id":987}`))
	})

	resp, err := m.Refund(context.Background(), RefundRequest{
		ExternalTransactionID: "tx-123",
		Amount:                "10000.00",
		Reason:                "customer request",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "midtrans-refund-987", resp.RefundID)

	got := <-reqCh
	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "/v2/tx-123/refund", got.path)
	assert.Equal(t, basicAuthHeader(testServerKey), got.auth)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(got.body, &body))
	assert.Equal(t, "refund-tx-123", body["refund_key"])
	assert.Equal(t, "customer request", body["reason"])
	assert.Equal(t, "10000.00", body["amount"])
}

func TestMidtransRefund_Failures(t *testing.T) {
	ctx := context.Background()

	t.Run("missing external_transaction_id", func(t *testing.T) {
		m := newNoCallAdapter(t)
		_, err := m.Refund(ctx, RefundRequest{Amount: "1.00"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "external_transaction_id is required")
	})

	t.Run("5xx transient", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"upstream"}`))
		})
		_, err := m.Refund(ctx, RefundRequest{ExternalTransactionID: "tx-1"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrTransient), "want ErrTransient, got: %v", err)
	})

	t.Run("4xx permanent", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		})
		_, err := m.Refund(ctx, RefundRequest{ExternalTransactionID: "tx-1"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPermanent), "want ErrPermanent, got: %v", err)
	})

	t.Run("nil refund_chargeback_id permanent", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status_code":"200"}`))
		})
		_, err := m.Refund(ctx, RefundRequest{ExternalTransactionID: "tx-1"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPermanent), "want ErrPermanent, got: %v", err)
		assert.Contains(t, err.Error(), "empty refund_chargeback_id")
	})

	t.Run("zero refund_chargeback_id permanent", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status_code":"200","refund_chargeback_id":0}`))
		})
		_, err := m.Refund(ctx, RefundRequest{ExternalTransactionID: "tx-1"})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPermanent), "want ErrPermanent, got: %v", err)
	})

	t.Run("malformed json", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status_code":`))
		})
		_, err := m.Refund(ctx, RefundRequest{ExternalTransactionID: "tx-1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal refund")
	})
}

func TestMidtransGetStatus_Success(t *testing.T) {
	reqCh := make(chan capturedRequest, 1)
	m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		reqCh <- captureRequest(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status_code":"200","transaction_status":"settlement","fraud_status":"accept","gross_amount":"100000.00"}`))
	})

	resp, err := m.GetStatus(context.Background(), "tx-7")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "settlement", resp.TransactionStatus)
	assert.Equal(t, "accept", resp.FraudStatus)
	assert.Equal(t, "100000.00", resp.Amount)

	got := <-reqCh
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v2/tx-7/status", got.path)
	assert.Equal(t, basicAuthHeader(testServerKey), got.auth)
	assert.Empty(t, got.contentType, "GET must not set Content-Type")
}

func TestMidtransGetStatus_Failures(t *testing.T) {
	ctx := context.Background()

	t.Run("missing external_transaction_id", func(t *testing.T) {
		m := newNoCallAdapter(t)
		_, err := m.GetStatus(ctx, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "external_transaction_id is required")
	})

	t.Run("4xx permanent", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		})
		_, err := m.GetStatus(ctx, "tx-1")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPermanent), "want ErrPermanent, got: %v", err)
	})

	t.Run("5xx transient", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"maintenance"}`))
		})
		_, err := m.GetStatus(ctx, "tx-1")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrTransient), "want ErrTransient, got: %v", err)
	})

	t.Run("empty transaction_status transient", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status_code":"200","gross_amount":"1.00"}`))
		})
		_, err := m.GetStatus(ctx, "tx-1")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrTransient), "want ErrTransient, got: %v", err)
		assert.Contains(t, err.Error(), "empty transaction_status")
	})

	t.Run("malformed json", func(t *testing.T) {
		m := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[1,2,3]{"`))
		})
		_, err := m.GetStatus(ctx, "tx-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal status")
	})
}

func TestMidtransValidateWebhookSignature(t *testing.T) {
	m := &MidtransPaymentAdapter{serverKey: testServerKey}
	orderID, statusCode, grossAmount := "etix-o-1-abcd1234", "200", "100000.00"
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + testServerKey))
	validSig := hex.EncodeToString(sum[:])
	payload := []byte(`{"order_id":"` + orderID + `","status_code":"` + statusCode + `","gross_amount":"` + grossAmount + `"}`)

	t.Run("valid via X-Gateway-Signature", func(t *testing.T) {
		err := m.ValidateWebhookSignature(map[string]string{"X-Gateway-Signature": validSig}, payload)
		assert.NoError(t, err)
	})

	t.Run("valid via X-Midtrans-Signature", func(t *testing.T) {
		err := m.ValidateWebhookSignature(map[string]string{"X-Midtrans-Signature": validSig}, payload)
		assert.NoError(t, err)
	})

	t.Run("valid via body signature_key fallback", func(t *testing.T) {
		body := []byte(`{"order_id":"` + orderID + `","status_code":"` + statusCode + `","gross_amount":"` + grossAmount + `","signature_key":"` + validSig + `"}`)
		err := m.ValidateWebhookSignature(nil, body)
		assert.NoError(t, err)
	})

	t.Run("mismatch", func(t *testing.T) {
		err := m.ValidateWebhookSignature(map[string]string{"X-Gateway-Signature": strings.Repeat("0", 128)}, payload)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature mismatch")
	})

	t.Run("missing signature", func(t *testing.T) {
		err := m.ValidateWebhookSignature(nil, payload)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "signature missing")
	})

	t.Run("missing required fields", func(t *testing.T) {
		short := []byte(`{"order_id":"","status_code":"200","gross_amount":"100000.00"}`)
		err := m.ValidateWebhookSignature(map[string]string{"X-Gateway-Signature": validSig}, short)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required fields")
	})

	t.Run("malformed payload", func(t *testing.T) {
		err := m.ValidateWebhookSignature(map[string]string{"X-Gateway-Signature": validSig}, []byte(`{"order_id":`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal webhook payload")
	})
}

func TestMidtransPayout_NotImplemented(t *testing.T) {
	m := &MidtransPaymentAdapter{serverKey: testServerKey}
	_, err := m.Payout(context.Background(), PayoutRequest{RecipientID: "r-1", Amount: "1000.00"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payout not implemented in FASE 4")
}
