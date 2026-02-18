package session

import "context"

type SessionRepository interface {
	Create(ctx context.Context, session *Session) error
	GetByID(ctx context.Context, id string) (*Session, error)
	UpdateSessionStatus(ctx context.Context, id string, status SessionStatus) error
	UpdateSessionContainerInfo(ctx context.Context, id string, containerID, nodeIP string) error
	ListByStatus(ctx context.Context, statuses []SessionStatus) ([]*Session, error)
	ListByProject(ctx context.Context, projectID string) ([]*Session, error)
}
