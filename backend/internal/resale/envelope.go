package resale

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// S4-2: response envelope per API_CONTRACT §3.1
// Format: {success: bool, data: any, error: null|{code,message}, meta: {timestamp, request_id}}

func resaleMeta() gin.H {
	return gin.H{
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		"request_id": uuid.NewString(),
	}
}

func resaleSuccess(c *gin.Context, status int, data interface{}) {
	c.JSON(status, gin.H{
		"success": true,
		"data":    data,
		"error":   nil,
		"meta":    resaleMeta(),
	})
}

func resaleError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{
		"success": false,
		"data":    nil,
		"error": gin.H{
			"code":    code,
			"message": msg,
		},
		"meta": resaleMeta(),
	})
}
