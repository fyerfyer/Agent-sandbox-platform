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
	EnvVars         []string
	MemoryLimit     int64
	CPULimit        int64
	NetworkName     string
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
	return "app/workspace"
}
