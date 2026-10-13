package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func RBACMiddleware(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetString("role")
		if role == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, errorResponse("UNAUTHORIZED", "missing authentication context"))
			return
		}

		for _, allowed := range allowedRoles {
			if role == allowed {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, errorResponse("FORBIDDEN", "insufficient role"))
	}
}
