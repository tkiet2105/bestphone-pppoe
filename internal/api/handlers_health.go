package api

import (
	"time"

	"github.com/gin-gonic/gin"
)

var startedAt = time.Now()

// AppVersion is set by main from cmd/bestphone-pppoe/main.go (appVersion const).
var AppVersion = "dev"

func HealthHandler(c *gin.Context) {
	c.JSON(200, gin.H{
		"service": "bestphone-pppoe",
		"status":  "ok",
		"version": AppVersion,
		"uptime":  int(time.Since(startedAt).Seconds()),
	})
}
