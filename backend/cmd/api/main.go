package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/LittleMardiah/E-Tix/backend/internal/admin"
	"github.com/LittleMardiah/E-Tix/backend/internal/analytics"
	"github.com/LittleMardiah/E-Tix/backend/internal/auth"
	"github.com/LittleMardiah/E-Tix/backend/internal/booking"
	"github.com/LittleMardiah/E-Tix/backend/internal/config"
	"github.com/LittleMardiah/E-Tix/backend/internal/db"
	"github.com/LittleMardiah/E-Tix/backend/internal/event"
	"github.com/LittleMardiah/E-Tix/backend/internal/gate"
	"github.com/LittleMardiah/E-Tix/backend/internal/middleware"
	"github.com/LittleMardiah/E-Tix/backend/internal/notification"
	"github.com/LittleMardiah/E-Tix/backend/internal/payment"
	paymentadapter "github.com/LittleMardiah/E-Tix/backend/internal/payment/adapter"
	"github.com/LittleMardiah/E-Tix/backend/internal/queue"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
	"github.com/LittleMardiah/E-Tix/backend/internal/resale"
	"github.com/LittleMardiah/E-Tix/backend/internal/ticket"
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

// parseDurationEnv parses a duration env value ("30s", "5m"). Empty or
// invalid values fall back to the given default (P9a-12 TD-F9-17: the
// circuit breaker tuning must never crash the boot).
func parseDurationEnv(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// parseIntEnv parses an int env value. Empty or invalid values fall back to
// the given default.
func parseIntEnv(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
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

	dbMaxConns := 0
	if v := cfg.DBMaxConnections; v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			dbMaxConns = n
		} else {
			logger.Warn("invalid DB_MAX_CONNECTIONS; using default",
				zap.String("value", v), zap.Error(perr))
		}
	}
	pgPool, err := db.NewDBPoolWithOptions(ctx, cfg.DatabaseURL, db.PoolOptions{
		MaxConns:         dbMaxConns,
		StatementTimeout: cfg.DBStatementTimeout,
	})
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

	notifSvc := auth.NewLogNotification(logger, cfg.Environment)
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
	// NOTE: fanWorker.Run is started once below (in the worker goroutine section),
	// not here. Prior duplicate start caused a second consumer on "fan_score".

	fanScoreSvc := queue.NewFanScoreService(rdb, userRepo)
	queueTokenSvc, err := queue.NewQueueTokenService(cfg.JWTPrivateKeyPath, cfg.JWTPublicKeyPath, "e-tix-flash-queue")
	if err != nil {
		logger.Fatal("failed to init queue token service", zap.Error(err))
	}
	var captchaSvc queue.CaptchaService
	if cfg.TestMode == "true" {
		// Dev/CI: mock auto-pass for `test_pass` solution only, NOT for
		// arbitrary input. Never enable autoPass=true in any deployed binary.
		captchaSvc = queue.NewMockCaptchaService(false)
	} else {
		if cfg.TurnstileSecretKey == "" {
			logger.Fatal("TURNSTILE_SECRET_KEY required when TEST_MODE != true")
		}
		captchaSvc = queue.NewTurnstileCaptchaService(cfg.TurnstileSecretKey)
	}
	eventRepo := queue.NewPostgresEventRepository(pgPool)
	queueSvc := queue.NewQueueService(fanScoreSvc, queueTokenSvc, captchaSvc, eventRepo, rdb)
	queueHandler := queue.NewQueueHandler(queueSvc, logger)

	bookingRepo := booking.NewPostgresBookingRepository(pgPool)
	luaScripts, err := booking.LoadLuaScripts(context.Background(), rdb)
	if err != nil {
		logger.Fatal("failed to load lua scripts", zap.Error(err))
	}
	// P9a-12 TD-F9-17: Redis circuit breaker + durable seat-lock queue.
	// While the breaker is open the seat-lock requests are parked in
	// PostgreSQL (table seat_lock_queue) and replayed by the
	// DBQueueSyncWorker once Redis recovered (LOGIC FLOW §9.4).
	redisCB := booking.NewRedisCircuitBreakerWith(
		parseDurationEnv(cfg.CircuitBreakerInterval, 5*time.Second),
		parseDurationEnv(cfg.CircuitBreakerTimeout, 30*time.Second),
		uint32(parseIntEnv(cfg.CircuitBreakerMaxRequests, 5)),
	)
	seatLockQueue := booking.NewPgxSeatLockQueue(pgPool)
	bookingSvc := booking.NewBookingServiceWithCircuitBreaker(bookingRepo, rdb, luaScripts, queueTokenSvc, mq, redisCB, seatLockQueue)
	bookingHandler := booking.NewBookingHandler(bookingSvc, rdb, logger)

	// FASE 10 Epic 2: Zoned mode checkout (Redis DECR + RabbitMQ).
	zonedCh, err := mq.Channel()
	if err != nil {
		logger.Fatal("failed to open rabbitmq channel for zoned booking", zap.Error(err))
	}
	zonedSvc := booking.NewZonedService(rdb, zonedCh)
	zonedHandler := booking.NewZonedHandler(zonedSvc, logger)

	zonedBatchWorker := worker.NewZonedBatchWorker(pgPool, mq.Connection, logger)

	paymentRepo := payment.NewPostgresPaymentObligationRepository(pgPool)
	paymentIdem := payment.NewIdempotencyService(rdb, paymentRepo)
	paymentBookingReader := payment.NewBookingReaderFromRepo(bookingRepo)

	var paymentGateway paymentadapter.PaymentGateway
	if cfg.TestMode == "true" {
		paymentGateway = paymentadapter.NewMockPaymentAdapter("https://mock.pay.local")
	} else {
		paymentGateway = paymentadapter.NewMidtransPaymentAdapter(cfg.MidtransServerKey, cfg.MidtransIsProduction == "true")
	}

	paymentSvc := payment.NewPaymentService(pgPool, paymentRepo, paymentBookingReader, paymentGateway, paymentIdem)

	// FASE 7 P3b: partial wire — resaleRepo + webhook deps only
	// (routes + workers + ResaleService come in P4)
	resaleRepo := resale.NewPostgresResaleRepository(pgPool)
	paymentSvc.SetResaleWebhookDeps(resaleRepo, mq)

	// FASE 7 P4a: full resale wire (service + handler + workers)
	resaleSvc := resale.NewResaleService(resaleRepo, pgPool, mq, paymentGateway, cfg.ResalePlatformFeeRate)
	resaleHandler := resale.NewResaleHandler(resaleSvc)

	paymentHandler := payment.NewPaymentHandler(paymentSvc, paymentRepo, paymentBookingReader, paymentIdem, logger)

	ticketRepo := ticket.NewPostgresTicketRepository(pgPool)
	ticketSvc := ticket.NewTicketService(ticketRepo, redis.NewQRStore(rdb.Client), pgPool)
	ticketHandler := ticket.NewTicketHandler(ticketSvc)

	gateRepo := gate.NewPostgresGateRepository(pgPool)
	gateSvc := gate.NewGateService(gateRepo, pgPool)
	gateHandler := gate.NewGateHandler(gateSvc)

	anRepo := analytics.NewPostgresAnalyticsRepository(pgPool)
	// TD-F6-07: analytics ownership lookup now comes from the event repo.
	catalogRepo := event.NewPostgresEventRepository(pgPool)
	anSvc := analytics.NewAnalyticsService(anRepo, catalogRepo)
	anHandler := analytics.NewAnalyticsHandler(anSvc)

	ticketNotifier := notification.NewLogTicketNotification(logger)
	ticketNotifyWorker := worker.NewTicketNotificationWorker(pgPool, ticketNotifier, rdb, logger, 0)
	qrRegenWorker := worker.NewQRRegenerationWorker(pgPool, rdb, logger, 0)
	feedbackWorker := worker.NewFeedbackLoopWorker(pgPool, anRepo, logger)

	systemUserUUID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	if cfg.SystemUserUUID != "" {
		id, pErr := uuid.Parse(cfg.SystemUserUUID)
		if pErr != nil {
			logger.Warn("SystemUserUUID parse failed; using zero UUID fallback",
				zap.String("raw", cfg.SystemUserUUID), zap.Error(pErr))
		} else {
			systemUserUUID = id
		}
	}

	bookingExpiryRepo := worker.NewPgxBookingExpiryRepo(pgPool)
	refundAdapter := worker.NewGatewayRefundAdapter(paymentGateway)
	bookingExpiryWorker := worker.NewBookingExpiryWorker(mq, pgPool, bookingExpiryRepo, paymentRepo, bookingSvc, refundAdapter, systemUserUUID, logger)

	// P9a-12 TD-F9-17: drains the durable seat-lock queue once the Redis
	// circuit breaker closed again (LOGIC FLOW §9.4).
	dbQueueSyncWorker := worker.NewDBQueueSyncWorker(seatLockQueue, bookingSvc, func() bool {
		return !bookingSvc.RedisCircuitBreakerOpen()
	}, logger)

	reconciliationRepo := worker.NewPgxReconciliationRepo(pgPool)
	statusFetcher := worker.NewGatewayStatusFetcher(paymentGateway)
	reconciliationWorker := worker.NewWebhookReconciliationWorker(pgPool, paymentRepo, reconciliationRepo, statusFetcher, systemUserUUID, logger)

	catalogSvc := event.NewEventService(catalogRepo)
	catalogHandler := event.NewEventHandler(catalogSvc)

	// FASE 8 P8h: admin module wire
	adminRepo := admin.NewPostgresAdminRepository(pgPool)
	adminSvc := admin.NewAdminService(adminRepo, pgPool, rdb.Client, paymentGateway, mq, logger, systemUserUUID)
	adminH := admin.NewAdminHandler(adminSvc)
	admin2fa := admin.NewTwoFAService("E-Tix Flash")
	admin2faMiddleware := admin.Admin2FAMiddleware(rdb.Client, admin2fa, logger)
	bulkCancelWorker := worker.NewBulkCancelWorker(pgPool, rdb.Client, paymentGateway, logger, mq)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	router := gin.New()
	// H2 FIX: gin default trusts ALL proxies (0.0.0.0/0) → c.ClientIP() can be
	// spoofed via X-Forwarded-For, breaking per-IP rate limits (auth login 5/min).
	// Empty list = trust no proxy = use direct RemoteAddr. Production proxy
	// configuration deferred to TD-F0-02 (TRUSTED_PROXIES env).
	if err := router.SetTrustedProxies([]string{}); err != nil {
		logger.Warn("failed to set trusted proxies", zap.Error(err))
	}
	router.Use(middleware.RecoveryMiddleware(logger))
	router.Use(middleware.LoggerMiddleware(logger))
	router.Use(middleware.CORSMiddleware(parseOrigins(cfg.CORSOrigins), logger))
	router.Use(middleware.PrometheusMiddleware())

	router.POST("/auth/register", auth.RateLimitMiddlewareScoped(rdb, 10, time.Hour, auth.RateLimitScopeIP), authHandler.Register)
	router.POST("/auth/login", auth.RateLimitMiddlewareScoped(rdb, 5, time.Minute, auth.RateLimitScopeIP), authHandler.Login)
	router.POST("/auth/refresh-token", auth.RateLimitMiddlewareScoped(rdb, 10, 15*time.Minute, auth.RateLimitScopeUserViaToken), authHandler.RefreshToken)

	router.POST("/auth/verify-otp", auth.AuthMiddleware(jwtSvc, blacklistSvc), auth.RateLimitMiddlewareScoped(rdb, 5, 15*time.Minute, auth.RateLimitScopeUser), authHandler.VerifyOTP)
	router.POST("/auth/logout", auth.AuthMiddleware(jwtSvc, blacklistSvc), auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser), authHandler.Logout)
	router.GET("/users/me", auth.AuthMiddleware(jwtSvc, blacklistSvc), auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser), authHandler.GetMe)

	router.GET("/queue/status",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		queue.IngressMiddleware(queue.IngressConfig{
			FanScore:         fanScoreSvc,
			QueueTokenIssuer: queueTokenSvc,
			Publisher:        mq,
		}),
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser),
		queueHandler.GetStatus)
	router.POST("/queue/verify-captcha",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 3, 5*time.Minute, auth.RateLimitScopeIP),
		queueHandler.VerifyCaptcha)

	router.GET("/events/:event_id/sections/:section_id/seats",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser),
		bookingHandler.GetSeatAvailability)
	router.POST("/events/:event_id/sections/:section_id/seats",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 10, time.Minute, auth.RateLimitScopeUser),
		bookingHandler.LockSeats)

	// FASE 10 Epic 2: Zoned mode checkout.
	router.POST("/bookings/zoned",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 10, time.Minute, auth.RateLimitScopeUser),
		zonedHandler.CheckoutZoned)

	router.POST("/bookings/:booking_id/payments",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 30, time.Minute, auth.RateLimitScopeUser),
		paymentHandler.InitiatePayments)
	router.GET("/bookings/:booking_id/status",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser),
		paymentHandler.GetBookingStatus)

	// Public webhook — auth via signature (adapter), no rate limit per API_CONTRACT §9.5.
	router.POST("/webhook/payment", paymentHandler.HandleWebhook)

	router.POST("/payments/bulk/assign",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 30, time.Minute, auth.RateLimitScopeUser),
		paymentHandler.BulkAssign)

	router.GET("/tickets",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser),
		ticketHandler.List)
	router.GET("/tickets/:ticket_id",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser),
		ticketHandler.GetByID)
	router.GET("/tickets/:ticket_id/qr",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeUser),
		ticketHandler.GetQR)
	router.PATCH("/tickets/:ticket_id/assign",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 30, time.Minute, auth.RateLimitScopeUser),
		ticketHandler.Assign)
	router.POST("/gate/scan-qr",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RBACMiddleware("gate_staff"),
		auth.RateLimitMiddlewareScoped(rdb, 180, time.Minute, auth.RateLimitScopeUser),
		gateHandler.ScanQR)
	router.POST("/gate/verify-pin",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RBACMiddleware("gate_staff"),
		auth.RateLimitMiddlewareScoped(rdb, 180, time.Minute, auth.RateLimitScopeUser),
		gateHandler.VerifyPIN)
	router.POST("/gate/sync",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RBACMiddleware("gate_staff"),
		auth.RateLimitMiddlewareScoped(rdb, 60, time.Minute, auth.RateLimitScopeUser),
		gateHandler.SyncOfflineScans)

	router.GET("/events/:event_id/dashboard",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RBACMiddleware("event_organizer", "admin"),
		auth.RateLimitMiddlewareScoped(rdb, 30, time.Minute, auth.RateLimitScopeUser),
		anHandler.GetDashboard)

	router.GET("/events/:event_id/analytics",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RBACMiddleware("event_organizer", "admin"),
		auth.RateLimitMiddlewareScoped(rdb, 30, time.Minute, auth.RateLimitScopeUser),
		anHandler.GetAnalytics)

	router.GET("/events/:event_id/post-event-report",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RBACMiddleware("event_organizer", "admin"),
		auth.RateLimitMiddlewareScoped(rdb, 10, time.Minute, auth.RateLimitScopeUser),
		anHandler.GetPostEventReport)

	router.GET("/events",
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeIP),
		catalogHandler.List)
	router.GET("/events/:event_id",
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeIP),
		catalogHandler.GetByID)

	// FASE 10 Epic 5: EO custom map layout.
	router.PUT("/eo/events/:event_id/layout",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RBACMiddleware("event_organizer", "admin"),
		auth.RateLimitMiddlewareScoped(rdb, 30, time.Minute, auth.RateLimitScopeUser),
		catalogHandler.UpdateLayout)

	// FIX P9a-8 TD-F0-01: Prometheus scrape endpoint. Registered before the
	// catch-all groups so it is never rate-limited or role-gated.
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

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

	// FASE 7 P4a: resale routes (API_CONTRACT §12)
	router.POST("/resale",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 10, time.Minute, auth.RateLimitScopeUser),
		resale.IdempotencyMiddleware(rdb, 24*time.Hour),
		resaleHandler.List)
	router.GET("/resale",
		auth.RateLimitMiddlewareScoped(rdb, 100, time.Minute, auth.RateLimitScopeIP),
		resaleHandler.Browse) // public browse
	router.POST("/resale/:id/buy",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 10, time.Minute, auth.RateLimitScopeUser),
		resale.IdempotencyMiddleware(rdb, 24*time.Hour),
		resaleHandler.Buy)
	router.DELETE("/resale/listings/:id",
		auth.AuthMiddleware(jwtSvc, blacklistSvc),
		auth.RateLimitMiddlewareScoped(rdb, 10, time.Minute, auth.RateLimitScopeUser),
		resale.IdempotencyMiddleware(rdb, 24*time.Hour),
		resaleHandler.Cancel)

	// FASE 8 P8h: admin routes (11 endpoints, API_CONTRACT §14)
	adminGroup := router.Group("/admin")
	adminGroup.Use(auth.AuthMiddleware(jwtSvc, blacklistSvc))
	adminGroup.Use(auth.RBACMiddleware("admin", "event_organizer"))
	adminGroup.Use(auth.RateLimitMiddlewareScoped(rdb, 30, time.Minute, auth.RateLimitScopeUser))
	{
		adminGroup.POST("/events/:event_id/bulk-cancel", admin.IdempotencyMiddleware(rdb, 24*time.Hour), admin2faMiddleware, adminH.BulkCancel)
		adminGroup.GET("/ledger", adminH.GetLedger)
		adminGroup.POST("/ledger/reverse", admin.IdempotencyMiddleware(rdb, 24*time.Hour), admin2faMiddleware, adminH.ManualReverse)
		adminGroup.GET("/stuck-locks", adminH.GetStuckLocks)
		adminGroup.POST("/force-release", admin.IdempotencyMiddleware(rdb, 24*time.Hour), admin2faMiddleware, adminH.ForceRelease)
		adminGroup.GET("/users", adminH.ListUsers)
		adminGroup.PATCH("/users/:user_id/suspend", admin2faMiddleware, adminH.SuspendUser)
		adminGroup.PATCH("/users/:user_id/unsuspend", admin2faMiddleware, adminH.UnsuspendUser)
		adminGroup.GET("/blacklist", adminH.ListBlacklist)
		adminGroup.POST("/blacklist", admin.IdempotencyMiddleware(rdb, 24*time.Hour), adminH.AddBlacklist)
		adminGroup.DELETE("/blacklist/:id", admin.IdempotencyMiddleware(rdb, 24*time.Hour), adminH.RemoveBlacklist)
	}

	addr := ":" + port
	go func() {
		if err := fanWorker.Run(context.Background()); err != nil {
			logger.Error("fan worker stopped", zap.Error(err))
		}
	}()
	go func() {
		if err := bookingExpiryWorker.Run(context.Background()); err != nil {
			logger.Error("booking expiry worker stopped", zap.Error(err))
		}
	}()
	go func() {
		if err := reconciliationWorker.Run(context.Background()); err != nil {
			logger.Error("reconciliation worker stopped", zap.Error(err))
		}
	}()
	go func() {
		if err := ticketNotifyWorker.Run(context.Background()); err != nil {
			logger.Error("ticket notify worker stopped", zap.Error(err))
		}
	}()
	go func() {
		if err := qrRegenWorker.Run(context.Background()); err != nil {
			logger.Error("qr regen worker stopped", zap.Error(err))
		}
	}()
	go func() {
		if err := feedbackWorker.Run(context.Background()); err != nil {
			logger.Error("feedback loop worker stopped", zap.Error(err))
		}
	}()
	// FASE 9 P9a-7 TD-F8-01: bulk cancel worker is a real MQ consumer now
	// (admin BulkCancel publishes to the "admin_bulk_cancel" queue).
	go func() {
		if err := bulkCancelWorker.Run(ctx); err != nil {
			logger.Error("bulk cancel worker stopped", zap.Error(err))
		}
	}()
	// FASE 10 Epic 3: Zoned batch worker (pgx.CopyFrom).
	go func() {
		if err := zonedBatchWorker.Run(ctx); err != nil {
			logger.Error("zoned batch worker stopped", zap.Error(err))
		}
	}()
	// P9a-12 TD-F9-17: DB queue sync worker (drains the parked seat locks).
	go func() {
		if err := dbQueueSyncWorker.Run(ctx); err != nil {
			logger.Error("db queue sync worker stopped", zap.Error(err))
		}
	}()

	// FASE 7 P4a: resale workers
	resaleExpiryWorker := worker.NewResaleExpiryWorker(pgPool, resaleRepo, logger)
	payoutWorker := worker.NewPayoutWorker(pgPool, resaleRepo, paymentGateway, logger, cfg, mq)

	go func() {
		if err := resaleExpiryWorker.Run(ctx); err != nil {
			logger.Error("resale expiry worker stopped", zap.Error(err))
		}
	}()
	go func() {
		if err := payoutWorker.Run(ctx); err != nil {
			logger.Error("payout worker stopped", zap.Error(err))
		}
	}()

	logger.Info("starting server",
		zap.String("environment", cfg.Environment),
		zap.String("addr", addr),
	)
	if err := router.Run(addr); err != nil {
		logger.Fatal("server failed to start", zap.Error(err))
	}
}
