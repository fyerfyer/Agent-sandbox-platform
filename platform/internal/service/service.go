package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"platform/internal/agentproto"
	"platform/internal/dispatcher"
	"platform/internal/eventbus"
	"platform/internal/sandbox"
	"platform/internal/session"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type Service struct {
	SessionMgr  *session.SessionManager
	SessionRepo session.SessionRepository
	Dispatcher  *dispatcher.Dispatcher
	Bus         eventbus.EventBus
	Docker      *client.Client
	Logger      *slog.Logger
	HostRoot    string // 宿主机项目根目录，用于文件同步
	Companions  *CompanionManager
}

func NewService(
	sessionMgr *session.SessionManager,
	sessionRepo session.SessionRepository,
	disp *dispatcher.Dispatcher,
	bus eventbus.EventBus,
	docker *client.Client,
	logger *slog.Logger,
	hostRoot string,
	companions *CompanionManager,
) *Service {
	return &Service{
		SessionMgr:  sessionMgr,
		SessionRepo: sessionRepo,
		Dispatcher:  disp,
		Bus:         bus,
		Docker:      docker,
		Logger:      logger,
		HostRoot:    hostRoot,
		Companions:  companions,
	}
}

func (s *Service) CreateSession(ctx context.Context, params session.SessionParams) (*session.Session, error) {
	return s.SessionMgr.CreateSession(ctx, params)
}

func (s *Service) GetSession(ctx context.Context, id string) (*session.Session, error) {
	return s.SessionMgr.GetSession(ctx, id)
}

func (s *Service) TerminateSession(ctx context.Context, id string) error {
	sess, err := s.SessionMgr.GetSession(ctx, id)
	if err != nil {
		return err
	}

	if s.Companions != nil {
		s.Companions.CleanupSession(ctx, id)
	}

	s.Dispatcher.CleanUp(id)

	if sess.ContainerID != "" {
		timeout := 10
		stopErr := s.Docker.ContainerStop(ctx, sess.ContainerID, container.StopOptions{Timeout: &timeout})
		if stopErr != nil {
			s.Logger.Warn("Failed to stop container", "container_id", sess.ContainerID, "error", stopErr)
		}
		rmErr := s.Docker.ContainerRemove(ctx, sess.ContainerID, container.RemoveOptions{Force: true})
		if rmErr != nil {
			s.Logger.Warn("Failed to remove container", "container_id", sess.ContainerID, "error", rmErr)
		}
	}

	return s.SessionMgr.TerminateSession(ctx, id)
}

// Agent RPC 调用
func (s *Service) ConfigureSession(ctx context.Context, sessionID string, req *agentproto.ConfigureRequest) (*agentproto.ConfigureResponse, error) {
	sess, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	if sess.Status != session.StatusReady && sess.Status != session.StatusRunning {
		return nil, fmt.Errorf("session is not ready (status: %s)", sess.Status)
	}

	if sess.NodeIP == "" {
		return nil, fmt.Errorf("session has no container IP assigned")
	}

	c := &sandbox.Container{
		ID: sess.ContainerID,
		IP: sess.NodeIP,
		Config: sandbox.ContainerConfig{
			SessionID: sess.ID,
			ProjectID: sess.ProjectID,
		},
	}

	req.SessionId = sessionID
	return s.Dispatcher.Configure(ctx, c, req)
}

func (s *Service) StopAgent(ctx context.Context, sessionID string) (*agentproto.StopResponse, error) {
	sess, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	if sess.NodeIP == "" {
		return nil, fmt.Errorf("session has no container IP assigned")
	}

	c := &sandbox.Container{
		ID: sess.ContainerID,
		IP: sess.NodeIP,
		Config: sandbox.ContainerConfig{
			SessionID: sess.ID,
			ProjectID: sess.ProjectID,
		},
	}

	return s.Dispatcher.Stop(ctx, c, sessionID)
}

func (s *Service) SendMessage(ctx context.Context, sessionID string, message string) error {
	sess, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	if sess.Status != session.StatusReady && sess.Status != session.StatusRunning {
		return fmt.Errorf("session is not ready (status: %s)", sess.Status)
	}

	if sess.NodeIP == "" {
		return fmt.Errorf("session has no container IP assigned")
	}

	c := &sandbox.Container{
		ID: sess.ContainerID,
		IP: sess.NodeIP,
		Config: sandbox.ContainerConfig{
			SessionID: sess.ID,
			ProjectID: sess.ProjectID,
		},
	}

	if sess.Status == session.StatusReady {
		if err := s.SessionRepo.UpdateSessionStatus(ctx, sessionID, session.StatusRunning); err != nil {
			s.Logger.Warn("Failed to update session to running", "error", err)
		}
	}

	return s.Dispatcher.Dispatch(ctx, c, message)
}

