package sandbox

import (
	"context"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
)

type Sandbox interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context, timeoutSeconds int) error
	Remove(ctx context.Context) error
	Exec(ctx context.Context, cmd []string, env []string, workDir string) (*ExecResult, error)
	GetStatus(ctx context.Context) (container.ContainerState, error)
	GetLogs(ctx context.Context, tail int) (*LogResult, error)
	GetExecLogs(ctx context.Context) ([]ExecLogEntry, error)
	ListFiles(ctx context.Context, path string) ([]FileInfo, error)
	WriteFile(ctx context.Context, path string, reader io.Reader, perm os.FileMode) error
	OpenFile(ctx context.Context, path string) (io.ReadCloser, error)

	// 复制文件
	// Warm Pool 使用匿名卷管理文件，因此需要这样的启动方式
	CopyFromContainer(ctx context.Context, srcPath string, dest io.Writer) error

	// 上传多文件 Tar 的方法
	UploadArchive(ctx context.Context, destPath string, tarStream io.Reader) error

	CopyToContainer(ctx context.Context, destPath string, src io.Reader) error
	IsRunning(ctx context.Context) bool
}
