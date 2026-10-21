package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// STP traceability: config loading — no UT-* ID exists in STP v1.1 §3.2 for
// this module (module gap, see TD-F5-09).

func p9a7SetRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/e_tix")
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("JWT_PRIVATE_KEY", "priv")
	t.Setenv("JWT_PUBLIC_KEY", "pub")
}

func TestP9a7_Load_RequiredEnvPresent(t *testing.T) {
	p9a7SetRequiredEnv(t)
	t.Setenv("ENV", "test")
	t.Setenv("DB_MAX_CONNECTIONS", "25")
	t.Setenv("DB_STATEMENT_TIMEOUT", "5000ms")
	t.Setenv("REDIS_MAX_RETRIES", "5")
	t.Setenv("PAYMENT_GATEWAY_URL", "https://pg.example")
	t.Setenv("TEST_MODE", "true")
	t.Setenv("FRONTEND_URL", "http://localhost:5173")
	t.Setenv("CORS_ORIGINS", "http://localhost:5173")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("MIDTRANS_IS_PRODUCTION", "false")
	t.Setenv("TWILIO_ACCOUNT_SID", "sid")
	t.Setenv("TWILIO_AUTH_TOKEN", "token")
	t.Setenv("SENDGRID_API_KEY", "key")
	t.Setenv("TURNSTILE_SECRET_KEY", "turnstile")
	t.Setenv("PAYMENT_GATEWAY_WEBHOOK_SECRET", "whsec")
	t.Setenv("CIRCUIT_BREAKER_INTERVAL", "5s")
	t.Setenv("CIRCUIT_BREAKER_TIMEOUT", "3s")
	t.Setenv("CIRCUIT_BREAKER_MAX_REQUESTS", "100")
	t.Setenv("SYSTEM_USER_UUID", "11111111-1111-1111-1111-111111111111")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "test", cfg.Environment)
	assert.Equal(t, "postgres://u:p@localhost:5432/e_tix", cfg.DatabaseURL)
	assert.Equal(t, "25", cfg.DBMaxConnections)
	assert.Equal(t, "5000ms", cfg.DBStatementTimeout)
	assert.Equal(t, "redis://localhost:6379", cfg.RedisURL)
	assert.Equal(t, "5", cfg.RedisMaxRetries)
	assert.Equal(t, "amqp://guest:guest@localhost:5672/", cfg.RabbitMQURL)
	assert.Equal(t, "priv", cfg.JWTPrivateKey)
	assert.Equal(t, "pub", cfg.JWTPublicKey)
	assert.Equal(t, "https://pg.example", cfg.PaymentGatewayURL)
	assert.Equal(t, "true", cfg.TestMode)
	assert.Equal(t, "false", cfg.MidtransIsProduction)
	assert.Equal(t, "whsec", cfg.PaymentGatewayWebhookSecret)
	assert.Equal(t, "turnstile", cfg.TurnstileSecretKey)
	assert.Equal(t, "sid", cfg.TwilioAccountSID)
	assert.Equal(t, "token", cfg.TwilioAuthToken)
	assert.Equal(t, "key", cfg.SendgridAPIKey)
	assert.Equal(t, "http://localhost:5173", cfg.FrontendURL)
	assert.Equal(t, "http://localhost:5173", cfg.CORSOrigins)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "5s", cfg.CircuitBreakerInterval)
	assert.Equal(t, "3s", cfg.CircuitBreakerTimeout)
	assert.Equal(t, "100", cfg.CircuitBreakerMaxRequests)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", cfg.SystemUserUUID)
}

func TestP9a7_Load_DefaultsApplied(t *testing.T) {
	p9a7SetRequiredEnv(t)
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("RESALE_PLATFORM_FEE_RATE", "")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "info", cfg.LogLevel, "LOG_LEVEL must default to info")
	assert.Equal(t, "0.025", cfg.ResalePlatformFeeRate, "resale fee must default to 0.025")
}

func TestP9a7_Load_ResaleFeeOverride(t *testing.T) {
	p9a7SetRequiredEnv(t)
	t.Setenv("RESALE_PLATFORM_FEE_RATE", "0.05")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "0.05", cfg.ResalePlatformFeeRate)
}

func TestP9a7_Load_MissingAllRequired(t *testing.T) {
	for _, k := range []string{
		"DATABASE_URL", "REDIS_URL", "RABBITMQ_URL",
		"JWT_PRIVATE_KEY", "JWT_PRIVATE_KEY_PATH",
		"JWT_PUBLIC_KEY", "JWT_PUBLIC_KEY_PATH",
	} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "missing required environment variables")
	assert.Contains(t, err.Error(), "DATABASE_URL")
	assert.Contains(t, err.Error(), "REDIS_URL")
	assert.Contains(t, err.Error(), "RABBITMQ_URL")
	assert.Contains(t, err.Error(), "JWT_PRIVATE_KEY")
	assert.Contains(t, err.Error(), "JWT_PUBLIC_KEY")
}

func TestP9a7_Load_MissingRedisURLOnly(t *testing.T) {
	p9a7SetRequiredEnv(t)
	t.Setenv("REDIS_URL", "")

	cfg, err := Load()
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "REDIS_URL")
}

func TestP9a7_Load_JWTKeyPathVariant(t *testing.T) {
	p9a7SetRequiredEnv(t)
	t.Setenv("JWT_PRIVATE_KEY", "")
	t.Setenv("JWT_PUBLIC_KEY", "")
	t.Setenv("JWT_PRIVATE_KEY_PATH", "/etc/jwt/private.pem")
	t.Setenv("JWT_PUBLIC_KEY_PATH", "/etc/jwt/public.pem")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "/etc/jwt/private.pem", cfg.JWTPrivateKeyPath)
	assert.Equal(t, "/etc/jwt/public.pem", cfg.JWTPublicKeyPath)
}

func TestP9a7_GetEnvOrDefault(t *testing.T) {
	t.Setenv("P9A7_ENV_KEY", "value")
	assert.Equal(t, "value", getEnvOrDefault("P9A7_ENV_KEY", "fallback"))

	t.Setenv("P9A7_ENV_KEY", "")
	assert.Equal(t, "fallback", getEnvOrDefault("P9A7_ENV_KEY", "fallback"))
}
