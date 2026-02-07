package session

import (
	"context"
	"encoding/json"
	"log/slog"
	"platform/internal/orchestrator"

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

func NewSessionManager(pool orchestrator.IPool, repo SessionRepository, cache redis.Cmdable, queueClient *asynq.Client) *SessionManager {
	return &SessionManager{
		pool:        pool,
		repo:        repo,
		cache:       cache,
		queueClient: queueClient,
	}
}

func (s *SessionManager) CreateSession(ctx context.Context, params SessionParams) (*Session, error) {
	session := &Session{
		ID:        uuid.New().String(),
		ProjectID: params.ProjectID,
		Status:    StatusInitializing,
		Strategy:  params.Strategy,
	}

	if err := s.repo.Create(ctx, session); err != nil {
		return nil, err
	}

	payload, _ := json.Marshal(SessionCreatePayload{
		SessionID: session.ID,
		ProjectID: session.ProjectID,
		UserID:    session.UserID,
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
