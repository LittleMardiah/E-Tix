package queue

// FIX P9a-9b — unit coverage boost for the queue package.
//
// Scope: CAPTCHA verification + connection health. AMQP publish/consume
// paths need a live RabbitMQ broker and are covered by the integration suite.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p9a9RoundTripper intercepts the Turnstile siteverify call so the test
// never touches the network.
type p9a9RoundTripper struct {
	body    string
	err     error
	gotBody string
	gotURL  string
	calls   int
}

func (rt *p9a9RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls++
	rt.gotURL = req.URL.String()
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		rt.gotBody = string(b)
	}
	if rt.err != nil {
		return nil, rt.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(rt.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// ============================================================
// MockCaptchaService
// ============================================================

func TestP9a9MockCaptchaService_AutoPassAcceptsAnySolution(t *testing.T) {
	svc := NewMockCaptchaService(true)
	require.NotNil(t, svc)

	ok, err := svc.Verify(context.Background(), "challenge-1", "anything")

	require.NoError(t, err)
	assert.True(t, ok)
}

func TestP9a9MockCaptchaService_TestPassSolution(t *testing.T) {
	svc := NewMockCaptchaService(false)

	ok, err := svc.Verify(context.Background(), "challenge-1", "test_pass")

	require.NoError(t, err)
	assert.True(t, ok)
}

func TestP9a9MockCaptchaService_RejectsWrongSolution(t *testing.T) {
	svc := NewMockCaptchaService(false)

	ok, err := svc.Verify(context.Background(), "challenge-1", "wrong")

	require.NoError(t, err)
	assert.False(t, ok)
}

// ============================================================
// TurnstileCaptchaService
// ============================================================

func TestP9a9TurnstileCaptchaService_EmptySecretKeyFails(t *testing.T) {
	svc := NewTurnstileCaptchaService("")
	require.NotNil(t, svc)

	ok, err := svc.Verify(context.Background(), "challenge-1", "solution")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret key empty")
	assert.False(t, ok)
}

func TestP9a9TurnstileCaptchaService_VerifySuccess(t *testing.T) {
	rt := &p9a9RoundTripper{body: `{"success":true}`}
	svc := NewTurnstileCaptchaService("secret-key")
	svc.httpClient = &http.Client{Transport: rt}

	ok, err := svc.Verify(context.Background(), "challenge-1", "solution")

	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 1, rt.calls)
	assert.Contains(t, rt.gotBody, "secret-key")
	assert.Contains(t, rt.gotBody, "solution")
}

func TestP9a9TurnstileCaptchaService_VerifyRejection(t *testing.T) {
	rt := &p9a9RoundTripper{body: `{"success":false}`}
	svc := NewTurnstileCaptchaService("secret-key")
	svc.httpClient = &http.Client{Transport: rt}

	ok, err := svc.Verify(context.Background(), "challenge-1", "solution")

	require.NoError(t, err)
	assert.False(t, ok)
}

func TestP9a9TurnstileCaptchaService_VerifyMalformedResponse(t *testing.T) {
	rt := &p9a9RoundTripper{body: `not-json`}
	svc := NewTurnstileCaptchaService("secret-key")
	svc.httpClient = &http.Client{Transport: rt}

	ok, err := svc.Verify(context.Background(), "challenge-1", "solution")

	require.Error(t, err)
	assert.False(t, ok)
}

func TestP9a9TurnstileCaptchaService_VerifyTransportError(t *testing.T) {
	rt := &p9a9RoundTripper{err: io.ErrUnexpectedEOF}
	svc := NewTurnstileCaptchaService("secret-key")
	svc.httpClient = &http.Client{Transport: rt}

	ok, err := svc.Verify(context.Background(), "challenge-1", "solution")

	require.Error(t, err)
	assert.False(t, ok)
}

// ============================================================
// Connection
// ============================================================

// A nil broker connection must be reported as unhealthy, not panic.
func TestP9a9Connection_HealthOnNilConnection(t *testing.T) {
	c := &Connection{}

	err := c.Health(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")
}

// An unreachable broker must surface the dial error to the caller.
func TestP9a9NewConnection_InvalidBrokerURL(t *testing.T) {
	conn, err := NewConnection("amqp://127.0.0.1:1/")

	require.Error(t, err)
	assert.Nil(t, conn)
}
