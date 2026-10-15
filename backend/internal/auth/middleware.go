package auth

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/LittleMardiah/E-Tix/backend/internal/redis"
	"io"
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

		// B1 FIX: fail-closed. If Redis is unavailable we cannot prove the
		// token is not revoked, so reject (503) rather than accept.
		blacklisted, bErr := cfg.blacklist.IsBlacklisted(c.Request.Context(), claims.ID)
		if bErr != nil {
			log.Printf("[ERROR] auth: blacklist check failed: %v", bErr)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, errorResponse("SERVICE_UNAVAILABLE", "auth dependency unavailable"))
			return
		}
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

type RateLimitScope string

const (
	RateLimitScopeIP           RateLimitScope = "ip"
	RateLimitScopeUser         RateLimitScope = "user"
	RateLimitScopeUserViaToken RateLimitScope = "user_via_token"
)

func RateLimitMiddleware(rdb *redis.Client, limit int, window time.Duration) gin.HandlerFunc {
	return RateLimitMiddlewareScoped(rdb, limit, window, RateLimitScopeIP)
}

func RateLimitMiddlewareScoped(rdb *redis.Client, limit int, window time.Duration, scope RateLimitScope) gin.HandlerFunc {
	return func(c *gin.Context) {
		var identifier string
		switch scope {
		case RateLimitScopeUser:
			identifier = c.GetString("user_id")
		case RateLimitScopeUserViaToken:
			identifier = extractUserIDFromRefreshToken(c)
		default:
			identifier = c.ClientIP()
		}
		if identifier == "" {
			identifier = c.ClientIP()
		}
		key := "rate:" + string(scope) + ":" + identifier + ":" + c.FullPath()

		cnt, err := rdb.IncrWithTTL(c.Request.Context(), key, window)
		if err != nil {
			// B6 (accepted trade-off): rate limit fails OPEN to keep the API
			// available; but log at ERROR so an outage is visible. Combined
			// with B1 (fail-closed blacklist), a Redis outage will reject
			// auth'd requests with 503 before this is reached.
			log.Printf("[ERROR] rate limit: redis error, failing open for key %q: %v", key, err)
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

func extractUserIDFromRefreshToken(c *gin.Context) string {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	var payload struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.RefreshToken == "" {
		return ""
	}

	parts := strings.Split(payload.RefreshToken, ".")
	if len(parts) != 3 {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return ""
	}
	return sub
}
