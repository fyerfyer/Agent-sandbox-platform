package session

import (
	"platform/internal/orchestrator"
	"time"
)

type SessionStatus string

const (
	StatusInitializing SessionStatus = "initializing"
	StatusReady        SessionStatus = "ready"
	StatusRunning      SessionStatus = "running"
	StatusTerminated   SessionStatus = "terminated"
	StatusError        SessionStatus = "error"
)

type Session struct {
	ID          string                    `json:"id"`
	ProjectID   string                    `json:"project_id"`
	UserID      string                    `json:"user_id"`
	ContainerID string                    `json:"container_id"` // 挂载容器 ID
	NodeIP      string                    `json:"node_ip"`      // gRPC 通信
	Status      SessionStatus             `json:"status"`
	Strategy    orchestrator.StrategyType `json:"strategy"`
	CreatedAt   time.Time                 `json:"created_at"`
	ActiveAt    time.Time                 `json:"active_at"`
}

type SessionParams struct {
	ProjectID     string
	UserID        string
	Strategy      orchestrator.StrategyType
	EnvVars       []string
	ContainerOpts orchestrator.ContainerOptions
}

const SessionCreateTask = "session:create"

type SessionCreatePayload struct {
	SessionID string                    `json:"session_id"`
	ProjectID string                    `json:"project_id"`
	UserID    string                    `json:"user_id"`
	Image     string                    `json:"image"`
	Strategy  orchestrator.StrategyType `json:"strategy"`
	EnvVars   []string                  `json:"env_vars"`
}
