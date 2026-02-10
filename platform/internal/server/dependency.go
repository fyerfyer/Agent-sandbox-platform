package server

import (
	"context"
	"fmt"
	"log/slog"

	"platform/internal/config"
	"platform/internal/session/repo"

	"github.com/docker/docker/client"
	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// Dependency 管理所有基础设施
type Dependency struct {
	Docker      *client.Client
	Redis       *redis.Client
	PG          *pg.DB
	AsynqClient *asynq.Client
	AsynqRedis  asynq.RedisClientOpt
	Logger      *slog.Logger
}

func InitDeps(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*Dependency, error) {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	if _, err := dockerClient.Ping(ctx); err != nil {
		dockerClient.Close()
		return nil, fmt.Errorf("docker ping: %w", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		dockerClient.Close()
		return nil, fmt.Errorf("redis ping (%s): %w", cfg.Redis.Addr, err)
	}

	pgDB := pg.Connect(&pg.Options{
		Addr:     cfg.Postgres.Addr,
		User:     cfg.Postgres.User,
		Password: cfg.Postgres.Password,
		Database: cfg.Postgres.Database,
	})
	if _, err := pgDB.Exec("SELECT 1"); err != nil {
		redisClient.Close()
		dockerClient.Close()
		return nil, fmt.Errorf("postgres ping (%s): %w", cfg.Postgres.Addr, err)
	}

	// 迁移数据库 schema
	if err := pgDB.Model(&repo.SessionModel{}).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	}); err != nil {
		pgDB.Close()
		redisClient.Close()
		dockerClient.Close()
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}

	asynqRedisOpt := asynq.RedisClientOpt{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
	}
	asynqClient := asynq.NewClient(asynqRedisOpt)

	return &Dependency{
		Docker:      dockerClient,
		Redis:       redisClient,
		PG:          pgDB,
		AsynqClient: asynqClient,
		AsynqRedis:  asynqRedisOpt,
		Logger:      logger,
	}, nil
}

func (d *Dependency) Close() {
	if d.AsynqClient != nil {
		d.AsynqClient.Close()
	}
	if d.PG != nil {
		d.PG.Close()
	}
	if d.Redis != nil {
		d.Redis.Close()
	}
	if d.Docker != nil {
		d.Docker.Close()
	}
}
