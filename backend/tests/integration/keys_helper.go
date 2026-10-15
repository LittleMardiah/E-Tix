package integration

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"github.com/stretchr/testify/require"
)

// generateIntegrationKeys returns a fresh RSA 2048-bit key pair for use in
// integration tests. Tests must not depend on PEM files on disk: those are
// gitignored, so CI (fresh checkout) has no access to them.
//
// Rationale: FASE 1 fix applied this pattern to unit tests only; integration
// tests were left reading ../../keys/*.pem and blew up on CI (retro-audit).
func generateIntegrationKeys(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}
