package sandbox

import (
	"context"

	"github.com/docker/docker/api/types/container"
)

type Sandbox interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context, timeoutSeconds int) error
	Remove(ctx context.Context) error
	Exec(ctx context.Context, cmd []string, env []string, workDir string) (*ExecResult, error)
	GetStatus(ctx context.Context) (container.ContainerState, error)
	GetLogs(ctx context.Context, tail int) (*LogResult, error)
	ListFiles(ctx context.Context, path string) ([]FileInfo, error)
	WriteFile(ctx context.Context, path string, content []byte) error
	ReadFile(ctx context.Context, path string) ([]byte, error)
}
