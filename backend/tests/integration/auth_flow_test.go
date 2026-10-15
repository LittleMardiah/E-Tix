package integration

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	integrationDefaultPassword = "Passw0rd!"
	// NOTE: key paths removed — integration tests now generate in-memory RSA
	// keys via generateIntegrationKeys(t). PEM files are gitignored and
	// absent on CI. See keys_helper.go.
	integrationIssuer = "etix-integration-test"
)

type authEnv struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	repo      *auth.PostgresUserRepository
	svc       *auth.AuthService
	jwt       *auth.JWTService
	blacklist *auth.TokenBlacklistService
	client    *redis.Client
	mr        *miniredis.Miniredis
}

func setupAuthEnv(t *testing.T) *authEnv {
	t.Helper()

	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		t.Skip("TEST_DB_URL not set; skipping integration tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	require.NoError(t, pool.Ping(ctx))

	seedBytes, err := os.ReadFile("../fixtures/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seedBytes), pgx.QueryExecModeSimpleProtocol)
	require.NoError(t, err)

	mr := miniredis.RunT(t)
	client := &redis.Client{Client: goredis.NewClient(&goredis.Options{Addr: mr.Addr()})}

	key := generateIntegrationKeys(t)
	jwtSvc := auth.NewJWTServiceWithKeys(key, &key.PublicKey, integrationIssuer)

	repo := auth.NewPostgresUserRepository(pool)
	blacklist := auth.NewTokenBlacklistService(client)
	svc := auth.NewAuthService(repo, jwtSvc, client, nil, blacklist)

	env := &authEnv{
		ctx:       ctx,
		pool:      pool,
		repo:      repo,
		svc:       svc,
		jwt:       jwtSvc,
		blacklist: blacklist,
		client:    client,
		mr:        mr,
	}
	t.Cleanup(func() {
		pool.Close()
		_ = client.Close()
	})
	return env
}

func (e *authEnv) newCredentials(t *testing.T) (email, phone, password string) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	email = fmt.Sprintf("itest_%s@test.local", suffix)
	phone = fmt.Sprintf("+628%d", 1000000000+rand.Int63n(9000000000))
	password = integrationDefaultPassword
	t.Cleanup(func() {
		_, _ = e.pool.Exec(e.ctx, "DELETE FROM users WHERE email = $1", email)
	})
	return email, phone, password
}

func (e *authEnv) register(t *testing.T, email, phone, password string) *auth.RegisterResponse {
	t.Helper()
	resp, err := e.svc.Register(e.ctx, &auth.RegisterRequest{
		Email:    email,
		Phone:    phone,
		Name:     "Integration User",
		Password: password,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	return resp
}

func TestRegisterFlow(t *testing.T) {
	env := setupAuthEnv(t)
	email, phone, password := env.newCredentials(t)

	resp := env.register(t, email, phone, password)

	assert.NotEmpty(t, resp.UserID)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, int64(3600), resp.ExpiresIn)

	uid, err := uuid.Parse(resp.UserID)
	require.NoError(t, err)

	user, err := env.repo.GetUserByID(env.ctx, uid)
	require.NoError(t, err)
	assert.Equal(t, email, user.Email)
	assert.Equal(t, phone, user.Phone)
	assert.Equal(t, "customer", user.Role)
	assert.Equal(t, "ACTIVE", user.Status)
	assert.NotEqual(t, password, user.PasswordHash)
}

func TestRegister_DuplicateEmail(t *testing.T) {
	env := setupAuthEnv(t)
	email, phone, password := env.newCredentials(t)
	env.register(t, email, phone, password)

	_, otherPhone, _ := env.newCredentials(t)
	resp, err := env.svc.Register(env.ctx, &auth.RegisterRequest{
		Email:    email,
		Phone:    otherPhone,
		Name:     "Duplicate User",
		Password: password,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrEmailExists)
	assert.Nil(t, resp)
}

func TestLogin_Success(t *testing.T) {
	env := setupAuthEnv(t)
	email, phone, password := env.newCredentials(t)
	env.register(t, email, phone, password)

	resp, err := env.svc.Login(env.ctx, &auth.LoginRequest{Email: email, Password: password})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, email, resp.Email)
	assert.Equal(t, "customer", resp.Role)
	assert.NotEmpty(t, resp.Tier)
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	assert.Equal(t, int64(3600), resp.ExpiresIn)
}

func TestLogin_WrongPassword(t *testing.T) {
	env := setupAuthEnv(t)
	email, phone, password := env.newCredentials(t)
	env.register(t, email, phone, password)

	resp, err := env.svc.Login(env.ctx, &auth.LoginRequest{Email: email, Password: "WrongPassw0rd!"})
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidCredentials)
	assert.Nil(t, resp)
}

func TestLogin_SuspendedUser(t *testing.T) {
	env := setupAuthEnv(t)
	email, phone, password := env.newCredentials(t)
	env.register(t, email, phone, password)

	_, err := env.pool.Exec(env.ctx, "UPDATE users SET status = 'SUSPENDED' WHERE email = $1", email)
	require.NoError(t, err)

	resp, err := env.svc.Login(env.ctx, &auth.LoginRequest{Email: email, Password: password})
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrForbidden)
	assert.Nil(t, resp)
}

func TestRefreshToken_Rotate(t *testing.T) {
	env := setupAuthEnv(t)
	email, phone, password := env.newCredentials(t)
	env.register(t, email, phone, password)

	login, err := env.svc.Login(env.ctx, &auth.LoginRequest{Email: email, Password: password})
	require.NoError(t, err)

	oldClaims, err := env.jwt.ParseRefreshToken(login.RefreshToken)
	require.NoError(t, err)

	rotated, err := env.svc.RefreshToken(env.ctx, login.RefreshToken)
	require.NoError(t, err)
	require.NotNil(t, rotated)
	assert.NotEmpty(t, rotated.AccessToken)
	assert.NotEmpty(t, rotated.RefreshToken)
	assert.NotEqual(t, login.RefreshToken, rotated.RefreshToken)

	blacklisted, err := env.blacklist.IsBlacklisted(env.ctx, oldClaims.ID)
	require.NoError(t, err)
	assert.True(t, blacklisted)

	_, err = env.svc.RefreshToken(env.ctx, login.RefreshToken)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrTokenBlacklisted)
}

func TestLogout_Blacklist(t *testing.T) {
	env := setupAuthEnv(t)
	email, phone, password := env.newCredentials(t)
	env.register(t, email, phone, password)

	login, err := env.svc.Login(env.ctx, &auth.LoginRequest{Email: email, Password: password})
	require.NoError(t, err)

	accessClaims, err := env.jwt.ValidateAccessToken(login.AccessToken)
	require.NoError(t, err)
	refreshClaims, err := env.jwt.ParseRefreshToken(login.RefreshToken)
	require.NoError(t, err)

	require.NoError(t, env.svc.Logout(env.ctx, login.AccessToken, login.RefreshToken))

	for name, jti := range map[string]string{
		"access":  accessClaims.ID,
		"refresh": refreshClaims.ID,
	} {
		blacklisted, err := env.blacklist.IsBlacklisted(env.ctx, jti)
		require.NoError(t, err)
		assert.Truef(t, blacklisted, "%s token jti should be blacklisted", name)
	}
}
