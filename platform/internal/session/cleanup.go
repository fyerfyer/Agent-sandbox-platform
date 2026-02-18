package session

import (
	"context"
	"log/slog"
	"time"
)

// CleanupConfig 会话清理配置
type CleanupConfig struct {
	Interval time.Duration // 清理循环间隔
	MaxAge   time.Duration // 超过此时间的非终态会话视为僵死
}

// SessionCleaner 定期清理僵死 session（长时间处于 Initializing 或 Running 但容器已不存在的）
type SessionCleaner struct {
	repo        SessionRepository
	terminateFn func(ctx context.Context, sessionID string) error
	logger      *slog.Logger
	config      CleanupConfig
	stopCh      chan struct{}
}

// NewSessionCleaner 创建 session 清理器。
// terminateFn 应当实现完整的 session 终止逻辑（清理容器、compose、companion 等），
// 通常传入 service.Service.TerminateSession。
func NewSessionCleaner(
	repo SessionRepository,
	terminateFn func(ctx context.Context, sessionID string) error,
	config CleanupConfig,
	logger *slog.Logger,
) *SessionCleaner {
	return &SessionCleaner{
		repo:        repo,
		terminateFn: terminateFn,
		logger:      logger.With("component", "session-cleaner"),
		config:      config,
		stopCh:      make(chan struct{}),
	}
}

// Start 启动清理循环（阻塞，应在 goroutine 中调用）
func (c *SessionCleaner) Start() {
	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()

	c.logger.Info("Session cleaner started",
		"interval", c.config.Interval,
		"max_age", c.config.MaxAge,
	)

	for {
		select {
		case <-c.stopCh:
			c.logger.Info("Session cleaner stopped")
			return
		case <-ticker.C:
			c.cleanup()
		}
	}
}

// Stop 停止清理循环
func (c *SessionCleaner) Stop() {
	select {
	case <-c.stopCh:
		// 已经关闭
	default:
		close(c.stopCh)
	}
}

func (c *SessionCleaner) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 查找所有处于 Initializing 或 Running 状态的 session
	staleSessions, err := c.repo.ListByStatus(ctx, []SessionStatus{
		StatusInitializing,
		StatusRunning,
		StatusReady,
	})
	if err != nil {
		c.logger.Error("Failed to list stale sessions", "error", err)
		return
	}

	cutoff := time.Now().Add(-c.config.MaxAge)
	cleaned := 0

	for _, sess := range staleSessions {
		if sess.CreatedAt.Before(cutoff) {
			c.logger.Warn("Cleaning up stale session",
				"session_id", sess.ID,
				"status", sess.Status,
				"created_at", sess.CreatedAt,
				"age", time.Since(sess.CreatedAt),
			)

			if err := c.terminateFn(ctx, sess.ID); err != nil {
				c.logger.Error("Failed to terminate stale session",
					"session_id", sess.ID,
					"error", err,
				)
				// 至少将状态标记为 error
				_ = c.repo.UpdateSessionStatus(ctx, sess.ID, StatusError)
			}
			cleaned++
		}
	}

	if cleaned > 0 {
		c.logger.Info("Session cleanup completed", "cleaned", cleaned)
	}
}

// CleanupAllActive 在平台关闭时清理所有活跃 session。
// 这确保所有容器、compose stack、companion 都被正确释放。
func CleanupAllActive(
	ctx context.Context,
	repo SessionRepository,
	terminateFn func(ctx context.Context, sessionID string) error,
	logger *slog.Logger,
) {
	sessions, err := repo.ListByStatus(ctx, []SessionStatus{
		StatusInitializing,
		StatusReady,
		StatusRunning,
	})
	if err != nil {
		logger.Error("Failed to list active sessions for shutdown cleanup", "error", err)
		return
	}

	if len(sessions) == 0 {
		return
	}

	logger.Info("Cleaning up active sessions on shutdown", "count", len(sessions))
	for _, sess := range sessions {
		logger.Info("Terminating session on shutdown", "session_id", sess.ID)
		if err := terminateFn(ctx, sess.ID); err != nil {
			logger.Error("Failed to terminate session on shutdown",
				"session_id", sess.ID,
				"error", err,
			)
		}
	}
	logger.Info("Shutdown session cleanup completed")
}
