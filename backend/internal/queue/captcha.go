package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ChallengeRequiredScoreThreshold is the final score below which CAPTCHA is required.
const ChallengeRequiredScoreThreshold = 50

// CaptchaService verifies CAPTCHA challenges (provider-agnostic).
type CaptchaService interface {
	Verify(ctx context.Context, challengeID, solution string) (bool, error)
}

// MockCaptchaService is a dev/test implementation.
type MockCaptchaService struct {
	autoPass bool
}

func NewMockCaptchaService(autoPass bool) *MockCaptchaService {
	return &MockCaptchaService{autoPass: autoPass}
}

func (m *MockCaptchaService) Verify(ctx context.Context, challengeID, solution string) (bool, error) {
	if m.autoPass {
		return true, nil
	}
	if solution == "test_pass" {
		return true, nil
	}
	return false, nil
}

// TurnstileCaptchaService calls Cloudflare Turnstile siteverify.
type TurnstileCaptchaService struct {
	secretKey  string
	httpClient *http.Client
}

func NewTurnstileCaptchaService(secretKey string) *TurnstileCaptchaService {
	return &TurnstileCaptchaService{
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (t *TurnstileCaptchaService) Verify(ctx context.Context, challengeID, solution string) (bool, error) {
	if t.secretKey == "" {
		return false, fmt.Errorf("turnstile: secret key empty")
	}
	body := map[string]string{
		"secret":   t.secretKey,
		"response": solution,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://challenges.cloudflare.com/turnstile/v0/siteverify",
		bytes.NewReader(buf))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Success, nil
}
