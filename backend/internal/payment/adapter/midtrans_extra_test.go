package adapter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// Extra Midtrans adapter tests — P9a-5
// Covers the transport-failure paths: a RoundTripper that fails the
// request, and a response body that fails mid-read.
// ============================================================

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// failingTransport always fails the request.
var failingTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
})

// brokenBody fails on the first read.
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("unexpected EOF") }
func (brokenBody) Close() error             { return nil }

// newTransportAdapter builds an adapter whose HTTP client uses the given
// transport.
func newTransportAdapter(rt http.RoundTripper) *MidtransPaymentAdapter {
	return &MidtransPaymentAdapter{
		serverKey:  "test-key",
		baseURL:    midtransSandboxURL,
		httpClient: &http.Client{Transport: rt},
	}
}

func TestMidtrans_Initiate_TransportFailureIsTransient(t *testing.T) {
	m := newTransportAdapter(failingTransport)

	_, err := m.Initiate(context.Background(), InitiateRequest{
		Amount:    "1000.00",
		BookingID: "bk-1",
		Method:    "VA",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTransient)
	assert.Contains(t, err.Error(), "http do")
}

func TestMidtrans_Refund_TransportFailureIsTransient(t *testing.T) {
	m := newTransportAdapter(failingTransport)

	_, err := m.Refund(context.Background(), RefundRequest{
		ExternalTransactionID: "etix-1",
		Amount:                "1000.00",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTransient)
}

func TestMidtrans_GetStatus_TransportFailureIsTransient(t *testing.T) {
	m := newTransportAdapter(failingTransport)

	_, err := m.GetStatus(context.Background(), "etix-1")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTransient)
}

func TestMidtrans_DoJSON_ResponseBodyReadFailure(t *testing.T) {
	m := newTransportAdapter(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       brokenBody{},
			Header:     make(http.Header),
		}, nil
	}))

	_, err := m.Initiate(context.Background(), InitiateRequest{
		Amount:    "1000.00",
		BookingID: "bk-1",
		Method:    "VA",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read body")
}

func TestMidtrans_GetStatus_SendsGetWithoutPayload(t *testing.T) {
	// GetStatus issues a GET with no request body; the path and auth header
	// must still be built correctly.
	var gotMethod, gotPath, gotAuth, gotContentType string
	m := newTransportAdapter(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"transaction_status":"settlement"}`)),
			Header:     make(http.Header),
		}, nil
	}))

	resp, err := m.GetStatus(context.Background(), "etix-1")

	require.NoError(t, err)
	assert.Equal(t, "settlement", resp.TransactionStatus)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/v2/etix-1/status", gotPath)
	assert.NotEmpty(t, gotAuth)
	assert.Empty(t, gotContentType, "a bodyless GET must not declare a content type")
}
