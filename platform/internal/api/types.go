package api

import (
	"platform/internal/orchestrator"
	"time"
)

type CreateSessionRequest struct {
	ProjectID string   `json:"project_id" binding:"required"`
	UserID    string   `json:"user_id" binding:"required"`
	Strategy  string   `json:"strategy" binding:"required,oneof=Warm-Strategy Cold-Strategy"`
	Image     string   `json:"image"`
	EnvVars   []string `json:"env_vars"`
}

type ChatRequest struct {
	Message string `json:"message" binding:"required"`
}

type SessionResponse struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	UserID      string `json:"user_id"`
	ContainerID string `json:"container_id,omitempty"`
	NodeIP      string `json:"node_ip,omitempty"`
	Status      string `json:"status"`
	Strategy    string `json:"strategy"`
	CreatedAt   string `json:"created_at"`
}

type ChatResponse struct {
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
}

type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Details string `json:"details,omitempty"`
}

// SSEEvent 是服务器发送事件的结构体
type SSEEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Payload   any    `json:"payload"`
	Timestamp string `json:"timestamp"`
}

func mapStrategyType(s string) orchestrator.StrategyType {
	switch s {
	case string(orchestrator.WarmStrategyType):
		return orchestrator.WarmStrategyType
	case string(orchestrator.ColdStrategyType):
		return orchestrator.ColdStrategyType
	default:
		return orchestrator.ColdStrategyType
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
