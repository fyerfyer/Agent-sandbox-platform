package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"platform/internal/eventbus"
	"platform/internal/orchestrator"
	"platform/internal/sandbox"
	"platform/internal/session"
	"time"

	"github.com/hibiken/asynq"
)

var _ SessionWorker = (*SessionTaskWorker)(nil)

type WorkerConfig struct {
	ProjectDir string // 项目存储根目录，如 "/.../agent-platform/projects"
}

type SessionTaskWorker struct {
	pool   orchestrator.IPool
	repo   session.SessionRepository
	bus    eventbus.EventBus
	config WorkerConfig
	logger *slog.Logger
}

func NewSessionTaskWorker(pool orchestrator.IPool, repo session.SessionRepository, bus eventbus.EventBus, config WorkerConfig, logger *slog.Logger) *SessionTaskWorker {
	return &SessionTaskWorker{
		pool:   pool,
		repo:   repo,
		bus:    bus,
		config: config,
		logger: logger.With("component", "session-worker"),
	}
}

func (w *SessionTaskWorker) HandleSessionCreate(ctx context.Context, task *asynq.Task) error {
	w.logger.Info("Processing session create task")

	var payload session.SessionCreatePayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		w.logger.Error("Failed to unmarshal payload", "error", err)
		return fmt.Errorf("json unmarshal error: %w", err)
	}

	w.logger.Info("Deserialized payload",
		"session_id", payload.SessionID,
		"project_id", payload.ProjectID,
		"strategy", payload.Strategy,
		"image", payload.Image)

	var strategy orchestrator.ContainerStrategy
	switch payload.Strategy {
	case orchestrator.WarmStrategyType:
		strategy = &orchestrator.WarmStrategy{}
	case orchestrator.ColdStrategyType:
		strategy = &orchestrator.ColdStrategy{}
	default:
		w.logger.Error("Unknown strategy type", "strategy", payload.Strategy)
		return fmt.Errorf("unknown strategy type: %s", payload.Strategy)
	}

	containerOptions := orchestrator.ContainerOptions{
		ProjectID: payload.ProjectID,
		SessionID: payload.SessionID,
		EnvVars:   payload.EnvVars,
		Image:     payload.Image,
	}

	w.logger.Info("Acquiring container", "strategy", strategy.Name())
	container, err := strategy.Get(ctx, w.pool, containerOptions)
	if err != nil {
		w.logger.Error("Failed to acquire container",
			"session_id", payload.SessionID,
			"strategy", strategy.Name(),
			"error", err)
		// 标记 Session Error
		w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusError)
		w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
			Type:    eventbus.EventSessionError,
			Payload: err.Error(),
		})

		return err
	}

	w.logger.Info("Container acquired",
		"session_id", payload.SessionID,
		"container_id", container.ID,
		"container_ip", container.IP)

	// 对于 Cold Strategy，需要等待容器内 gRPC 服务器启动完成
	if _, ok := strategy.(*orchestrator.ColdStrategy); ok {
		w.logger.Info("Waiting for cold container agent server to become ready",
			"session_id", payload.SessionID, "container_id", container.ID)
		if err := waitForAgentServer(ctx, container, 30*time.Second); err != nil {
			w.logger.Error("Cold container agent server not ready",
				"session_id", payload.SessionID, "error", err)
			w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusError)
			w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
				Type:    eventbus.EventSessionError,
				Payload: fmt.Sprintf("cold container agent not ready: %v", err),
			})
			return err
		}
		w.logger.Info("Cold container agent server is ready", "session_id", payload.SessionID)
	}

	if err := w.repo.UpdateSessionContainerInfo(ctx, payload.SessionID, container.ID, container.IP); err != nil {
		w.logger.Error("Failed to update container info", "session_id", payload.SessionID, "error", err)
		w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusError)
		return err
	}

	if err := w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusReady); err != nil {
		w.logger.Error("Failed to update session status to ready", "session_id", payload.SessionID, "error", err)
		return err
	}

	w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
		Type: eventbus.EventSessionReady,
		Payload: map[string]string{
			"container_id": container.ID,
			"node_ip":      container.IP,
			"host_path":    container.HostPath,
		},
	})

	// 对于 Warm Strategy，需要将项目文件同步到容器中
	if _, ok := strategy.(*orchestrator.WarmStrategy); ok {
		projectRoot := filepath.Join(w.config.ProjectDir, payload.ProjectID)
		w.logger.Info("Syncing project files", "project_root", projectRoot, "session_id", payload.SessionID)

		tarReader, err := TarContext(projectRoot)
		if err != nil {
			w.logger.Error("Failed to tar project", "error", err, "session_id", payload.SessionID)
			w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusError)
			w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
				Type:    eventbus.EventSessionError,
				Payload: fmt.Sprintf("failed to tar project: %v", err),
			})
			return err
		}

		if err := container.UploadArchive(ctx, "/", tarReader); err != nil {
			w.logger.Error("Failed to sync project", "error", err, "session_id", payload.SessionID)
			w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusError)
			w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
				Type:    eventbus.EventSessionError,
				Payload: fmt.Sprintf("failed to sync project: %v", err),
			})
			return err
		}

		envReader := GenerateEnvFile(payload.EnvVars)
		if err := container.CopyToContainer(ctx, ".env", envReader); err != nil {
			w.logger.Error("Failed to write .env", "error", err, "session_id", payload.SessionID)
			w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusError)
			w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
				Type:    eventbus.EventSessionError,
				Payload: fmt.Sprintf("failed to write .env: %v", err),
			})
			return err
		}

		// 在 Warm Container 中启动 gRPC 服务器
		w.logger.Info("Starting agent server", "session_id", payload.SessionID, "container_id", container.ID)
		if err := startAgentServer(ctx, container); err != nil {
			w.logger.Error("Failed to start agent server", "error", err, "session_id", payload.SessionID)
			w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusError)
			w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
				Type:    eventbus.EventSessionError,
				Payload: fmt.Sprintf("failed to start agent server: %v", err),
			})
			return err
		}
		w.logger.Info("Agent server started successfully", "session_id", payload.SessionID)
	}

	w.logger.Info("Session create task completed", "session_id", payload.SessionID)
	return nil
}

