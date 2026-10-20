package admin

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// FASE 8 P8c — Admin2FAMiddleware.
// Chain: AuthMiddleware (jwtSvc, blacklistSvc) → Admin2FAMiddleware → handler.
// Handler retrieves user_id via c.GetString("user_id").

// Admin2FAMiddleware validates X-Admin-2FA-Token header before admin mutations.
// Sets Redis keys:
//
//	admin_2fa:{user_id}           — TOTP secret (persistent, CLI-enrolled)
//	admin_2fa_fails:{user_id}     — fail counter (TTL 15m, rolling)
//	admin_2fa_lockout:{user_id}   — lockout flag (TTL 15m)
//
// Policy: 3 fails in rolling 15m → 15m lockout (SECURITY_SPEC §6.3).
func Admin2FAMiddleware(rdb *redis.Client, twofa *TwoFAService, log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// 1. Extract user_id (set by AuthMiddleware upstream)
		userIDStr := c.GetString("user_id")
		userID, err := uuid.Parse(userIDStr)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{
				"success": false,
				"error":   gin.H{"code": "UNAUTHORIZED", "message": "invalid user context"},
			})
			return
		}

		// 2. Extract 2FA token header
		code := c.GetHeader(HeaderAdmin2FAToken)
		if code == "" {
			c.AbortWithStatusJSON(401, gin.H{
				"success": false,
				"error":   gin.H{"code": "ADMIN_2FA_REQUIRED", "message": "X-Admin-2FA-Token header required"},
			})
			return
		}

		// 3. Check lockout first (cheap, no secret lookup)
		lockoutKey := fmt.Sprintf(KeyAdmin2FALockout, userID.String())
		if exists, _ := rdb.Exists(ctx, lockoutKey).Result(); exists > 0 {
			ttl, _ := rdb.TTL(ctx, lockoutKey).Result()
			if log != nil {
				log.Warn("admin 2fa lockout hit", zap.String("user_id", userID.String()))
			}
			c.AbortWithStatusJSON(429, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "ADMIN_2FA_LOCKED",
					"message": fmt.Sprintf("2FA locked out, retry in %ds", int(ttl.Seconds())),
				},
			})
			return
		}

		// 4. Get stored secret
		secretKey := fmt.Sprintf(KeyAdmin2FASecret, userID.String())
		secret, err := rdb.Get(ctx, secretKey).Result()
		if err == redis.Nil || secret == "" {
			c.AbortWithStatusJSON(422, gin.H{
				"success": false,
				"error":   gin.H{"code": "ADMIN_2FA_NOT_ENROLLED", "message": "2FA not enrolled for this admin"},
			})
			return
		}
		if err != nil {
			if log != nil {
				log.Error("admin 2fa redis get", zap.Error(err))
			}
			c.AbortWithStatusJSON(500, gin.H{
				"success": false,
				"error":   gin.H{"code": "INTERNAL_ERROR", "message": "2FA check failed"},
			})
			return
		}

		// 5. Validate TOTP (constant-time internally)
		if twofa.Validate(code, secret) {
			// Success: reset fail counter (best-effort)
			failsKey := fmt.Sprintf(KeyAdmin2FAFails, userID.String())
			_ = rdb.Del(ctx, failsKey).Err()
			c.Next()
			return
		}

		// 6. Failure: increment counter, maybe trigger lockout
		failsKey := fmt.Sprintf(KeyAdmin2FAFails, userID.String())
		fails, _ := rdb.Incr(ctx, failsKey).Result()
		if fails == 1 {
			_ = rdb.Expire(ctx, failsKey, Admin2FAFailsTTL).Err()
		}
		if fails >= Admin2FAMaxFails {
			_ = rdb.Set(ctx, lockoutKey, "1", Admin2FALockoutTTL).Err()
			// Clear fails (lockout takes over)
			_ = rdb.Del(ctx, failsKey).Err()
			if log != nil {
				log.Warn("admin 2fa lockout triggered",
					zap.String("user_id", userID.String()),
					zap.Int64("fails", fails))
			}
			c.AbortWithStatusJSON(429, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "ADMIN_2FA_LOCKED",
					"message": fmt.Sprintf("too many failed attempts, locked for %ds", int(Admin2FALockoutTTL.Seconds())),
				},
			})
			return
		}

		if log != nil {
			log.Warn("admin 2fa invalid",
				zap.String("user_id", userID.String()),
				zap.Int64("fails", fails))
		}
		c.AbortWithStatusJSON(401, gin.H{
			"success": false,
			"error":   gin.H{"code": "ADMIN_2FA_INVALID", "message": "invalid 2FA token"},
		})
	}
}
