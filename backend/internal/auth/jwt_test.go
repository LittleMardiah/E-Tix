package auth

// STP traceability: UT-AUTH-003 (mapped below) — UT-AUTH-001/002 have no
// matching test yet (gap tracked in TD-F5-09).

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPrivKeyPath = "../../keys/jwt_private.pem"
	testPubKeyPath  = "../../keys/jwt_public.pem"
	testIssuer      = "etix-test"
)

func generateTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func writeTestKeys(t *testing.T) (string, string) {
	t.Helper()
	key := generateTestRSAKey(t)
	dir := t.TempDir()

	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	privPath := filepath.Join(dir, "jwt_private.pem")
	privFile, err := os.Create(privPath)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(privFile, &pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	require.NoError(t, privFile.Close())

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPath := filepath.Join(dir, "jwt_public.pem")
	pubFile, err := os.Create(pubPath)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(pubFile, &pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	require.NoError(t, pubFile.Close())

	return privPath, pubPath
}

func newTestJWTService(t *testing.T) *JWTService {
	t.Helper()
	key := generateTestRSAKey(t)
	return &JWTService{priv: key, pub: &key.PublicKey, issuer: testIssuer}
}

func newTestUser() *User {
	return &User{
		ID:    uuid.New(),
		Email: "jwt-test@test.local",
		Role:  "customer",
	}
}

func TestNewJWTService_ValidKeys(t *testing.T) {
	privPath, pubPath := writeTestKeys(t)
	svc, err := NewJWTService(privPath, pubPath, testIssuer)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.NotEmpty(t, svc.issuer)
	assert.Equal(t, testIssuer, svc.issuer)
	assert.NotNil(t, svc.priv)
	assert.NotNil(t, svc.pub)
}

func TestNewJWTService_EmptyIssuer(t *testing.T) {
	svc, err := NewJWTService(testPrivKeyPath, testPubKeyPath, "")
	require.Error(t, err)
	assert.Nil(t, svc)
}

func TestGenerateAccessToken_ValidFormat(t *testing.T) {
	svc := newTestJWTService(t)
	user := newTestUser()

	token, err := svc.GenerateAccessToken(user, "TIER_2")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	assert.Len(t, strings.Split(token, "."), 3)

	claims, err := svc.ValidateAccessToken(token)
	require.NoError(t, err)
	assert.Equal(t, user.ID.String(), claims.UserID)
	assert.Equal(t, user.ID.String(), claims.Subject)
	assert.Equal(t, user.Email, claims.Email)
	assert.Equal(t, user.Role, claims.Role)
	assert.Equal(t, "TIER_2", claims.Tier)
	assert.Equal(t, "access", claims.TokenType)
	assert.Equal(t, testIssuer, claims.Issuer)
	assert.NotEmpty(t, claims.ID)
}

func TestValidateAccessToken_Valid(t *testing.T) {
	svc := newTestJWTService(t)
	token, err := svc.GenerateAccessToken(newTestUser(), "TIER_1")
	require.NoError(t, err)

	claims, err := svc.ValidateAccessToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	assert.Equal(t, "access", claims.TokenType)
	assert.WithinDuration(t, time.Now().Add(AccessTokenTTL), claims.ExpiresAt.Time, time.Minute)
}

// UT-AUTH-003: ValidateJWT_RefreshUsedAsAccess_ReturnsTokenTypeMismatch
func TestValidateAccessToken_RefreshAsAccess(t *testing.T) {
	svc := newTestJWTService(t)
	refresh, err := svc.GenerateRefreshToken(newTestUser())
	require.NoError(t, err)

	claims, err := svc.ValidateAccessToken(refresh)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenTypeMismatch)
	assert.Nil(t, claims)
}

func TestParseRefreshToken_AccessAsRefresh(t *testing.T) {
	svc := newTestJWTService(t)
	access, err := svc.GenerateAccessToken(newTestUser(), "TIER_3")
	require.NoError(t, err)

	claims, err := svc.ParseRefreshToken(access)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTokenTypeMismatch)
	assert.Nil(t, claims)
}

func TestValidateAccessToken_Tampered(t *testing.T) {
	svc := newTestJWTService(t)
	token, err := svc.GenerateAccessToken(newTestUser(), "TIER_3")
	require.NoError(t, err)

	pos := len(token) / 2
	replacement := byte('A')
	if token[pos] == 'A' {
		replacement = 'B'
	}
	tampered := token[:pos] + string(replacement) + token[pos+1:]

	claims, err := svc.ValidateAccessToken(tampered)
	require.Error(t, err)
	assert.Nil(t, claims)
}
