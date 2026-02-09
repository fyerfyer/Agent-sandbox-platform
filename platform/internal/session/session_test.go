package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"platform/internal/eventbus"
	"platform/internal/orchestrator"
	"platform/internal/session"
	"platform/internal/session/repo"
	"platform/internal/session/worker"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/go-pg/pg/v10"
	"github.com/go-pg/pg/v10/orm"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const (
	testNetworkName = "test-net" // match docker-compose.test.yml
	redisAddr       = "localhost:6383"
	postgresAddr    = "localhost:5432"
	postgresUser    = "test"
	postgresPass    = "test"
	postgresDB      = "testdb"
)

// SessionTestHarness manages the integration test infrastructure
type SessionTestHarness struct {
	t            *testing.T
	dockerClient *client.Client
	networkName  string
	logger       *slog.Logger

	// Service Clients
	pgDB           *pg.DB
	redisClient    *redis.Client
	asynqClient    *asynq.Client
	asynqInspector *asynq.Inspector
}

func NewSessionTestHarness(t *testing.T) *SessionTestHarness {
	t.Helper()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}

	h := &SessionTestHarness{
		t:            t,
		dockerClient: cli,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})),
		networkName:  testNetworkName,
	}

	h.setupConnections()
	return h
}

func (h *SessionTestHarness) setupConnections() {
	ctx := context.Background()

	// 1. Connect to Redis
	h.redisClient = redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	if err := h.redisClient.Ping(ctx).Err(); err != nil {
		h.t.Fatalf("Failed to connect to Redis at %s: %v. Make sure docker-compose.test.yml is running.", redisAddr, err)
	}

	// 2. Connect to Asynq
	h.asynqClient = asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	h.asynqInspector = asynq.NewInspector(asynq.RedisClientOpt{Addr: redisAddr})

	// 3. Connect to Postgres
	h.pgDB = pg.Connect(&pg.Options{
		Addr:     postgresAddr,
		User:     postgresUser,
		Password: postgresPass,
		Database: postgresDB,
	})

	if _, err := h.pgDB.Exec("SELECT 1"); err != nil {
		h.t.Fatalf("Failed to connect to Postgres at %s: %v. Make sure docker-compose.test.yml is running.", postgresAddr, err)
	}

	// Init Schema
	h.initSchema(ctx)
}

func (h *SessionTestHarness) initSchema(ctx context.Context) {
	// Clean table before test
	_, err := h.pgDB.ExecContext(ctx, "DROP TABLE IF EXISTS session_models")
	if err != nil {
		h.t.Logf("Failed to drop table: %v", err)
	}

	err = h.pgDB.Model(&repo.SessionModel{}).CreateTable(&orm.CreateTableOptions{
		IfNotExists: true,
	})
	if err != nil {
		h.t.Fatalf("Failed to create session table: %v", err)
	}
}

func (h *SessionTestHarness) ensureImage(ctx context.Context, imageName string) {
	_, err := h.dockerClient.ImageInspect(ctx, imageName)
	if err == nil {
		return
	}

	h.t.Logf("Pulling image %s...", imageName)
	reader, err := h.dockerClient.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		h.t.Fatalf("Failed to pull image %s: %v", imageName, err)
	}
	defer reader.Close()
	io.Copy(io.Discard, reader)
}

func (h *SessionTestHarness) Cleanup() {
	// Close clients
	if h.pgDB != nil {
		h.pgDB.Close()
	}
	if h.redisClient != nil {
		h.redisClient.Close()
	}
	if h.asynqClient != nil {
		h.asynqClient.Close()
	}
	if h.asynqInspector != nil {
		h.asynqInspector.Close()
	}

	h.dockerClient.Close()
}

