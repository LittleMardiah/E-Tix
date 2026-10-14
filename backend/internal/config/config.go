package config

import (
	"fmt"
	"os"
)

type Config struct {
	Environment string

	DatabaseURL        string
	DBMaxConnections   string
	DBStatementTimeout string

	RedisURL        string
	RedisMaxRetries string

	RabbitMQURL string

	JWTPrivateKey string
	JWTPublicKey  string

	JWTPrivateKeyPath string
	JWTPublicKeyPath  string

	PaymentGatewayURL    string
	PaymentGatewayAPIKey string

	// Midtrans (Sandbox default). Activated when TestMode != "true".
	MidtransServerKey    string
	MidtransClientKey    string
	MidtransIsProduction string

	// PaymentGatewayWebhookSecret is the HMAC secret for generic webhook
	// signature validation (in addition to Midtrans SHA512 formula).
	PaymentGatewayWebhookSecret string

	// TestMode enables MockPaymentAdapter (dev/CI). "true" → mock, else real.
	TestMode string

	TwilioAccountSID string
	TwilioAuthToken  string
	SendgridAPIKey   string

	FrontendURL string
	CORSOrigins string

	LogLevel  string
	SentryDSN string

	CircuitBreakerInterval    string
	CircuitBreakerTimeout     string
	CircuitBreakerMaxRequests string

	ResalePlatformFeeRate string
	SystemUserUUID        string
}

func Load() (*Config, error) {
	cfg := &Config{
		Environment: os.Getenv("ENV"),

		DatabaseURL:        os.Getenv("DATABASE_URL"),
		DBMaxConnections:   os.Getenv("DB_MAX_CONNECTIONS"),
		DBStatementTimeout: os.Getenv("DB_STATEMENT_TIMEOUT"),

		RedisURL:        os.Getenv("REDIS_URL"),
		RedisMaxRetries: os.Getenv("REDIS_MAX_RETRIES"),

		RabbitMQURL: os.Getenv("RABBITMQ_URL"),

		JWTPrivateKey: os.Getenv("JWT_PRIVATE_KEY or JWT_PRIVATE_KEY_PATH"),
		JWTPublicKey:  os.Getenv("JWT_PUBLIC_KEY or JWT_PUBLIC_KEY_PATH"),

		JWTPrivateKeyPath: os.Getenv("JWT_PRIVATE_KEY_PATH"),
		JWTPublicKeyPath:  os.Getenv("JWT_PUBLIC_KEY_PATH"),

		PaymentGatewayURL:    os.Getenv("PAYMENT_GATEWAY_URL"),
		PaymentGatewayAPIKey: os.Getenv("PAYMENT_GATEWAY_API_KEY"),

		MidtransServerKey:           os.Getenv("MIDTRANS_SERVER_KEY"),
		MidtransClientKey:           os.Getenv("MIDTRANS_CLIENT_KEY"),
		MidtransIsProduction:        os.Getenv("MIDTRANS_IS_PRODUCTION"),
		PaymentGatewayWebhookSecret: os.Getenv("PAYMENT_GATEWAY_WEBHOOK_SECRET"),
		TestMode:                    os.Getenv("TEST_MODE"),

		TwilioAccountSID: os.Getenv("TWILIO_ACCOUNT_SID"),
		TwilioAuthToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
		SendgridAPIKey:   os.Getenv("SENDGRID_API_KEY"),

		FrontendURL: os.Getenv("FRONTEND_URL"),
		CORSOrigins: os.Getenv("CORS_ORIGINS"),

		LogLevel:  os.Getenv("LOG_LEVEL"),
		SentryDSN: os.Getenv("SENTRY_DSN"),

		CircuitBreakerInterval:    os.Getenv("CIRCUIT_BREAKER_INTERVAL"),
		CircuitBreakerTimeout:     os.Getenv("CIRCUIT_BREAKER_TIMEOUT"),
		CircuitBreakerMaxRequests: os.Getenv("CIRCUIT_BREAKER_MAX_REQUESTS"),

		ResalePlatformFeeRate: os.Getenv("RESALE_PLATFORM_FEE_RATE"),
		SystemUserUUID:        os.Getenv("SYSTEM_USER_UUID"),
	}

	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	var missing []string
	if cfg.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if cfg.JWTPrivateKey == "" && cfg.JWTPrivateKeyPath == "" {
		missing = append(missing, "JWT_PRIVATE_KEY")
	}
	if cfg.JWTPublicKey == "" && cfg.JWTPublicKeyPath == "" {
		missing = append(missing, "JWT_PUBLIC_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	return cfg, nil
}
