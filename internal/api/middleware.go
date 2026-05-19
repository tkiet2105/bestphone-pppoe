package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tkiet2105/bestphone-pppoe/internal/db"
	"github.com/tkiet2105/bestphone-pppoe/internal/models"
)

// BearerAuth — middleware enforce header `Authorization: Bearer <token>` exist trong DB.
// Cho phép query param ?token= cho EventSource (SSE).
func BearerAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ""
		auth := c.GetHeader("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		if token == "" {
			token = c.Query("token")
		}
		token = strings.TrimSpace(token)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "missing token"})
			return
		}
		var t models.Token
		if err := db.DB.Where("token = ?", token).First(&t).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid token"})
			return
		}
		c.Set("token_id", t.Id)
		c.Next()
	}
}

// CORS — cho phép UI gọi từ same-origin qua nginx; nếu cross-origin (dev) thì lỏng.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = "*"
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func fail(c *gin.Context, code int, msg string) {
	c.JSON(code, gin.H{"success": false, "error": msg})
}
