package queue

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type QueueTokenIssuer interface {
	Generate(userID uuid.UUID, tier string, score int) (string, error)
}

type MessagePublisher interface {
	Publish(ctx context.Context, queueName string, body []byte) error
}

type IngressConfig struct {
	FanScore         *FanScoreService
	QueueTokenIssuer QueueTokenIssuer
	IPBlacklistCheck func(ctx context.Context, ip string) (bool, error)
	Publisher        MessagePublisher
}

func IngressMiddleware(cfg IngressConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "UNAUTHORIZED",
			})
			return
		}

		uid, err := uuid.Parse(userID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "UNAUTHORIZED",
			})
			return
		}

		ctx := c.Request.Context()

		if cfg.IPBlacklistCheck != nil {
			ip := c.ClientIP()
			blocked, berr := cfg.IPBlacklistCheck(ctx, ip)
			if berr != nil {
				// H8 FIX: fail-closed. If we cannot verify the IP is not
				// blacklisted, reject rather than proceed.
				zap.L().Error("ingress ip blacklist check failed",
					zap.String("ip", ip), zap.Error(berr))
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"success": false,
					"error":   "SERVICE_UNAVAILABLE",
				})
				return
			}
			if blocked {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"success": false,
					"error":   "BLACKLISTED",
				})
				return
			}
		}

		score, tier, err := cfg.FanScore.ComputeFinalScore(ctx, uid)
		if err != nil {
			zap.L().Error("ingress compute final score failed", zap.String("user_id", uid.String()), zap.Error(err))
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error":   "INTERNAL_ERROR",
			})
			return
		}

		c.Set("fan_score", score)
		c.Set("tier", tier)

		queueToken := ""
		if cfg.QueueTokenIssuer != nil {
			token, terr := cfg.QueueTokenIssuer.Generate(uid, tier, score)
			if terr != nil {
				zap.L().Warn("ingress queue token generation failed", zap.String("user_id", uid.String()), zap.Error(terr))
			} else {
				queueToken = token
			}
		}
		c.Set("queue_token", queueToken)

		if cfg.Publisher != nil {
			meta := FanScoreMetadata{
				UserID:     uid.String(),
				IP:         c.ClientIP(),
				FinalScore: score,
				Timestamp:  time.Now().UTC(),
			}
			payload, merr := json.Marshal(meta)
			if merr != nil {
				zap.L().Warn("ingress marshal fan score metadata failed", zap.String("user_id", uid.String()), zap.Error(merr))
			} else {
				go func() {
					if perr := cfg.Publisher.Publish(context.Background(), "fan_score", payload); perr != nil {
						zap.L().Warn("ingress publish fan score metadata failed", zap.String("user_id", uid.String()), zap.Error(perr))
					}
				}()
			}
		}

		c.Next()
	}
}
