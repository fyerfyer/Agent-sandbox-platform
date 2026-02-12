package api

import (
	"net/http"
	"platform/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

func NewRouter(svc *service.Service) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(LoggerMiddleware())
	r.Use(CORSMiddleware())
	r.Use(RequestIDMiddleware())

	// Global health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, HealthResponse{
			Status:    "ok",
			Timestamp: formatTime(time.Now()),
		})
	})

	sessionHandler := NewSessionHandler(svc)
	chatHandler := NewChatHandler(svc)

	v1 := r.Group("/api/v1")
	{
		sessions := v1.Group("/sessions")
		{
			sessions.POST("", sessionHandler.CreateSession)
			sessions.GET("/:id", sessionHandler.GetSession)
			sessions.DELETE("/:id", sessionHandler.TerminateSession)
			sessions.GET("/:id/health", sessionHandler.HealthCheckSession)
			sessions.GET("/:id/wait", sessionHandler.WaitReady)

			// Agent configuration & control
			sessions.POST("/:id/configure", sessionHandler.ConfigureAgent)
			sessions.POST("/:id/stop", sessionHandler.StopAgent)

			// Chat
			sessions.POST("/:id/chat", chatHandler.SendMessage)
			sessions.GET("/:id/stream", chatHandler.StreamEvents)

			// File operations
			sessions.POST("/:id/sync", sessionHandler.SyncFiles)
			sessions.GET("/:id/files", sessionHandler.ListFiles)
			sessions.GET("/:id/files/read", sessionHandler.ReadFile)

			// Companion service management
			sessions.POST("/:id/services", sessionHandler.CreateService)
			sessions.GET("/:id/services", sessionHandler.ListServices)
			sessions.DELETE("/:id/services/:service_id", sessionHandler.RemoveService)
		}
	}

	return r
}
