package api

import "github.com/gin-gonic/gin"

// RegisterRoutes — toàn bộ route dưới /api/v1.
func RegisterRoutes(r *gin.Engine) {
	r.Use(CORS())

	// public
	r.GET("/api/v1/health", HealthHandler)
	r.POST("/api/v1/auth/login", AuthLogin)

	// protected
	g := r.Group("/api/v1", BearerAuth())
	{
		g.GET("/events", EventsSSE)
		g.GET("/ifaces", ListIfaces)
		g.POST("/ifaces/probe", ProbeIfaces)

		g.POST("/lines", CreateLine)
		g.GET("/lines", ListLines)
		g.GET("/lines/:id", GetLine)
		g.PUT("/lines/:id", UpdateLine)
		g.POST("/lines/:id/delete", DeleteLine)
		g.POST("/lines/:id/sessions", CreateLineSession)
		g.POST("/lines/:id/sessions/bulk", CreateLineSessionsBulk)

		g.GET("/sessions", ListSessions)
		g.GET("/sessions/:id", GetSession)
		g.POST("/sessions/:id/delete", DeleteSession)
		g.POST("/sessions/:id/rotate", RotateSession)
		g.POST("/sessions/:id/enabled", SetSessionEnabled)

		g.GET("/proxies/export", ExportProxies)
		g.GET("/proxies/:id/credentials", ListCreds)
		g.POST("/proxies/:id/credentials", CreateCred)
		g.POST("/proxies/:id/credentials/bulk", BulkCreateCreds)
		g.PUT("/proxies/:id/credentials/:cid", UpdateCred)
		g.DELETE("/proxies/:id/credentials/:cid", DeleteCred)

		g.POST("/rotate", RotateBatch)

		g.GET("/logs", GetLogs)

		g.GET("/rules", ListRules)
		g.POST("/rules", CreateRule)
		g.PUT("/rules/:id", UpdateRule)
		g.DELETE("/rules/:id", DeleteRule)

		g.GET("/tokens", ListTokens)
		g.POST("/tokens", CreateToken)
		g.DELETE("/tokens/:id", DeleteToken)
	}
}
