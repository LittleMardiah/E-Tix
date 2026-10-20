package admin

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// FASE 8 P8d — response envelope for admin endpoints.
// API_CONTRACT §3.1: {success, data, error, meta{timestamp, request_id}}.

func adminMeta() gin.H {
	return gin.H{
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		"request_id": uuid.NewString(),
	}
}

func adminSuccess(c *gin.Context, status int, data interface{}) {
	c.JSON(status, gin.H{
		"success": true,
		"data":    data,
		"error":   nil,
		"meta":    adminMeta(),
	})
}

func adminError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{
		"success": false,
		"data":    nil,
		"error": gin.H{
			"code":    code,
			"message": msg,
		},
		"meta": adminMeta(),
	})
}
