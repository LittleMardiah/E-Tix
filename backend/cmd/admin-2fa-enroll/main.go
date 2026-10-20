package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
	"github.com/LittleMardiah/E-Tix/backend/internal/config"
)

// FASE 8 P8c — 2FA enrollment CLI.
// Usage: go run ./cmd/admin-2fa-enroll -user-id <uuid> -email admin@example.com
// Flow: generate secret → display QR → validate first code → persist to Redis.

func main() {
	var (
		userIDStr = flag.String("user-id", "", "Admin user UUID (required)")
		email     = flag.String("email", "", "Admin email (for QR account name, optional)")
		force     = flag.Bool("force", false, "Overwrite existing enrollment")
	)
	flag.Parse()

	if *userIDStr == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --user-id is required")
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: config load: %v\n", err)
		os.Exit(1)
	}
	if cfg.RedisURL == "" {
		fmt.Fprintln(os.Stderr, "ERROR: REDIS_URL not set")
		os.Exit(1)
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisURL})
	defer func() { _ = rdb.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: redis ping: %v\n", err)
		os.Exit(1)
	}

	secretKey := fmt.Sprintf(admin.KeyAdmin2FASecret, *userIDStr)

	// Check existing enrollment
	if exists, _ := rdb.Exists(ctx, secretKey).Result(); exists > 0 && !*force {
		fmt.Fprintf(os.Stderr, "ERROR: user %s already enrolled. Use -force to overwrite.\n", *userIDStr)
		os.Exit(1)
	}

	// Generate secret
	accountName := *email
	if accountName == "" {
		accountName = *userIDStr
	}
	svc := admin.NewTwoFAService("E-Tix Flash")
	enroll, err := svc.GenerateSecret(accountName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: generate: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=================================================================")
	fmt.Printf("2FA Enrollment for user: %s\n", *userIDStr)
	fmt.Println("=================================================================")
	fmt.Println()
	fmt.Println("1. Scan QR URL in Google Authenticator / Authy:")
	fmt.Println()
	fmt.Printf("   %s\n", enroll.URL)
	fmt.Println()
	fmt.Println("2. OR enter secret manually:")
	fmt.Println()
	fmt.Printf("   Secret: %s\n", enroll.Secret)
	fmt.Println()
	fmt.Println("3. Enter the 6-digit code from your authenticator:")
	fmt.Print("   Code: ")

	var code string
	_, _ = fmt.Scanln(&code)

	if !svc.Validate(code, enroll.Secret) {
		fmt.Fprintln(os.Stderr, "ERROR: invalid code, enrollment aborted")
		os.Exit(1)
	}

	// Persist (persistent — no TTL)
	if err := rdb.Set(ctx, secretKey, enroll.Secret, 0).Err(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: redis set: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("✅ 2FA enrolled successfully.")
	fmt.Printf("   Redis key: %s\n", secretKey)
	fmt.Println("   Store a backup of the secret in your password manager.")
}
