package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"platform/internal/eventbus"
	"platform/internal/orchestrator"
	"platform/internal/session"

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
}

func NewSessionTaskWorker(pool orchestrator.IPool, repo session.SessionRepository, bus eventbus.EventBus, config WorkerConfig) *SessionTaskWorker {
	return &SessionTaskWorker{
		pool:   pool,
		repo:   repo,
		bus:    bus,
		config: config,
	}
}

func (w *SessionTaskWorker) HandleSessionCreate(ctx context.Context, task *asynq.Task) error {
	var payload session.SessionCreatePayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("json unmarshal error: %w", err)
	}

	var strategy orchestrator.ContainerStrategy
	switch payload.Strategy {
	case orchestrator.WarmStrategyType:
		strategy = &orchestrator.WarmStrategy{}
	case orchestrator.ColdStrategyType:
		strategy = &orchestrator.ColdStrategy{}
	default:
		return fmt.Errorf("unknown strategy type: %s", payload.Strategy)
	}

	containerOptions := orchestrator.ContainerOptions{
		ProjectID: payload.ProjectID,
		SessionID: payload.SessionID,
		EnvVars:   payload.EnvVars,
		Image:     payload.Image,
	}

	container, err := strategy.Get(ctx, w.pool, containerOptions)
	if err != nil {
		// 标记 Session Error
		w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusError)
		w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
			Type:    eventbus.EventSessionError,
			Payload: err.Error(),
		})

		return err
	}

	if err := w.repo.UpdateSessionContainerInfo(ctx, payload.SessionID, container.ID, container.IP); err != nil {
		return err
	}

	if err := w.repo.UpdateSessionStatus(ctx, payload.SessionID, session.StatusReady); err != nil {
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

		tarReader, err := TarContext(projectRoot)
		if err != nil {
			w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
				Type:    eventbus.EventSessionError,
				Payload: fmt.Sprintf("failed to tar project: %v", err),
			})
			return err
		}

		if err := container.UploadArchive(ctx, "/", tarReader); err != nil {
			w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
				Type:    eventbus.EventSessionError,
				Payload: fmt.Sprintf("failed to sync project: %v", err),
			})
			return err
		}

		envReader := GenerateEnvFile(payload.EnvVars)
		if err := container.WriteFile(ctx, ".env", envReader, 0644); err != nil {
			w.bus.Publish(ctx, payload.SessionID, eventbus.Event{
				Type:    eventbus.EventSessionError,
				Payload: fmt.Sprintf("failed to write .env: %v", err),
			})
			return err
		}
	}

	return nil
}
