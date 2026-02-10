package api

import (
	"net/http"
	"platform/internal/orchestrator"
	"platform/internal/service"
	"platform/internal/session"
	"time"

	"github.com/gin-gonic/gin"
)

type SessionHandler struct {
	svc *service.Service
}

func NewSessionHandler(svc *service.Service) *SessionHandler {
	return &SessionHandler{svc: svc}
}

// CreateSession POST /api/v1/sessions
func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorWithDetails(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
		return
	}

	params := session.SessionParams{
		ProjectID: req.ProjectID,
		UserID:    req.UserID,
		Strategy:  mapStrategyType(req.Strategy),
		EnvVars:   req.EnvVars,
		ContainerOpts: orchestrator.ContainerOptions{
			Image:     req.Image,
			ProjectID: req.ProjectID,
			EnvVars:   req.EnvVars,
		},
	}

	sess, err := h.svc.CreateSession(c.Request.Context(), params)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusCreated, SessionResponse{
		ID:        sess.ID,
		ProjectID: sess.ProjectID,
		UserID:    sess.UserID,
		Status:    string(sess.Status),
		Strategy:  string(sess.Strategy),
		CreatedAt: formatTime(sess.CreatedAt),
	})
}

// GetSession GET /api/v1/sessions/:id
func (h *SessionHandler) GetSession(c *gin.Context) {
	id := c.Param("id")

	sess, err := h.svc.GetSession(c.Request.Context(), id)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, SessionResponse{
		ID:          sess.ID,
		ProjectID:   sess.ProjectID,
		UserID:      sess.UserID,
		ContainerID: sess.ContainerID,
		NodeIP:      sess.NodeIP,
		Status:      string(sess.Status),
		Strategy:    string(sess.Strategy),
		CreatedAt:   formatTime(sess.CreatedAt),
	})
}

// TerminateSession DELETE /api/v1/sessions/:id
func (h *SessionHandler) TerminateSession(c *gin.Context) {
	id := c.Param("id")

	if err := h.svc.TerminateSession(c.Request.Context(), id); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "terminated",
		"session_id": id,
	})
}

// HealthCheckSession GET /api/v1/sessions/:id/health
func (h *SessionHandler) HealthCheckSession(c *gin.Context) {
	id := c.Param("id")

	healthy, err := h.svc.HealthCheck(c.Request.Context(), id)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	healthStatus := "unhealthy"
	if healthy {
		healthStatus = "healthy"
	}

	c.JSON(http.StatusOK, HealthResponse{
		Status:    healthStatus,
		Timestamp: formatTime(time.Now()),
	})
}

// WaitReady GET /api/v1/sessions/:id/wait
func (h *SessionHandler) WaitReady(c *gin.Context) {
	id := c.Param("id")

	sess, err := h.svc.WaitForReady(c.Request.Context(), id, 500*time.Millisecond)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, SessionResponse{
		ID:          sess.ID,
		ProjectID:   sess.ProjectID,
		UserID:      sess.UserID,
		ContainerID: sess.ContainerID,
		NodeIP:      sess.NodeIP,
		Status:      string(sess.Status),
		Strategy:    string(sess.Strategy),
		CreatedAt:   formatTime(sess.CreatedAt),
	})
}
