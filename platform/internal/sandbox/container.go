package sandbox

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path"
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
	"github.com/google/uuid"
)

var _ Sandbox = (*Container)(nil)

type Container struct {
	ID        string
	IP        string
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

	c := &Container{
		Config:    cfg,
		client:    client,
		logger:    l,
		MountPath: DefaultMountPath(cfg.ProjectID),
	}

	if c.Config.LogDir == "" {
		c.Config.LogDir = ".dockerlogs"
	}

	// Ensure log directory exists
	logPath := filepath.Join(c.Config.LogDir, c.Config.SessionID)
	if err := os.MkdirAll(logPath, 0755); err != nil {
		l.Error("Failed to create log directory", "error", err)
	}

	if !cfg.UseAnonymousVol {
		c.HostPath = DefaultHostPath(hostRoot, cfg.ProjectID)
	}

	return c
}

func (c *Container) resolveHostPath(userPath string) (string, error) {
	// join会清理路径、去掉相对路径
	target := filepath.Join(c.HostPath, userPath)
	if !strings.HasPrefix(target, filepath.Clean(c.HostPath)) {
		return "", fmt.Errorf("%w: path escapes workspace: %s", ErrInvalidPath, userPath)
	}
	return target, nil
}

func (c *Container) resolveContainerPath(userPath string) (string, error) {
	basePath := c.MountPath

	// 这里用 path，因为 filepath 在 windows 上会把 / 换成 \，导致容器路径错误
	target := path.Join(basePath, userPath)
	cleanedTarget := path.Clean(target)

	// 确保路径没有逃逸
	if !strings.HasPrefix(cleanedTarget, basePath) {
		return "", fmt.Errorf("Path escapes workspace: %s", userPath)
	}
	return cleanedTarget, nil
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
	if c.HostPath != "" {
		if err := os.MkdirAll(c.HostPath, 0755); err != nil {
			return fmt.Errorf("failed to create host path: %w", err)
		}
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

	var hostConfig *container.HostConfig
	if c.Config.UseAnonymousVol {
		hostConfig = &container.HostConfig{
			Resources: container.Resources{
				Memory:   c.Config.MemoryLimit,
				NanoCPUs: int64(c.Config.CPULimit * 1e9),
			},
			AutoRemove: false,
			Tmpfs: map[string]string{
				"/app/workspace": "rw,size=512m",
			},
		}
	} else {
		hostConfig = &container.HostConfig{
			Binds: []string{
				fmt.Sprintf("%s:%s:rw", c.HostPath, c.MountPath),
			},
			Resources: container.Resources{
				Memory:   c.Config.MemoryLimit,
				NanoCPUs: int64(c.Config.CPULimit * 1e9),
			},
			AutoRemove: false,
		}
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

	// 获取容器IP
	inspect, err := c.client.ContainerInspect(ctx, c.ID)
	if err != nil {
		c.logger.Error("Failed to inspect container", "error", err)
		_ = c.client.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("failed to inspect container: %w", err)
	}

	if net, ok := inspect.NetworkSettings.Networks[c.Config.NetworkName]; ok {
		c.IP = net.IPAddress
	} else {
		for _, v := range inspect.NetworkSettings.Networks {
			c.IP = v.IPAddress
			break
		}
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
		Tty:          false,
		AttachStdout: true,
		AttachStderr: true,
	}

	createdResp, err := c.client.ContainerExecCreate(ctx, c.ID, createOpts)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to create exec: %v", ErrExecFailed, err)
	}

	c.logger.Info("Exec created successfully")

	attachOpts := container.ExecAttachOptions{
		Tty:    false,
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
		// TTY=false, Docker 使用多路复用格式，stdcopy 可以解析
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

	// 持久化 Exec Log 存储
	entry := ExecLogEntry{
		ID:         uuid.New().String(),
		Timestamp:  start,
		Command:    cmd,
		Output:     stdoutBuf.String() + stderrBuf.String(),
		ExitCode:   inspectResp.ExitCode,
		DurationMs: duration.Milliseconds(),
	}

	logFile := filepath.Join(c.Config.LogDir, c.Config.SessionID, "events.jsonl")
	if f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		if data, err := json.Marshal(entry); err == nil {
			_, _ = f.Write(append(data, '\n'))
		} else {
			c.logger.Error("Failed to marshal log entry", "error", err)
		}
		f.Close()
	} else {
		c.logger.Error("Failed to open log file", "error", err)
	}

	return &ExecResult{
		ExitCode: inspectResp.ExitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: duration,
	}, nil
}

func (c *Container) WriteFile(ctx context.Context, path string, reader io.Reader, perm os.FileMode) error {
	hostTarget, err := c.resolveHostPath(path)
	if err != nil {
		return fmt.Errorf("failed to resolve host path: %v", err)
	}

	f, err := os.OpenFile(hostTarget, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer f.Close()

	_, err = io.Copy(f, reader)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	return nil
}

func (c *Container) OpenFile(ctx context.Context, path string) (io.ReadCloser, error) {
	hostTarget, err := c.resolveHostPath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve host path: %v", err)
	}

	f, err := os.Open(hostTarget)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %v", err)
	}

	return f, nil
}