func TestSessionRepository(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewSessionTestHarness(t)
	defer h.Cleanup()

	ctx := context.Background()
	r := repo.NewRepository(h.pgDB, h.redisClient)

	// Test case: Create and Get
	t.Run("CreateAndGet", func(t *testing.T) {
		s := &session.Session{
			ID:        uuid.New().String(),
			ProjectID: "proj-1",
			UserID:    "user-1",
			Status:    session.StatusInitializing,
			CreatedAt: time.Now(),
		}

		if err := r.Create(ctx, s); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		got, err := r.GetByID(ctx, s.ID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}

		if got.ID != s.ID || got.Status != s.Status {
			t.Errorf("Session mismatch: got %+v, want %+v", got, s)
		}
	})

	// Test case: Update Status
	t.Run("UpdateStatus", func(t *testing.T) {
		s := &session.Session{
			ID:        uuid.New().String(),
			ProjectID: "proj-2",
			UserID:    "user-2",
			Status:    session.StatusInitializing,
			CreatedAt: time.Now(),
		}
		if err := r.Create(ctx, s); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		if err := r.UpdateSessionStatus(ctx, s.ID, session.StatusRunning); err != nil {
			t.Fatalf("Failed to update status: %v", err)
		}

		got, err := r.GetByID(ctx, s.ID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}
		if got.Status != session.StatusRunning {
			t.Errorf("Expected status Running, got %s", got.Status)
		}
	})

	// Test case: Container Info Update
	t.Run("UpdateContainerInfo", func(t *testing.T) {
		s := &session.Session{
			ID:        uuid.New().String(),
			ProjectID: "proj-3",
			UserID:    "user-3",
			Status:    session.StatusInitializing,
			CreatedAt: time.Now(),
		}
		if err := r.Create(ctx, s); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		containerID := "container-123"
		nodeIP := "10.0.0.5"
		if err := r.UpdateSessionContainerInfo(ctx, s.ID, containerID, nodeIP); err != nil {
			t.Fatalf("Failed to update container info: %v", err)
		}

		got, err := r.GetByID(ctx, s.ID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}
		if got.ContainerID != containerID {
			t.Errorf("Expected ContainerID %s, got %s", containerID, got.ContainerID)
		}
		if got.NodeIP != nodeIP {
			t.Errorf("Expected NodeIP %s, got %s", nodeIP, got.NodeIP)
		}
	})
}

func TestSessionWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewSessionTestHarness(t)
	defer h.Cleanup()

	ctx := context.Background()
	r := repo.NewRepository(h.pgDB, h.redisClient)
	bus := eventbus.NewRedisBus(h.redisClient, h.logger)

	// Setup Pool
	poolCfg := orchestrator.PoolConfig{
		MinIdle:             1,
		MaxBurst:            2,
		WarmupImage:         "alpine:latest",
		HealthCheckInterval: 1 * time.Second,
		NetworkName:         h.networkName,
		ContainerMem:        64,
		ContainerCPU:        0.1,
	}
	p := orchestrator.NewPool(h.dockerClient, h.logger, poolCfg)
	defer p.Shutdown(ctx, nil)

	// Wait for pool warmup
	// We need to ensure the image exists first, otherwise pool warmup might fail or take too long
	h.ensureImage(ctx, "alpine:latest")

	t.Log("Waiting for pool warmup...")
	// Simple wait loop checking docker containers
	for range 30 {
		containers, _ := h.dockerClient.ContainerList(ctx, container.ListOptions{All: true})
		warmCount := 0
		for _, c := range containers {
			if c.State == "running" && c.Image == "alpine:latest" { // simplified check
				warmCount++
			}
		}
		if warmCount >= 1 {
			break
		}
		time.Sleep(1 * time.Second)
	}

	// Create temp dir for projects
	tmpDir, err := os.MkdirTemp("", "session-test-projects")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	w := worker.NewSessionTaskWorker(p, r, bus, worker.WorkerConfig{
		ProjectDir: tmpDir,
	})

	// Test Warm Strategy
	t.Run("WarmStrategy", func(t *testing.T) {
		sessionID := uuid.New().String()
		projectID := "proj-warm"

		// Create session in DB first
		s := &session.Session{
			ID:        sessionID,
			ProjectID: projectID,
			UserID:    "user-warm",
			Status:    session.StatusInitializing,
			Strategy:  orchestrator.WarmStrategyType,
			CreatedAt: time.Now(),
		}
		if err := r.Create(ctx, s); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Prepare project files
		projectPath := fmt.Sprintf("%s/%s", tmpDir, projectID)
		if err := os.MkdirAll(projectPath, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(projectPath+"/main.py", []byte("print('hello')"), 0644); err != nil {
			t.Fatal(err)
		}

		// Subscribe to events
		sub, err := bus.Subscribe(ctx, sessionID)
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}

		// Create Task
		payload := session.SessionCreatePayload{
			SessionID: sessionID,
			ProjectID: projectID,
			UserID:    s.UserID,
			Strategy:  orchestrator.WarmStrategyType,
			EnvVars:   []string{"TEST=warm"},
		}
		payloadBytes, _ := json.Marshal(payload)
		task := asynq.NewTask(session.SessionCreateTask, payloadBytes)

		// Execute Worker Handle
		if err := w.HandleSessionCreate(ctx, task); err != nil {
			t.Fatalf("Worker failed: %v", err)
		}

		// Verify Database Update
		updated, err := r.GetByID(ctx, sessionID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}
		if updated.Status != session.StatusReady {
			t.Errorf("Expected status Ready, got %s", updated.Status)
		}
		if updated.ContainerID == "" {
			t.Error("ContainerID should be set")
		}

		// Verify Event
		select {
		case event := <-sub:
			if event.Type != eventbus.EventSessionReady {
				t.Errorf("Expected event session.ready, got %s", event.Type)
			}
		case <-time.After(5 * time.Second):
			t.Error("Timeout waiting for event")
		}
	})

	// Test Cold Strategy
	t.Run("ColdStrategy", func(t *testing.T) {
		sessionID := uuid.New().String()
		projectID := "proj-cold"

		s := &session.Session{
			ID:        sessionID,
			ProjectID: projectID,
			UserID:    "user-cold",
			Status:    session.StatusInitializing,
			Strategy:  orchestrator.ColdStrategyType,
			CreatedAt: time.Now(),
		}
		if err := r.Create(ctx, s); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		// Subscribe
		sub, err := bus.Subscribe(ctx, sessionID)
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}

		payload := session.SessionCreatePayload{
			SessionID: sessionID,
			ProjectID: projectID,
			UserID:    s.UserID,
			Strategy:  orchestrator.ColdStrategyType,
			Image:     "alpine:latest",
			EnvVars:   []string{"TEST=cold"},
		}
		payloadBytes, _ := json.Marshal(payload)
		task := asynq.NewTask(session.SessionCreateTask, payloadBytes)

		if err := w.HandleSessionCreate(ctx, task); err != nil {
			t.Fatalf("Worker failed: %v", err)
		}

		updated, err := r.GetByID(ctx, sessionID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}
		if updated.Status != session.StatusReady {
			t.Errorf("Expected status Ready, got %s", updated.Status)
		}

		select {
		case event := <-sub:
			if event.Type != eventbus.EventSessionReady {
				t.Errorf("Expected event session.ready, got %s", event.Type)
			}
		case <-time.After(5 * time.Second):
			t.Error("Timeout waiting for event")
		}
	})
}

