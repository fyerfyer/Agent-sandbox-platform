package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"io"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

var _ Sandbox = (*Container)(nil)

type Container struct {
	ID        string
	Config    ContainerConfig
	client    *client.Client
	status    container.ContainerState
	logger    *slog.Logger
	HostPath  string
	MountPath string
}

func NewContainer(client *client.Client, cfg ContainerConfig, hostRoot string,
	logger *slog.Logger) *Container {
	l := logger.With(
		slog.String("session_id", cfg.SessionID),
		slog.String("project_id", cfg.ProjectID),
	)

	return &Container{
		Config:    cfg,
		client:    client,
		logger:    l,
		HostPath:  DefaultHostPath(hostRoot, cfg.ProjectID),
		MountPath: DefaultMountPath(cfg.ProjectID),
	}
}

func (c *Container) secureResolvePath(userPath string) (string, error) {
	// join会清理路径、去掉相对路径
	target := filepath.Join(c.HostPath, userPath)
	if !strings.HasPrefix(target, filepath.Clean(c.HostPath)) {
		return "", fmt.Errorf("%w: path escapes workspace: %s", ErrInvalidPath, userPath)
	}
	return target, nil
}

func (c *Container) Start(ctx context.Context) error {
	c.logger.Info("Starting container", slog.String("image", c.Config.Image))

	// 确认 Image 存在
	_, err := c.client.ImageInspect(ctx, c.Config.Image)
	if errdefs.IsNotFound(err) {
		c.logger.Info("Image not found, pulling...", "image", c.Config.Image)
		reader, err := c.client.ImagePull(ctx, c.Config.Image, image.PullOptions{})
		if err != nil {
			c.logger.Error("Failed to pull image", "error", err)
			return fmt.Errorf("%w: %v", ErrImagePullFailed, err)
		}
		defer reader.Close()

		// 异步读取 pull 输出
		done := make(chan struct{})
		go func() {
			_, err := io.Copy(io.Discard, reader)
			if err != nil {
				c.logger.Error("Failed to read pull output", "error", err)
			}
			close(done)
		}()

		select {
		case <-done:
			c.logger.Info("Image pull completed")
		case <-ctx.Done():
			c.logger.Info("Image pull cancelled")
			return fmt.Errorf("%w: %v", ErrImagePullFailed, ctx.Err())
		}
	} else if err != nil {
		return fmt.Errorf("failed to inspect image: %w", err)
	}

	// 确保工作目录存在
	if err := os.MkdirAll(c.HostPath, 0755); err != nil {
		return fmt.Errorf("failed to create host path: %w", err)
	}

	name := ContainerName(c.Config.SessionID)
	config := &container.Config{
		Image:      c.Config.Image,
		Cmd:        []string{"tail", "-f", "/dev/null"},
		Env:        c.Config.EnvVars,
		WorkingDir: c.MountPath,
		Labels: map[string]string{
			"managed_by": "agent-platform",
			"project_id": c.Config.ProjectID,
			"session_id": c.Config.SessionID,
		},
	}

	hostConfig := &container.HostConfig{
		// 绑定主机路径到容器路径
		Binds: []string{
			fmt.Sprintf("%s:%s:rw", c.HostPath, c.MountPath),
		},
		Resources: container.Resources{
			Memory:   c.Config.MemoryLimit,
			NanoCPUs: int64(c.Config.CPULimit * 1e9),
		},
		AutoRemove: false,
	}

	netConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			c.Config.NetworkName: {},
		},
	}

	resp, err := c.client.ContainerCreate(ctx, config, hostConfig, netConfig, nil, name)
	if err != nil {
		c.logger.Error("Failed to create container", "error", err)
		return fmt.Errorf("%w: %v", ErrContainerStartFailed, err)
	}

	c.ID = resp.ID
	if err := c.client.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
		c.logger.Error("Failed to start container", "error", err)
		// 如果启动失败，清理容器
		_ = c.client.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("%w: %v", ErrContainerStartFailed, err)
	}

	// 初始状态更新
	if err := c.refreshStatus(ctx); err != nil {
		c.logger.Warn("Failed to refresh status after start", "error", err)
	}

	c.logger.Info("Container started successfully", "container_id", c.ID)
	return nil
}

