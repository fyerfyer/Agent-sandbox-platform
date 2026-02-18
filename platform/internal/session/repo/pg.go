package repo

import (
	"context"
	"encoding/json"
	"platform/internal/session"

	"github.com/go-pg/pg/v10"
	"github.com/redis/go-redis/v9"
)

var _ session.SessionRepository = (*Repository)(nil)

type Repository struct {
	db    *pg.DB
	redis redis.Cmdable
}

func NewRepository(db *pg.DB, redis redis.Cmdable) *Repository {
	return &Repository{
		db:    db,
		redis: redis,
	}
}

func (r *Repository) Create(ctx context.Context, session *session.Session) error {
	sessionModel := &SessionModel{
		ID:            session.ID,
		ProjectID:     session.ProjectID,
		UserID:        session.UserID,
		SessionStatus: session.Status,
		Strategy:      session.Strategy,
		CreatedAt:     session.CreatedAt,
	}

	_, err := r.db.Model(sessionModel).Insert()
	if err != nil {
		return err
	}

	return nil
}

func (r *Repository) GetByID(ctx context.Context, id string) (*session.Session, error) {
	if r.redis != nil {
		key := sessionCacheKey(id)
		val, err := r.redis.Get(ctx, key).Result()
		if err == nil {
			var cachedSession cacheSession
			if err := json.Unmarshal([]byte(val), &cachedSession); err == nil {
				sess := &session.Session{
					ID:          cachedSession.ID,
					ProjectID:   cachedSession.ProjectID,
					UserID:      cachedSession.UserID,
					ContainerID: cachedSession.ContainerID,
					NodeIP:      cachedSession.NodeIP,
					Status:      cachedSession.Status,
					Strategy:    cachedSession.Strategy,
					CreatedAt:   cachedSession.CreatedAt,
				}

				return sess, nil
			}
		}
	}

	sessionModel := &SessionModel{ID: id}
	err := r.db.Model(sessionModel).WherePK().Select()
	if err != nil {
		return nil, err
	}

	sess := &session.Session{
		ID:          sessionModel.ID,
		ProjectID:   sessionModel.ProjectID,
		UserID:      sessionModel.UserID,
		ContainerID: sessionModel.ContainerID,
		NodeIP:      sessionModel.NodeIP,
		Status:      sessionModel.SessionStatus,
		Strategy:    sessionModel.Strategy,
		CreatedAt:   sessionModel.CreatedAt,
	}

	if r.redis != nil {
		key := sessionCacheKey(id)
		cachedSession := &cacheSession{
			ID:          sessionModel.ID,
			ProjectID:   sessionModel.ProjectID,
			UserID:      sessionModel.UserID,
			ContainerID: sessionModel.ContainerID,
			NodeIP:      sessionModel.NodeIP,
			Status:      sessionModel.SessionStatus,
			Strategy:    sessionModel.Strategy,
			CreatedAt:   sessionModel.CreatedAt,
		}

		if b, err := json.Marshal(cachedSession); err == nil {
			_ = r.redis.Set(ctx, key, b, sessionCacheTTL).Err()
		}
	}

	return sess, nil
}

func (r *Repository) UpdateSessionStatus(ctx context.Context, id string, status session.SessionStatus) error {
	_, err := r.db.Model(&SessionModel{}).
		Set("session_status = ?", status).
		Where("id = ?", id).
		Update()
	if err != nil {
		return err
	}

	// Invalidate cache
	if r.redis != nil {
		_ = r.redis.Del(ctx, sessionCacheKey(id)).Err()
	}

	return nil
}

func (r *Repository) UpdateSessionContainerInfo(ctx context.Context, id string, containerID, nodeIP string) error {
	_, err := r.db.Model(&SessionModel{}).
		Set("container_id = ?, node_ip = ?", containerID, nodeIP).
		Where("id = ?", id).
		Update()
	if err != nil {
		return err
	}

	// 缓存失效
	if r.redis != nil {
		_ = r.redis.Del(ctx, sessionCacheKey(id)).Err()
	}

	return nil
}

func (r *Repository) ListByStatus(ctx context.Context, statuses []session.SessionStatus) ([]*session.Session, error) {
	var models []SessionModel
	err := r.db.Model(&models).
		Where("session_status IN (?)", pg.In(statuses)).
		Order("created_at DESC").
		Select()
	if err != nil {
		return nil, err
	}

	sessions := make([]*session.Session, 0, len(models))
	for _, m := range models {
		sessions = append(sessions, &session.Session{
			ID:          m.ID,
			ProjectID:   m.ProjectID,
			UserID:      m.UserID,
			ContainerID: m.ContainerID,
			NodeIP:      m.NodeIP,
			Status:      m.SessionStatus,
			Strategy:    m.Strategy,
			CreatedAt:   m.CreatedAt,
		})
	}
	return sessions, nil
}

func (r *Repository) ListByProject(ctx context.Context, projectID string) ([]*session.Session, error) {
	var models []SessionModel
	err := r.db.Model(&models).
		Where("project_id = ?", projectID).
		Order("created_at DESC").
		Limit(50).
		Select()
	if err != nil {
		return nil, err
	}

	sessions := make([]*session.Session, 0, len(models))
	for _, m := range models {
		sessions = append(sessions, &session.Session{
			ID:          m.ID,
			ProjectID:   m.ProjectID,
			UserID:      m.UserID,
			ContainerID: m.ContainerID,
			NodeIP:      m.NodeIP,
			Status:      m.SessionStatus,
			Strategy:    m.Strategy,
			CreatedAt:   m.CreatedAt,
		})
	}
	return sessions, nil
}
