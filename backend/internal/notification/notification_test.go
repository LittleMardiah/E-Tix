package notification

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Compile-time guarantee that the dev implementation satisfies the contract.
var _ TicketNotification = (*LogTicketNotification)(nil)

// ---------- helpers ----------

// notifObservedLogger returns a logger that records entries in memory.
func notifObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

// notifContextFlatten renders every logged field as text, so a test can
// assert that a secret (e.g. the PIN) never reaches the log stream.
func notifContextFlatten(entry observer.LoggedEntry) string {
	var b strings.Builder
	b.WriteString(entry.Message)
	for _, f := range entry.Context {
		b.WriteString(" ")
		b.WriteString(f.Key)
		b.WriteString("=")
		b.WriteString(f.String)
	}
	return b.String()
}

// ---------- constructor ----------

func TestNewLogTicketNotification_ReturnsInstance(t *testing.T) {
	n := NewLogTicketNotification(zap.NewNop())

	require.NotNil(t, n)
	assert.NotNil(t, n.logger)
}

func TestNewLogTicketNotification_ImplementsTicketNotification(t *testing.T) {
	var iface TicketNotification = NewLogTicketNotification(zap.NewNop())

	require.NotNil(t, iface)
}

// ---------- SendFallbackPIN ----------

func TestSendFallbackPIN_ReturnsNil(t *testing.T) {
	logger, _ := notifObservedLogger()
	n := NewLogTicketNotification(logger)

	err := n.SendFallbackPIN(context.Background(), uuid.New(), "user@test.local", "+628123456789", "A3F8K9L2M1N4")

	assert.NoError(t, err)
}

func TestSendFallbackPIN_LogsDispatchEvent(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)

	err := n.SendFallbackPIN(context.Background(), uuid.New(), "user@test.local", "+628123456789", "A3F8K9L2M1N4")

	require.NoError(t, err)
	require.Equal(t, 1, logs.Len())
	entry := logs.All()[0]
	assert.Contains(t, entry.Message, "ticket fallback PIN dispatched")
	assert.Equal(t, "u***@test.local", entry.ContextMap()["email"])
	assert.Equal(t, "+6***89", entry.ContextMap()["phone"])
}

func TestSendFallbackPIN_DoesNotLogPinValue(t *testing.T) {
	// E5-S1-T12: the PIN must never be written to the log stream.
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)
	pin := "A3F8K9L2M1N4"

	require.NoError(t, n.SendFallbackPIN(context.Background(), uuid.New(), "user@test.local", "+628123456789", pin))
	require.Equal(t, 1, logs.Len())

	flat := notifContextFlatten(logs.All()[0])
	assert.NotContains(t, flat, pin, "PIN value must not be logged")
}

func TestSendFallbackPIN_EmptyPinSkipsLogging(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)

	err := n.SendFallbackPIN(context.Background(), uuid.New(), "user@test.local", "+628123456789", "")

	assert.NoError(t, err)
	assert.Equal(t, 0, logs.Len(), "empty PIN must short-circuit without logging")
}

func TestSendFallbackPIN_LogsUserID(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)
	userID := uuid.New()

	require.NoError(t, n.SendFallbackPIN(context.Background(), userID, "user@test.local", "+628123456789", "PIN123"))

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, userID.String(), logs.All()[0].ContextMap()["user_id"])
}

func TestSendFallbackPIN_MasksEmail(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)

	require.NoError(t, n.SendFallbackPIN(context.Background(), uuid.New(), "john.doe@example.com", "+628123456789", "PIN123"))

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "j***@example.com", logs.All()[0].ContextMap()["email"])
}

func TestSendFallbackPIN_MasksPhone(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)

	require.NoError(t, n.SendFallbackPIN(context.Background(), uuid.New(), "john.doe@example.com", "+628123456789", "PIN123"))

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "+6***89", logs.All()[0].ContextMap()["phone"])
}

func TestSendFallbackPIN_EmptyEmailIsMasked(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)

	require.NoError(t, n.SendFallbackPIN(context.Background(), uuid.New(), "", "+628123456789", "PIN123"))

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "***", logs.All()[0].ContextMap()["email"])
}

func TestSendFallbackPIN_EmptyPhoneIsMasked(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)

	require.NoError(t, n.SendFallbackPIN(context.Background(), uuid.New(), "john.doe@example.com", "", "PIN123"))

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "***", logs.All()[0].ContextMap()["phone"])
}

func TestSendFallbackPIN_MultipleDispatchesAreEachLogged(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)

	for i := 0; i < 3; i++ {
		require.NoError(t, n.SendFallbackPIN(context.Background(), uuid.New(), "user@test.local", "+628123456789", "PIN123"))
	}

	assert.Equal(t, 3, logs.Len())
}

func TestSendFallbackPIN_CanceledContextStillDispatches(t *testing.T) {
	logger, logs := notifObservedLogger()
	n := NewLogTicketNotification(logger)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := n.SendFallbackPIN(ctx, uuid.New(), "user@test.local", "+628123456789", "PIN123")

	assert.NoError(t, err)
	assert.Equal(t, 1, logs.Len())
}

func TestSendFallbackPIN_NilLoggerDoesNotPanic(t *testing.T) {
	// BUG #102 FIX: NewLogTicketNotification now substitutes zap.NewNop() for a
	// nil logger, so a caller that forgot to wire a logger must not crash the
	// request path. Previously this panicked with a nil-pointer dereference.
	n := NewLogTicketNotification(nil)

	require.NotNil(t, n)
	assert.NotNil(t, n.logger, "nil logger must be replaced with a no-op logger")

	assert.NotPanics(t, func() {
		_ = n.SendFallbackPIN(context.Background(), uuid.New(), "user@test.local", "+628123456789", "PIN123")
	})
}

// ---------- maskEmail ----------

func TestMaskEmail_Valid(t *testing.T) {
	assert.Equal(t, "j***@example.com", maskEmail("john.doe@example.com"))
}

func TestMaskEmail_NoAtSign(t *testing.T) {
	assert.Equal(t, "***", maskEmail("not-an-email"))
}

func TestMaskEmail_AtSignAtIndexZero(t *testing.T) {
	assert.Equal(t, "***", maskEmail("@example.com"))
}

func TestMaskEmail_Empty(t *testing.T) {
	assert.Equal(t, "***", maskEmail(""))
}

func TestMaskEmail_UsesLastAtSign(t *testing.T) {
	assert.Equal(t, "j***@mail.example.com", maskEmail("john@mail.example.com"))
}

func TestMaskEmail_SingleCharacterLocalPart(t *testing.T) {
	assert.Equal(t, "j***@example.com", maskEmail("j@example.com"))
}

// ---------- maskPhone ----------

func TestMaskPhone_Valid(t *testing.T) {
	assert.Equal(t, "+6***89", maskPhone("+628123456789"))
}

func TestMaskPhone_ExactlyFourCharacters(t *testing.T) {
	assert.Equal(t, "12***34", maskPhone("1234"))
}

func TestMaskPhone_TooShort(t *testing.T) {
	assert.Equal(t, "***", maskPhone("123"))
}

func TestMaskPhone_Empty(t *testing.T) {
	assert.Equal(t, "***", maskPhone(""))
}