func (c *Container) Stop(ctx context.Context, timeoutSeconds int) error {
	c.logger.Info("Stopping container", "container_id", c.ID)
	opts := container.StopOptions{
		Timeout: &timeoutSeconds,
	}

	if err := c.client.ContainerStop(ctx, c.ID, opts); err != nil {
		if errdefs.IsNotFound(err) {
			return ErrContainerNotFound
		}
		return fmt.Errorf("failed to stop container: %w", err)
	}

	c.logger.Info("Container stopped successfully", "container_id", c.ID)
	return nil
}

func (c *Container) Remove(ctx context.Context) error {
	c.logger.Info("Removing container", "container_id", c.ID)
	opts := container.RemoveOptions{
		Force: true,
	}

	if err := c.client.ContainerRemove(ctx, c.ID, opts); err != nil {
		if errdefs.IsNotFound(err) {
			return ErrContainerNotFound
		}
		return fmt.Errorf("failed to remove container: %w", err)
	}

	c.logger.Info("Container removed successfully", "container_id", c.ID)
	return nil
}

func (c *Container) refreshStatus(ctx context.Context) error {
	inspect, err := c.client.ContainerInspect(ctx, c.ID)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return ErrContainerNotFound
		}
		return fmt.Errorf("failed to inspect container: %w", err)
	}
	c.status = inspect.State.Status
	return nil
}

func (c *Container) GetStatus(ctx context.Context) (container.ContainerState, error) {
	if err := c.refreshStatus(ctx); err != nil {
		return "", err
	}
	return c.status, nil
}

func (c *Container) Exec(ctx context.Context, cmd []string, env []string, workDir string) (*ExecResult, error) {
	if workDir == "" {
		workDir = c.MountPath
	}

	createOpts := container.ExecOptions{
		Cmd:          cmd,
		Env:          env,
		WorkingDir:   workDir,
		Tty:          true,
		AttachStdout: true,
		AttachStderr: true,
	}

	createdResp, err := c.client.ContainerExecCreate(ctx, c.ID, createOpts)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to create exec: %v", ErrExecFailed, err)
	}

	c.logger.Info("Exec created successfully")

	attachOpts := container.ExecAttachOptions{
		Tty:    true,
		Detach: false,
	}

	attachResp, err := c.client.ContainerExecAttach(ctx, createdResp.ID, attachOpts)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to attach to exec: %v", ErrExecFailed, err)
	}
	defer attachResp.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	start := time.Now()

	// 异步读取输出
	done := make(chan struct{})
	go func() {
		_, _ = stdcopy.StdCopy(&stdoutBuf, &stderrBuf, attachResp.Reader)
		close(done)
	}()

	select {
	case <-done:
		// 正常完成
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	duration := time.Since(start)

	inspectResp, err := c.client.ContainerExecInspect(ctx, createdResp.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to inspect exec: %v", ErrExecFailed, err)
	}

	return &ExecResult{
		ExitCode: inspectResp.ExitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: duration,
	}, nil
}

func (c *Container) WriteFile(ctx context.Context, path string, content []byte) error {
	hostTarget, err := c.secureResolvePath(path)
	if err != nil {
		return err
	}

	if err := os.WriteFile(hostTarget, content, 0644); err != nil {
		return err
	}

	return nil
}

func (c *Container) ReadFile(ctx context.Context, path string) ([]byte, error) {
	hostTarget, err := c.secureResolvePath(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(hostTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return data, nil
}

func (c *Container) ListFiles(ctx context.Context, path string) ([]FileInfo, error) {
	hostPath, err := c.secureResolvePath(path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(hostPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var files []FileInfo
	for _, entry := range entries {
		fileInfo, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to get file info: %w", err)
		}

		files = append(files, FileInfo{
			Path:    entry.Name(),
			Size:    fileInfo.Size(),
			IsDir:   entry.IsDir(),
			ModTime: fileInfo.ModTime(),
		})
	}

	return files, nil
}

func (c *Container) GetLogs(ctx context.Context, tail int) (*LogResult, error) {
	tailStr := "all"
	if tail > 0 {
		tailStr = fmt.Sprintf("%d", tail)
	}

	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tailStr,
	}

	render, err := c.client.ContainerLogs(ctx, c.ID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs: %w", err)
	}
	defer render.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = stdcopy.StdCopy(&stdoutBuf, &stderrBuf, render)
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return &LogResult{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}, nil
}
