package auth

import (
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

func newTestJWTService(t *testing.T) *JWTService {
	t.Helper()
	svc, err := NewJWTService(testPrivKeyPath, testPubKeyPath, testIssuer)
	require.NoError(t, err)
	require.NotNil(t, svc)
	return svc
}

func newTestUser() *User {
	return &User{
		ID:    uuid.New(),
		Email: "jwt-test@test.local",
		Role:  "customer",
	}
}

func TestNewJWTService_ValidKeys(t *testing.T) {
	svc, err := NewJWTService(testPrivKeyPath, testPubKeyPath, testIssuer)
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