// 事件订阅
func (s *Service) StreamEvents(ctx context.Context, sessionID string) (<-chan eventbus.Event, error) {
	_, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	return s.Bus.Subscribe(ctx, sessionID)
}

// Agent 辅助容器管理
func (s *Service) CreateCompanionService(ctx context.Context, sessionID string, req CreateServiceRequest) (*CompanionService, error) {
	sess, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	if sess.Status != session.StatusReady && sess.Status != session.StatusRunning {
		return nil, fmt.Errorf("session is not ready (status: %s)", sess.Status)
	}

	if s.Companions == nil {
		return nil, fmt.Errorf("companion service manager not initialized")
	}

	return s.Companions.CreateService(ctx, sessionID, req)
}

func (s *Service) RemoveCompanionService(ctx context.Context, sessionID, serviceID string) error {
	_, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	if s.Companions == nil {
		return fmt.Errorf("companion service manager not initialized")
	}

	return s.Companions.RemoveService(ctx, sessionID, serviceID)
}

func (s *Service) ListCompanionServices(sessionID string) []*CompanionService {
	if s.Companions == nil {
		return nil
	}
	return s.Companions.ListServices(sessionID)
}

func (s *Service) SyncFilesToHost(ctx context.Context, sessionID string, srcPath string, destPath string) error {
	sess, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	if sess.ContainerID == "" {
		return fmt.Errorf("session has no container")
	}

	hostDest := filepath.Join(s.HostRoot, sess.ProjectID)
	if destPath != "" {
		hostDest = filepath.Join(hostDest, destPath)
	}

	if err := os.MkdirAll(hostDest, 0755); err != nil {
		return fmt.Errorf("failed to create host directory: %w", err)
	}

	containerSrc := srcPath
	if containerSrc == "" {
		containerSrc = "/app/workspace/"
	}

	reader, _, err := s.Docker.CopyFromContainer(ctx, sess.ContainerID, containerSrc)
	if err != nil {
		return fmt.Errorf("failed to copy from container: %w", err)
	}
	defer reader.Close()

	if err := extractTarToDir(reader, hostDest); err != nil {
		return fmt.Errorf("failed to extract files: %w", err)
	}

	s.Logger.Info("Files synced from container to host",
		"session_id", sessionID,
		"container_id", sess.ContainerID,
		"container_src", containerSrc,
		"host_dest", hostDest,
	)

	return nil
}

func (s *Service) ListContainerFiles(ctx context.Context, sessionID string, path string) (string, error) {
	sess, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("session not found: %w", err)
	}

	if sess.ContainerID == "" {
		return "", fmt.Errorf("session has no container")
	}

	target := "/app/workspace"
	if path != "" {
		target = filepath.Join(target, path)
	}

	execCfg := container.ExecOptions{
		Cmd:          []string{"ls", "-la", target},
		AttachStdout: true,
		AttachStderr: true,
	}

	resp, err := s.Docker.ContainerExecCreate(ctx, sess.ContainerID, execCfg)
	if err != nil {
		return "", fmt.Errorf("failed to create exec: %w", err)
	}

	attachResp, err := s.Docker.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to attach exec: %w", err)
	}
	defer attachResp.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, attachResp.Reader)

	return buf.String(), nil
}

func (s *Service) ReadContainerFile(ctx context.Context, sessionID string, path string) ([]byte, error) {
	sess, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	if sess.ContainerID == "" {
		return nil, fmt.Errorf("session has no container")
	}

	containerPath := filepath.Join("/app/workspace", path)

	reader, _, err := s.Docker.CopyFromContainer(ctx, sess.ContainerID, containerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to copy from container: %w", err)
	}
	defer reader.Close()

	var buf bytes.Buffer
	if err := extractFirstFileFromTar(reader, &buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Service) HealthCheck(ctx context.Context, sessionID string) (bool, error) {
	sess, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return false, err
	}

	if sess.ContainerID == "" {
		return false, nil
	}

	inspect, inspectErr := s.Docker.ContainerInspect(ctx, sess.ContainerID)
	if inspectErr != nil {
		return false, nil
	}

	return inspect.State.Running, nil
}

func (s *Service) WaitForReady(ctx context.Context, sessionID string, pollInterval time.Duration) (*session.Session, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			sess, err := s.SessionMgr.GetSession(ctx, sessionID)
			if err != nil {
				return nil, err
			}
			switch sess.Status {
			case session.StatusReady, session.StatusRunning:
				return sess, nil
			case session.StatusError, session.StatusTerminated:
				return nil, fmt.Errorf("session failed with status: %s", sess.Status)
			}
		}
	}
}
