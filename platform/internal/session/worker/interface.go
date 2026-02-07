package worker

import (
	"context"

	"github.com/hibiken/asynq"
)

type SessionWorker interface {
	HandleSessionCreate(ctx context.Context, task *asynq.Task) error
}
