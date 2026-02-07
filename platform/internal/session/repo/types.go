package repo

import (
	"platform/internal/session"
	"time"
)

const sessionCacheTTL = time.Minute * 5

type SessionModel struct {
	ID            string                `json:"id" pg:"id,pk"`
	ProjectID     string                `json:"project_id" pg:"project_id,notnull"`
	UserID        string                `json:"user_id" pg:"user_id,notnull"`
	NodeIP        string                `json:"node_ip" pg:"node_ip,notnull"`
	ContainerID   string                `json:"container_id" pg:"container_id,notnull"`
	SessionStatus session.SessionStatus `json:"session_status" pg:"session_status,notnull"`
	CreatedAt     time.Time             `json:"created_at" pg:"created_at,notnull	"`
}

type cacheSession struct {
	ID        string                `json:"id"`
	ProjectID string                `json:"project_id"`
	UserID    string                `json:"user_id"`
	Status    session.SessionStatus `json:"status"`
	CreatedAt time.Time             `json:"created_at"`
}

func sessionCacheKey(sessionID string) string {
	return "session:" + sessionID + ":location"
}
