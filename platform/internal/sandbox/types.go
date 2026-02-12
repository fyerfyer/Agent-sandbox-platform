package sandbox

import (
	"path/filepath"
	"time"
)

type ContainerConfig struct {
	UseAnonymousVol bool
	ProjectID       string
	SessionID       string
	Image           string
	Cmd             []string // 要在容器中运行的命令
	EnvVars         []string
	MemoryLimit     int64   // 内存限制（字节）
	CPULimit        float64 // CPU 核心数（如 0.5, 1, 2）
	NetworkName     string
	LogDir          string // 宿主机日志存储路径
}

type FileInfo struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}

type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

type LogResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

type ExecLogEntry struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Command    []string  `json:"command"`
	Output     string    `json:"output"`
	ExitCode   int       `json:"exit_code"`
	DurationMs int64     `json:"duration_ms"`
}

func ContainerName(sessionID string) string {
	return "agent-" + sessionID
}

func NetworkName(projectID string) string {
	return "agent-net-" + projectID
}

func DefaultHostPath(root string, projectID string) string {
	return filepath.Join(root, projectID)
}

func DefaultMountPath(projectID string) string {
	return "/app/workspace"
}
