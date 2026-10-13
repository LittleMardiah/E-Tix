package auth

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
)

const (
	authorizationHeader = "Authorization"
	bearerPrefix        = "Bearer "
)

type AuthConfig struct {
	jwt       *JWTService
	blacklist *TokenBlacklistService
}

func AuthMiddleware(jwt *JWTService, blacklist *TokenBlacklistService) gin.HandlerFunc {
	cfg := AuthConfig{jwt: jwt, blacklist: blacklist}

	return func(c *gin.Context) {
		header := c.GetHeader(authorizationHeader)
		if header == "" || !strings.HasPrefix(header, bearerPrefix) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("UNAUTHORIZED", "missing or malformed authorization header"))
			return
		}

		token := strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("UNAUTHORIZED", "missing or malformed authorization header"))
			return
		}

		claims, err := cfg.jwt.ValidateAccessToken(token)
		if err != nil {
			if errors.Is(err, ErrTokenTypeMismatch) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("TOKEN_TYPE_MISMATCH", "refresh token cannot be used as access token"))
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("UNAUTHORIZED", "invalid or expired token"))
			return
		}

		blacklisted, _ := cfg.blacklist.IsBlacklisted(c.Request.Context(), claims.ID)
		if blacklisted {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("TOKEN_BLACKLISTED", "token has been revoked"))
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("role", claims.Role)
		c.Set("tier", claims.Tier)
		c.Set("jti", claims.ID)

		c.Next()
	}
}

func RateLimitMiddleware(rdb *redis.Client, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := "rate:" + c.ClientIP() + ":" + c.FullPath()

		cnt, err := rdb.IncrWithTTL(c.Request.Context(), key, window)
		if err != nil {
			log.Printf("[WARN] rate limit: redis error for key %q: %v", key, err)
			c.Next()
			return
		}

		remaining := int64(limit) - cnt
		if remaining < 0 {
			remaining = 0
		}

		c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Header("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
		c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(window).Unix(), 10))

		if cnt > int64(limit) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, errorResponse("RATE_LIMIT_EXCEEDED", "rate limit exceeded"))
			return
		}

		c.Next()
	}
}
