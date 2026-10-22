package config

// FIX P9a-9b — unit coverage boost for the config package (logger.go).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestP9a9NewLogger_ValidLevel(t *testing.T) {
	logger, err := NewLogger("info")

	require.NoError(t, err)
	require.NotNil(t, logger)
	defer func() { _ = logger.Sync() }()

	logger.Info("p9a9 logger smoke test")
}

func TestP9a9NewLogger_DebugLevel(t *testing.T) {
	logger, err := NewLogger("debug")

	require.NoError(t, err)
	require.NotNil(t, logger)
	defer func() { _ = logger.Sync() }()
}

func TestP9a9NewLogger_InvalidLevelFails(t *testing.T) {
	logger, err := NewLogger("not-a-level")

	require.Error(t, err)
	assert.Nil(t, logger)
	assert.Contains(t, err.Error(), "invalid log level")
}