func waitForAgentServer(ctx context.Context, c *sandbox.Container, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	probeCmd := []string{
		"python3", "-c",
		"import socket; s=socket.socket(); s.settimeout(1); s.connect(('127.0.0.1',50051)); s.close()",
	}

	for {
		result, err := c.Exec(waitCtx, probeCmd, nil, "/")
		if err == nil && result.ExitCode == 0 {
			return nil
		}

		// 检查容器是否正常运行
		if !c.IsRunning(waitCtx) {
			diagCtx, diagCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer diagCancel()
			logs, logErr := c.GetLogs(diagCtx, 50)
			if logErr == nil && logs != nil {
				return fmt.Errorf("container exited unexpectedly; logs: %s%s", logs.Stdout, logs.Stderr)
			}
			return fmt.Errorf("container exited unexpectedly")
		}

		select {
		case <-waitCtx.Done():
			diagCtx, diagCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer diagCancel()
			logResult, logErr := c.Exec(diagCtx, []string{"cat", "/tmp/agent.log"}, nil, "/")
			if logErr == nil {
				return fmt.Errorf("agent server did not become ready within timeout; agent log: %s", logResult.Stdout+logResult.Stderr)
			}
			return fmt.Errorf("agent server did not become ready within timeout")
		case <-time.After(500 * time.Millisecond):
			// 重试
		}
	}
}

func startAgentServer(ctx context.Context, c *sandbox.Container) error {
	// 在后台启动 agent 服务器。
	// warm 容器的主进程是 "tail -f /dev/null"，用于保持容器存活。
	startCmd := []string{
		"sh", "-c",
		"PYTHONPATH=/app nohup python -m src.main > /tmp/agent.log 2>&1 &",
	}
	if _, err := c.Exec(ctx, startCmd, nil, "/app/workspace"); err != nil {
		return fmt.Errorf("failed to exec agent server: %w", err)
	}

	return waitForAgentServer(ctx, c, 30*time.Second)
}
