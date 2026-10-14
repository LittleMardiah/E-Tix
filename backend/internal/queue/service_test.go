package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockEventRepo struct {
	status string
	err    error
}

func (m *mockEventRepo) GetEventStatus(_ context.Context, _ uuid.UUID) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.status, nil
}

type mockCaptchaSvc struct {
	ok  bool
	err error
}

func (m *mockCaptchaSvc) Verify(_ context.Context, _, _ string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.ok, nil
}

func newTestQueueService(t *testing.T, eventRepo EventRepository, captcha CaptchaService) *QueueService {
	t.Helper()

	fanScore, client, _, _ := newTestFanScoreService(t)
	return NewQueueService(fanScore, newTestQueueTokenService(t), captcha, eventRepo, client)
}

func TestService_GetStatus_EventNotFound(t *testing.T) {
	repoErr := errors.New("event not found")
	svc := newTestQueueService(t, &mockEventRepo{err: repoErr}, &mockCaptchaSvc{ok: true})

	resp, err := svc.GetStatus(t.Context(), uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, repoErr)
}

func TestService_GetStatus_EventNotOnSale(t *testing.T) {
	svc := newTestQueueService(t, &mockEventRepo{status: "DRAFT"}, &mockCaptchaSvc{ok: true})

	resp, err := svc.GetStatus(t.Context(), uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrEventNotOnSale)
}

func TestService_GetStatus_Success(t *testing.T) {
	svc := newTestQueueService(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	userID := uuid.New()

	resp, err := svc.GetStatus(t.Context(), userID, uuid.New())

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, estimatedPosition(userID), resp.Position)
	assert.Equal(t, resp.Position+1000, resp.TotalInQueue)
	assert.Equal(t, resp.Position*2, resp.EstimatedWaitSeconds)
	assert.Equal(t, 65, resp.LoyaltyScore)
	assert.Equal(t, TierLow, resp.Tier)
	assert.Equal(t, "Low Priority", resp.TierLabel)
	assert.NotEmpty(t, resp.QueueToken)

	claims, err := svc.queueToken.Validate(resp.QueueToken)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.Subject)
}

func TestService_GetStatus_FanScoreError(t *testing.T) {
	svc := newTestQueueService(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	svc.fanScore.userRepo = &mockUserRepo{err: errors.New("user lookup failed")}

	resp, err := svc.GetStatus(t.Context(), uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "user lookup failed")
}

func TestService_VerifyCaptcha_CaptchaInvalid(t *testing.T) {
	svc := newTestQueueService(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: false})

	resp, err := svc.VerifyCaptcha(t.Context(), uuid.New(), &VerifyCaptchaRequest{
		ChallengeID: "c-1",
		Solution:    "wrong",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrCaptchaInvalid)
}

func TestService_VerifyCaptcha_CaptchaError(t *testing.T) {
	capErr := errors.New("captcha provider unavailable")
	svc := newTestQueueService(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{err: capErr})

	resp, err := svc.VerifyCaptcha(t.Context(), uuid.New(), &VerifyCaptchaRequest{
		ChallengeID: "c-1",
		Solution:    "s",
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, capErr)
}

func TestService_VerifyCaptcha_Success(t *testing.T) {
	svc := newTestQueueService(t, &mockEventRepo{status: "ON_SALE"}, &mockCaptchaSvc{ok: true})
	userID := uuid.New()

	resp, err := svc.VerifyCaptcha(t.Context(), userID, &VerifyCaptchaRequest{
		ChallengeID: "c-1",
		Solution:    "correct",
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Verified)
	assert.NotEmpty(t, resp.QueueToken)

	claims, err := svc.queueToken.Validate(resp.QueueToken)
	require.NoError(t, err)
	assert.Equal(t, userID.String(), claims.Subject)
}
