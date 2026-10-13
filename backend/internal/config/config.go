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

		JWTPrivateKey: os.Getenv("JWT_PRIVATE_KEY"),
		JWTPublicKey:  os.Getenv("JWT_PUBLIC_KEY"),

		JWTPrivateKeyPath: os.Getenv("JWT_PRIVATE_KEY_PATH"),
		JWTPublicKeyPath:  os.Getenv("JWT_PUBLIC_KEY_PATH"),

		PaymentGatewayURL:    os.Getenv("PAYMENT_GATEWAY_URL"),
		PaymentGatewayAPIKey: os.Getenv("PAYMENT_GATEWAY_API_KEY"),

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
	if cfg.JWTPrivateKey == "" {
		missing = append(missing, "JWT_PRIVATE_KEY")
	}
	if cfg.JWTPublicKey == "" {
		missing = append(missing, "JWT_PUBLIC_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	return cfg, nil
}
