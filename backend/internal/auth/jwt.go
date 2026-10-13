package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var ErrTokenTypeMismatch = errors.New("jwt: token type mismatch")

func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key %q: %w", path, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode private key %q: no PEM block found", path)
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS8 private key %q: %w", path, err)
	}

	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key %q is not an RSA key", path)
	}

	return key, nil
}

func LoadPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key %q: %w", path, err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode public key %q: no PEM block found", path)
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX public key %q: %w", path, err)
	}

	key, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key %q is not an RSA key", path)
	}

	return key, nil
}

// TokenTTL constants per TDD section 8.
const (
	AccessTokenTTL  = 1 * time.Hour
	RefreshTokenTTL = 7 * 24 * time.Hour
)

// JWTClaims embeds jwt.RegisteredClaims to satisfy the jwt.Claims interface.
// UserID mirrors RegisteredClaims.Subject for callers that expect the flat
// field name used by auth.Claims.
type JWTClaims struct {
	jwt.RegisteredClaims
	UserID    string `json:"-"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Tier      string `json:"tier"`
	TokenType string `json:"token_type"`
}

type JWTService struct {
	priv   *rsa.PrivateKey
	pub    *rsa.PublicKey
	issuer string
}

func NewJWTService(privatePath, publicPath, issuer string) (*JWTService, error) {
	if issuer == "" {
		return nil, fmt.Errorf("jwt: issuer must not be empty")
	}

	priv, err := LoadPrivateKey(privatePath)
	if err != nil {
		return nil, err
	}

	pub, err := LoadPublicKey(publicPath)
	if err != nil {
		return nil, err
	}

	return &JWTService{priv: priv, pub: pub, issuer: issuer}, nil
}

func (s *JWTService) GenerateAccessToken(user *User, tier string) (string, error) {
	return s.generate(user, tier, "access", AccessTokenTTL)
}

func (s *JWTService) GenerateRefreshToken(user *User) (string, error) {
	return s.generate(user, "", "refresh", RefreshTokenTTL)
}

func (s *JWTService) generate(user *User, tier, tokenType string, ttl time.Duration) (string, error) {
	if user == nil {
		return "", fmt.Errorf("jwt: user must not be nil")
	}

	now := time.Now()
	sub := user.ID.String()

	claims := JWTClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ID:        uuid.NewString(),
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		UserID:    sub,
		Email:     user.Email,
		Role:      user.Role,
		Tier:      tier,
		TokenType: tokenType,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(s.priv)
	if err != nil {
		return "", fmt.Errorf("jwt: sign %s token: %w", tokenType, err)
	}

	return signed, nil
}

func (s *JWTService) ValidateAccessToken(tokenString string) (*JWTClaims, error) {
	return s.parse(tokenString, "access")
}

func (s *JWTService) ParseRefreshToken(tokenString string) (*JWTClaims, error) {
	return s.parse(tokenString, "refresh")
}

func (s *JWTService) parse(tokenString, expectedType string) (*JWTClaims, error) {
	claims := &JWTClaims{}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("jwt: unexpected signing method %q", t.Header["alg"])
		}
		return s.pub, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))
	if err != nil {
		return nil, fmt.Errorf("jwt: parse %s token: %w", expectedType, err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("jwt: invalid %s token", expectedType)
	}

	if claims.TokenType != expectedType {
		return nil, ErrTokenTypeMismatch
	}

	claims.UserID = claims.Subject

	return claims, nil
}
