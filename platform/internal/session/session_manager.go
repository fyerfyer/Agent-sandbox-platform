package session

import (
	"context"
	"encoding/json"
	"log/slog"
	"platform/internal/orchestrator"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

type SessionManager struct {
	pool        orchestrator.IPool
	repo        SessionRepository
	cache       redis.Cmdable
	queueClient *asynq.Client
	logger      *slog.Logger
}

func NewSessionManager(pool orchestrator.IPool, repo SessionRepository, cache redis.Cmdable, queueClient *asynq.Client, logger *slog.Logger) *SessionManager {
	return &SessionManager{
		pool:        pool,
		repo:        repo,
		cache:       cache,
		queueClient: queueClient,
		logger:      logger,
	}
}

func (s *SessionManager) CreateSession(ctx context.Context, params SessionParams) (*Session, error) {
	session := &Session{
		ID:        uuid.New().String(),
		ProjectID: params.ProjectID,
		UserID:    params.UserID,
		Status:    StatusInitializing,
		Strategy:  params.Strategy,
		CreatedAt: time.Now(),
	}

	if err := s.repo.Create(ctx, session); err != nil {
		return nil, err
	}

	payload, _ := json.Marshal(SessionCreatePayload{
		SessionID: session.ID,
		ProjectID: session.ProjectID,
		UserID:    session.UserID,
		Image:     params.ContainerOpts.Image,
		Strategy:  session.Strategy,
		EnvVars:   params.EnvVars,
	})

	task := asynq.NewTask(SessionCreateTask, payload)

	info, err := s.queueClient.Enqueue(task)
	if err != nil {
		// TODO：错误处理
		return nil, err
	}

	s.logger.Info("Session created", slog.String("session_id", session.ID), slog.String("task_id", info.ID))
	return session, nil
}

func (s *SessionManager) GetSession(ctx context.Context, id string) (*Session, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *SessionManager) TerminateSession(ctx context.Context, id string) error {
	return s.repo.UpdateSessionStatus(ctx, id, StatusTerminated)
}
