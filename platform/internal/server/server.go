package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"platform/internal/api"
	"platform/internal/config"
	"platform/internal/dispatcher"
	"platform/internal/eventbus"
	"platform/internal/monitor"
	"platform/internal/orchestrator"
	"platform/internal/service"
	"platform/internal/session"
	"platform/internal/session/repo"
	"platform/internal/session/worker"

	"github.com/hibiken/asynq"
)

type Server struct {
	cfg         *config.Config
	deps        *Dependency
	httpServer  *http.Server
	asynqServer *asynq.Server
	asynqMux    *asynq.ServeMux
	pool        *orchestrator.Pool
	logger      *slog.Logger
}

func NewServer(cfg *config.Config, deps *Dependency) *Server {
	logger := deps.Logger

	bus := eventbus.NewRedisBus(deps.Redis, logger)

	pool := orchestrator.NewPool(deps.Docker, logger, orchestrator.PoolConfig{
		MinIdle:             cfg.Pool.MinIdle,
		MaxBurst:            cfg.Pool.MaxBurst,
		WarmupImage:         cfg.Pool.WarmupImage,
		HealthCheckInterval: cfg.Pool.HealthCheckInterval,
		NetworkName:         cfg.Pool.NetworkName,
		HostRoot:            cfg.Pool.HostRoot,
		ContainerMem:        cfg.Pool.ContainerMem,
		ContainerCPU:        cfg.Pool.ContainerCPU,
	})

	sessionRepo := repo.NewRepository(deps.PG, deps.Redis)
	sessionMgr := session.NewSessionManager(pool, sessionRepo, deps.Redis, deps.AsynqClient, logger)
	disp := dispatcher.NewDispatcher(bus, logger)
	svc := service.NewService(sessionMgr, sessionRepo, disp, bus, deps.Docker, logger)

	sessionWorker := worker.NewSessionTaskWorker(pool, sessionRepo, bus, worker.WorkerConfig{
		ProjectDir: cfg.Worker.ProjectDir,
	})

	asynqServer := asynq.NewServer(deps.AsynqRedis, asynq.Config{
		Concurrency: cfg.Worker.Concurrency,
		Logger:      newAsynqLogger(logger),
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(session.SessionCreateTask, sessionWorker.HandleSessionCreate)

	router := api.NewRouter(svc)
	httpServer := &http.Server{
		Addr:         cfg.Server.Addr,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	s := &Server{
		cfg:         cfg,
		deps:        deps,
		httpServer:  httpServer,
		asynqServer: asynqServer,
		asynqMux:    mux,
		pool:        pool,
		logger:      logger,
	}

	return s
}

func (s *Server) Start(ctx context.Context) error {
	go func() {
		s.logger.Info("Starting Asynq worker", "concurrency", s.cfg.Worker.Concurrency)
		if err := s.asynqServer.Start(s.asynqMux); err != nil {
			s.logger.Error("Asynq worker failed", "error", err)
		}
	}()

	go func() {
		if err := monitor.StartMetricsServer(ctx, s.cfg.Metrics.Addr, s.logger); err != nil {
			s.logger.Error("Metrics server failed", "error", err)
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("Starting API server", "addr", s.cfg.Server.Addr)
		if err := s.httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		s.logger.Info("Shutdown signal received, draining...")
	case err := <-errCh:
		return err
	}

	return s.Shutdown()
}

func (s *Server) Shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		s.logger.Error("HTTP server shutdown error", "error", err)
	}

	s.asynqServer.Shutdown()

	s.pool.Shutdown(shutdownCtx, nil)

	s.logger.Info("Server stopped gracefully")
	return nil
}

type asynqLogger struct {
	l *slog.Logger
}

func newAsynqLogger(l *slog.Logger) *asynqLogger {
	return &asynqLogger{l: l.With("component", "asynq")}
}

func (a *asynqLogger) Debug(args ...any) { a.l.Debug("", "msg", args) }
func (a *asynqLogger) Info(args ...any)  { a.l.Info("", "msg", args) }
func (a *asynqLogger) Warn(args ...any)  { a.l.Warn("", "msg", args) }
func (a *asynqLogger) Error(args ...any) { a.l.Error("", "msg", args) }
func (a *asynqLogger) Fatal(args ...any) { a.l.Error("FATAL", "msg", args) }
