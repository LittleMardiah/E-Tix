package queue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockCaptcha_AutoPass(t *testing.T) {
	m := NewMockCaptchaService(true)

	ok, err := m.Verify(context.Background(), "challenge-1", "whatever")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestMockCaptcha_TestPass(t *testing.T) {
	m := NewMockCaptchaService(false)

	ok, err := m.Verify(context.Background(), "challenge-1", "test_pass")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestMockCaptcha_Invalid(t *testing.T) {
	m := NewMockCaptchaService(false)

	ok, err := m.Verify(context.Background(), "challenge-1", "wrong_solution")
	require.NoError(t, err)
	assert.False(t, ok)
}
