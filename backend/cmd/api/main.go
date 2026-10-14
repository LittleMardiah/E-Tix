package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/config"
	"github.com/LittleMardiah/E-Tix/backend/internal/db"
	"github.com/LittleMardiah/E-Tix/backend/internal/middleware"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
	"github.com/LittleMardiah/E-Tix/backend/internal/worker"
)

func parseOrigins(csv string) []string {
	origins := make([]string, 0)
	for _, part := range strings.Split(csv, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger, err := config.NewLogger(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()

	// Activate global zap logger so zap.L() works across the codebase.
	zap.ReplaceGlobals(logger)

	ctx := context.Background()

	pgPool, err := db.NewDBPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("failed to init postgres pool", zap.Error(err))
	}
	defer pgPool.Close()

	rdb, err := redis.NewClient(cfg.RedisURL)
	if err != nil {
		logger.Fatal("failed to init redis client", zap.Error(err))
	}
	defer func() { _ = rdb.Close() }()

	userRepo := auth.NewPostgresUserRepository(pgPool)

	jwtSvc, err := auth.NewJWTService(cfg.JWTPrivateKeyPath, cfg.JWTPublicKeyPath, "e-tix-flash")
	if err != nil {
		logger.Fatal("failed to init jwt service", zap.Error(err))
	}

	notifSvc := auth.NewLogNotification(logger)
	blacklistSvc := auth.NewTokenBlacklistService(rdb)
	authSvc := auth.NewAuthService(userRepo, jwtSvc, rdb, notifSvc, blacklistSvc)
	authHandler := auth.NewAuthHandler(authSvc, rdb)

	mq, err := queue.NewConnection(cfg.RabbitMQURL)
	if err != nil {
		logger.Fatal("failed to init rabbitmq connection", zap.Error(err))
	}
	defer func() { _ = mq.Close() }()

	blacklistRepo := worker.NewPgxBlacklistRepo(pgPool)
	fanWorker := worker.NewFanScoreWorker(mq, rdb, blacklistRepo, logger)
	go func() { _ = fanWorker.Run(context.Background()) }()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	router := gin.New()
	router.Use(middleware.RecoveryMiddleware(logger))
	router.Use(middleware.LoggerMiddleware(logger))
	router.Use(middleware.CORSMiddleware(parseOrigins(cfg.CORSOrigins), logger))

	router.POST("/auth/register", auth.RateLimitMiddlewareScoped(rdb, 10, time.Hour, auth.RateLimitScopeIP), authHandler.Register)
	router.POST("/auth/login", auth.RateLimitMiddlewareScoped(rdb, 5, time.Minute, auth.RateLimitScopeIP), authHandler.Login)
	router.POST("/auth/refresh-token", auth.RateLimitMiddlewareScoped(rdb, 10, 15*time.Minute, auth.RateLimitScopeUserViaToken), authHandler.RefreshToken)

	router.POST("/auth/verify-otp", auth.AuthMiddleware(jwtSvc, blacklistSvc), auth.RateLimitMiddlewareScoped(rdb, 5, 15*time.Minute, auth.RateLimitScopeUser), authHandler.VerifyOTP)
	router.POST("/auth/logout", auth.AuthMiddleware(jwtSvc, blacklistSvc), auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser), authHandler.Logout)
	router.GET("/users/me", auth.AuthMiddleware(jwtSvc, blacklistSvc), auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser), authHandler.GetMe)

	router.GET("/healthz/liveness", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "alive"})
	})

	router.GET("/healthz/readiness", func(c *gin.Context) {
		checkCtx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		checks := gin.H{
			"database": "ok",
			"redis":    "ok",
			"rabbitmq": "ok",
		}
		ready := true

		if err := pgPool.Ping(checkCtx); err != nil {
			checks["database"] = "fail"
			ready = false
		}
		if err := rdb.Ping(checkCtx).Err(); err != nil {
			checks["redis"] = "fail"
			ready = false
		}
		if mq.IsClosed() {
			checks["rabbitmq"] = "fail"
			ready = false
		}

		if ready {
			c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "checks": checks})
	})

	addr := ":" + port
	logger.Info("starting server",
		zap.String("environment", cfg.Environment),
		zap.String("addr", addr),
	)
	if err := router.Run(addr); err != nil {
		logger.Fatal("server failed to start", zap.Error(err))
	}
}