func (c *Container) ListFiles(ctx context.Context, path string) ([]FileInfo, error) {
	hostPath, err := c.resolveHostPath(path)
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

func (c *Container) CopyToContainer(ctx context.Context, destPath string, src io.Reader) error {
	containerDest, err := c.resolveContainerPath(destPath)
	if err != nil {
		return fmt.Errorf("failed to resolve container path: %v", err)
	}

	parent := path.Dir(containerDest)
	if _, err := c.Exec(ctx, []string{"mkdir", "-p", parent}, nil, "/"); err != nil {
		return err
	}

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		defer func() {
			tw.Close()
			pw.Close()
		}()

		header := &tar.Header{
			Name: path.Base(containerDest),
			Mode: 0644,
			Size: 0,
		}
		if err := tw.WriteHeader(header); err != nil {
			pw.CloseWithError(err)
			return
		}

		if _, err := io.Copy(tw, src); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	opts := container.CopyToContainerOptions{
		AllowOverwriteDirWithFile: true,
	}

	return c.client.CopyToContainer(ctx, c.ID, "/", pr, opts)
}

// UploadArchive 用于多文件 Tar 上传
// 使用这个方法上传文件会自动保留文件目录架构
func (c *Container) UploadArchive(ctx context.Context, destPath string, tarStream io.Reader) error {
	containerDest, err := c.resolveContainerPath(destPath)
	if err != nil {
		return fmt.Errorf("failed to resolve container path: %v", err)
	}

	if _, err := c.Exec(ctx, []string{"mkdir", "-p", containerDest}, nil, "/"); err != nil {
		return err
	}

	opts := container.CopyToContainerOptions{
		AllowOverwriteDirWithFile: true,
	}

	return c.client.CopyToContainer(ctx, c.ID, containerDest, tarStream, opts)
}

func (c *Container) CopyFromContainer(ctx context.Context, srcPath string, dest io.Writer) error {
	containerSrc, err := c.resolveContainerPath(srcPath)
	if err != nil {
		return fmt.Errorf("failed to resolve container path: %v", err)
	}

	r, _, err := c.client.CopyFromContainer(ctx, c.ID, containerSrc)
	if err != nil {
		return fmt.Errorf("failed to copy from container: %v", err)
	}
	defer r.Close()

	tarReader := tar.NewReader(r)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %v", err)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		if _, err := io.Copy(dest, tarReader); err != nil {
			return fmt.Errorf("failed to write file: %v", err)
		}
	}

	return nil
}

func (c *Container) IsRunning(ctx context.Context) bool {
	inspect, err := c.client.ContainerInspect(ctx, c.ID)
	if err != nil {
		return false
	}
	return inspect.State.Running
}

func (c *Container) GetExecLogs(ctx context.Context) ([]ExecLogEntry, error) {
	logFile := filepath.Join(c.Config.LogDir, c.Config.SessionID, "events.jsonl")
	f, err := os.Open(logFile)
	if err != nil {
		if os.IsNotExist(err) {
			return []ExecLogEntry{}, nil
		}
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	var entries []ExecLogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry ExecLogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			c.logger.Warn("Failed to unmarshal log entry", "error", err)
			continue
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan log file: %w", err)
	}

	return entries, nil
}
