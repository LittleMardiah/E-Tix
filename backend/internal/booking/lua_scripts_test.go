package booking

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadLuaScripts_Success(t *testing.T) {
	rdb, _ := newBookingTestRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	scripts, err := LoadLuaScripts(ctx, rdb)
	require.NoError(t, err)
	require.NotNil(t, scripts)

	assert.NotEmpty(t, scripts.Lock)
	assert.NotEmpty(t, scripts.Release)
	assert.Contains(t, scripts.Lock, "GETBIT")
	assert.Contains(t, scripts.Lock, "SETBIT")
	assert.Contains(t, scripts.Release, "SETBIT")
}
