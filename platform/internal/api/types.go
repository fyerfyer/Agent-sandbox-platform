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
	AgentType string   `json:"agent_type"`
}

type ChatRequest struct {
	Message string `json:"message" binding:"required"`
}

// Agent 配置请求体
type ConfigureAgentRequest struct {
	SystemPrompt string            `json:"system_prompt"`
	BuiltinTools []string          `json:"builtin_tools"` // e.g. ["bash","file_read","file_write","list_files"]
	Tools        []ToolDefRequest  `json:"tools"`
	AgentConfig  map[string]string `json:"agent_config"` // e.g. {"max_loops":"10"}
}

type ToolDefRequest struct {
	Name           string `json:"name" binding:"required"`
	Description    string `json:"description"`
	ParametersJSON string `json:"parameters_json"`
}

type SyncFilesRequest struct {
	SrcPath  string `json:"src_path"`
	DestPath string `json:"dest_path"`
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

type SessionListResponse struct {
	Sessions []SessionResponse `json:"sessions"`
}

type ConfigureAgentResponse struct {
	Success        bool     `json:"success"`
	Message        string   `json:"message"`
	AvailableTools []string `json:"available_tools"`
}

type SyncFilesResponse struct {
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	Message   string `json:"message,omitempty"`
}

type FilesListResponse struct {
	SessionID string `json:"session_id"`
	Output    string `json:"output"`
}

type FileContentResponse struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

type HealthResponse struct {
	Status         string `json:"status"`
	ContainerState string `json:"container_state,omitempty"`
	Timestamp      string `json:"timestamp"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Details string `json:"details,omitempty"`
}

type CreateServiceAPIRequest struct {
	Name    string   `json:"name" binding:"required"`
	Image   string   `json:"image" binding:"required"`
	EnvVars []string `json:"env_vars"`
	Cmd     []string `json:"cmd"`
}

type ServiceResponse struct {
	ServiceID string `json:"service_id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	IP        string `json:"ip"`
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
}

type ServiceListResponse struct {
	SessionID string            `json:"session_id"`
	Services  []ServiceResponse `json:"services"`
}

type CreateComposeAPIRequest struct {
	ComposeContent string `json:"compose_content"` // docker-compose.yml 文件内容
	ComposeFile    string `json:"compose_file"`    // 或宿主机上的文件路径（二选一）
}

type ComposeStackResponse struct {
	SessionID   string                   `json:"session_id"`
	ProjectName string                   `json:"project_name"`
	Status      string                   `json:"status"`
	Services    []ComposeServiceResponse `json:"services"`
}

type ComposeServiceResponse struct {
	Name        string `json:"name"`
	ContainerID string `json:"container_id"`
	IP          string `json:"ip"`
	Status      string `json:"status"`
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