func TestSessionManager(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewSessionTestHarness(t)
	defer h.Cleanup()

	ctx := context.Background()

	poolCfg := orchestrator.PoolConfig{
		MinIdle:             1,
		MaxBurst:            2,
		WarmupImage:         "alpine:latest",
		HealthCheckInterval: 1 * time.Second,
		NetworkName:         h.networkName,
		ContainerMem:        64,
		ContainerCPU:        0.1,
	}
	p := orchestrator.NewPool(h.dockerClient, h.logger, poolCfg)
	defer p.Shutdown(ctx, nil)

	r := repo.NewRepository(h.pgDB, h.redisClient)
	m := session.NewSessionManager(p, r, h.redisClient, h.asynqClient, h.logger)
	bus := eventbus.NewRedisBus(h.redisClient, h.logger)

	t.Run("CreateSession_EnqueuesTask", func(t *testing.T) {
		params := session.SessionParams{
			ProjectID: "proj-mgr-1",
			UserID:    "user-mgr-1",
			Strategy:  orchestrator.ColdStrategyType,
			ContainerOpts: orchestrator.ContainerOptions{
				Image: "alpine:latest",
			},
			EnvVars: []string{"TEST=mgr"},
		}

		sess, err := m.CreateSession(ctx, params)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		if sess.Status != session.StatusInitializing {
			t.Errorf("Expected status Initializing, got %s", sess.Status)
		}

		// Verify Task in Queue
		time.Sleep(100 * time.Millisecond)

		info, err := h.asynqInspector.GetQueueInfo("default")
		if err != nil {
			t.Fatalf("Failed to get queue info: %v", err)
		}
		if info.Size == 0 {
			t.Error("Queue size is 0, expected > 0")
		}

		// List tasks to find ours
		tasks, err := h.asynqInspector.ListPendingTasks("default", asynq.Page(1), asynq.PageSize(10))
		if err != nil {
			t.Fatalf("Failed to list tasks: %v", err)
		}

		found := false
		for _, task := range tasks {
			if task.Type == session.SessionCreateTask {
				var payload session.SessionCreatePayload
				if err := json.Unmarshal(task.Payload, &payload); err == nil {
					if payload.SessionID == sess.ID {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Error("Task not found in queue")
		}
	})

	t.Run("EndToEnd_WithWorker", func(t *testing.T) {
		// Setup Worker
		tmpDir, err := os.MkdirTemp("", "session-mgr-test")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		w := worker.NewSessionTaskWorker(p, r, bus, worker.WorkerConfig{
			ProjectDir: tmpDir,
		})

		params := session.SessionParams{
			ProjectID: "proj-mgr-e2e",
			UserID:    "user-mgr-e2e",
			Strategy:  orchestrator.ColdStrategyType,
			ContainerOpts: orchestrator.ContainerOptions{
				Image: "alpine:latest",
			},
			EnvVars: []string{"TEST=e2e"},
		}

		sess, err := m.CreateSession(ctx, params)
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		sub, err := bus.Subscribe(ctx, sess.ID)
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}

		// Simulate Worker Process
		payload := session.SessionCreatePayload{
			SessionID: sess.ID,
			ProjectID: params.ProjectID,
			UserID:    params.UserID,
			Strategy:  params.Strategy,
			Image:     params.ContainerOpts.Image,
			EnvVars:   params.EnvVars,
		}
		payloadBytes, _ := json.Marshal(payload)
		task := asynq.NewTask(session.SessionCreateTask, payloadBytes)

		// Create context with timeout for worker
		wCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := w.HandleSessionCreate(wCtx, task); err != nil {
			t.Fatalf("Worker failed: %v", err)
		}

		// Verify Session Ready
		updated, err := r.GetByID(ctx, sess.ID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}
		if updated.Status != session.StatusReady {
			t.Errorf("Expected status Ready, got %s", updated.Status)
		}

		// Verify Event
		select {
		case event := <-sub:
			if event.Type != eventbus.EventSessionReady {
				t.Errorf("Expected event session.ready, got %s", event.Type)
			}
		case <-time.After(5 * time.Second):
			t.Error("Timeout waiting for event")
		}

		// Cleanup container
		if updated.ContainerID != "" {
			h.dockerClient.ContainerRemove(context.Background(), updated.ContainerID, container.RemoveOptions{Force: true})
		}
	})
}
