package service

import (
	"context"
	"fmt"
	"log/slog"
	"platform/internal/dispatcher"
	"platform/internal/eventbus"
	"platform/internal/sandbox"
	"platform/internal/session"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Service coordinates between session management, dispatch, and event streaming.
type Service struct {
	SessionMgr  *session.SessionManager
	SessionRepo session.SessionRepository
	Dispatcher  *dispatcher.Dispatcher
	Bus         eventbus.EventBus
	Docker      *client.Client
	Logger      *slog.Logger
}

func NewService(
	sessionMgr *session.SessionManager,
	sessionRepo session.SessionRepository,
	disp *dispatcher.Dispatcher,
	bus eventbus.EventBus,
	docker *client.Client,
	logger *slog.Logger,
) *Service {
	return &Service{
		SessionMgr:  sessionMgr,
		SessionRepo: sessionRepo,
		Dispatcher:  disp,
		Bus:         bus,
		Docker:      docker,
		Logger:      logger,
	}
}

// CreateSession creates a new session and enqueues the container provisioning task.
func (s *Service) CreateSession(ctx context.Context, params session.SessionParams) (*session.Session, error) {
	return s.SessionMgr.CreateSession(ctx, params)
}

// GetSession retrieves a session by ID.
func (s *Service) GetSession(ctx context.Context, id string) (*session.Session, error) {
	return s.SessionMgr.GetSession(ctx, id)
}

// TerminateSession stops the container, cleans up gRPC connection, and marks session terminated.
func (s *Service) TerminateSession(ctx context.Context, id string) error {
	sess, err := s.SessionMgr.GetSession(ctx, id)
	if err != nil {
		return err
	}

	// Clean up gRPC connection
	s.Dispatcher.CleanUp(id)

	// Stop and remove container
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

	// Update session status
	return s.SessionMgr.TerminateSession(ctx, id)
}

// SendMessage dispatches a user message to the agent running in the session's container.
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

	// Build a sandbox.Container stub with the info we need for dispatch
	c := &sandbox.Container{
		ID: sess.ContainerID,
		IP: sess.NodeIP,
		Config: sandbox.ContainerConfig{
			SessionID: sess.ID,
			ProjectID: sess.ProjectID,
		},
	}

	// Update session to running
	if sess.Status == session.StatusReady {
		if err := s.SessionRepo.UpdateSessionStatus(ctx, sessionID, session.StatusRunning); err != nil {
			s.Logger.Warn("Failed to update session to running", "error", err)
		}
	}

	return s.Dispatcher.Dispatch(ctx, c, message)
}

// StreamEvents subscribes to a session's event stream via EventBus.
func (s *Service) StreamEvents(ctx context.Context, sessionID string) (<-chan eventbus.Event, error) {
	// Verify session exists
	_, err := s.SessionMgr.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %w", err)
	}

	return s.Bus.Subscribe(ctx, sessionID)
}

// HealthCheck verifies that the agent in a session's container is responsive.
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

// WaitForReady polls until the session reaches Ready status or the context is cancelled.
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
			// Still initializing, continue polling
		}
	}
}
