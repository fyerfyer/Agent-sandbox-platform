package orchestrator

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

const (
	testNetworkName = "test-orchestrator-net"
	testTimeout     = 60 * time.Second
	testWarmImage   = "alpine:latest"
)

type TestHarness struct {
	t            *testing.T
	dockerClient *client.Client
	networkID    string
	logger       *slog.Logger
	pool         *Pool
}

func NewTestHarness(t *testing.T) *TestHarness {
	t.Helper()

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("Failed to create Docker client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := dockerClient.Ping(ctx); err != nil {
		t.Fatalf("Docker daemon is not available: %v", err)
	}

	h := &TestHarness{
		t:            t,
		dockerClient: dockerClient,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	h.createNetwork()
	return h
}

func (h *TestHarness) createNetwork() {
	ctx := context.Background()

	inspect, err := h.dockerClient.NetworkInspect(ctx, testNetworkName, network.InspectOptions{})
	if err == nil {
		// 清理网络下所有容器
		for containerID := range inspect.Containers {
			h.dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
		}
		// 清理网络
		if err := h.dockerClient.NetworkRemove(ctx, inspect.ID); err != nil {
			h.t.Logf("Warning: failed to remove existing network %s: %v", inspect.ID, err)
		}
	}

	resp, err := h.dockerClient.NetworkCreate(ctx, testNetworkName, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		h.t.Fatalf("Failed to create test network: %v", err)
	}
	h.networkID = resp.ID
	h.t.Logf("Created test network: %s", h.networkID)
}

func (h *TestHarness) Cleanup() {
	ctx := context.Background()

	if h.pool != nil {
		close(h.pool.stopCh)
	}

	// 清理所有容器
	filters := container.ListOptions{
		All: true,
	}
	containers, _ := h.dockerClient.ContainerList(ctx, filters)
	for _, c := range containers {
		if _, ok := c.NetworkSettings.Networks[testNetworkName]; ok {
			h.dockerClient.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
		}
	}

	if h.networkID != "" {
		h.dockerClient.NetworkRemove(ctx, h.networkID)
	}
	h.dockerClient.Close()
}

func TestPoolMaintenance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	cfg := PoolConfig{
		MinIdle:             2,
		MaxBurst:            5,
		WarmupImage:         testWarmImage,
		HealthCheckInterval: 1 * time.Second,
		NetworkName:         testNetworkName,
		ContainerMem:        64,  // 64MB
		ContainerCPU:        0.1, // 0.1 CPU
		DisableHealthCheck:  true,
	}

	p := NewPool(h.dockerClient, h.logger, cfg)
	h.pool = p

	t.Log("Waiting for pool to warmup...")
	time.Sleep(5 * time.Second)

	p.mu.Lock()
	count := len(p.idleContainers)
	p.mu.Unlock()

	if count < 2 {
		t.Errorf("Expected at least 2 idle containers, got %d", count)
	}
	t.Logf("Pool warmed up with %d containers", count)

	// Verify docker state
	ctx := context.Background()
	containers, err := h.dockerClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		t.Fatalf("Failed to list containers: %v", err)
	}

	warmCount := 0
	for _, c := range containers {
		// Check network
		if _, ok := c.NetworkSettings.Networks[testNetworkName]; ok {
			if c.State == "running" {
				warmCount++
			}
		}
	}
	if warmCount < 2 {
		t.Errorf("Expected at least 2 running containers in docker network, got %d", warmCount)
	}
}

func TestAcquireRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	cfg := PoolConfig{
		MinIdle:             1,
		MaxBurst:            2,
		WarmupImage:         testWarmImage,
		HealthCheckInterval: 1 * time.Second,
		NetworkName:         testNetworkName,
		ContainerMem:        64,
		DisableHealthCheck:  true,
	}

	p := NewPool(h.dockerClient, h.logger, cfg)
	h.pool = p

	t.Log("Waiting for pool warmup...")
	time.Sleep(3 * time.Second)

	ctx := context.Background()

	// Acquire
	start := time.Now()
	c, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Failed to acquire: %v", err)
	}
	t.Logf("Acquired container %s in %v", c.ID, time.Since(start))

	// Verify pool state
	p.mu.Lock()
	idle := len(p.idleContainers)
	active := p.managedCount
	p.mu.Unlock()

	if idle != 0 {
		t.Errorf("Expected 0 idle containers, got %d", idle)
	}
	if active != 1 {
		t.Errorf("Expected 1 active container, got %d", active)
	}

	// Verify container is running
	if !c.IsRunning(ctx) {
		t.Error("Acquired container is not running")
	}

	// Release
	t.Log("Releasing container...")
	p.Release(ctx, c)

	// Wait for cleanup (polling managed count to show container is released)
	// Since MinIdle=1, maintainPool will create a replacement, so managedCount may return to 1
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		count := p.managedCount
		idle := len(p.idleContainers)
		p.mu.Unlock()

		// We want to see that the released container cleanup completes
		// and maintainPool refills to MinIdle
		if count == idle && idle == 1 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Wait for container removal from docker
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_, err := h.dockerClient.ContainerInspect(ctx, c.ID)
		if errdefs.IsNotFound(err) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Verify container removed from docker
	inspect, err := h.dockerClient.ContainerInspect(ctx, c.ID)
	if err == nil {
		if inspect.State.Status == "removing" || inspect.State.Dead {
			t.Logf("Container is in %s state, considering removed", inspect.State.Status)
		} else {
			t.Errorf("Container should be removed after release. Status: %s", inspect.State.Status)
		}
	}

	// Verify pool state: With MinIdle=1, pool should have 1 idle container
	p.mu.Lock()
	finalManaged := p.managedCount
	finalIdle := len(p.idleContainers)
	p.mu.Unlock()

	if finalManaged != 1 {
		t.Errorf("Expected managedCount=1 (MinIdle), got %d", finalManaged)
	}
	if finalIdle != 1 {
		t.Errorf("Expected 1 idle container (MinIdle), got %d", finalIdle)
	}
}

func TestConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	// MinIdle 2, MaxBurst 5.
	// 10 Goroutines.
	// 2 should define instant. 3 should burst. 5 should wait.
	cfg := PoolConfig{
		MinIdle:             2,
		MaxBurst:            5,
		WarmupImage:         testWarmImage,
		HealthCheckInterval: 1 * time.Second,
		NetworkName:         testNetworkName,
		ContainerMem:        64,
		DisableHealthCheck:  true,
	}

	p := NewPool(h.dockerClient, h.logger, cfg)
	h.pool = p

	t.Log("Waiting for warmup...")
	time.Sleep(5 * time.Second) // Ensure 2 idle exist

	var wg sync.WaitGroup
	var successCount int32
	var timeoutCount int32

	totalRequests := 10
	wg.Add(totalRequests)

	// We set a moderate timeout. If logic works, latecomers wait for early birds to release.
	// We make early birds hold for a short time (e.g. 2s).
	// Burst creation takes ~1-2s.
	// So latecomers might need ~4-5s total to get a slot.

	for i := range totalRequests {
		go func(id int) {
			defer wg.Done()
			// Give enough time for everyone to cycle through
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			t.Logf("Req %d: Acquiring...", id)
			start := time.Now()
			c, err := p.Acquire(ctx)
			if err != nil {
				t.Logf("Req %d: Failed: %v", id, err)
				atomic.AddInt32(&timeoutCount, 1)
				return
			}
			elapsed := time.Since(start)
			t.Logf("Req %d: Success (ID=%s) in %v", id, c.ID, elapsed)
			atomic.AddInt32(&successCount, 1)

			// Determine if it was "instant" (idle), "burst" (created), or "wait" (blocked).
			// Instant: < 100ms
			// Burst: ~1-2s
			// Wait: > 2s (because we hold for 2s below + burst time)

			// Simulate work
			time.Sleep(2 * time.Second)
			p.Release(context.Background(), c)
		}(i)
	}

	wg.Wait()

	if successCount != int32(totalRequests) {
		t.Errorf("Expected %d successful acquisitions, got %d. Timeouts: %d", totalRequests, successCount, timeoutCount)
	}
}

func TestHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	cfg := PoolConfig{
		MinIdle:             2,
		MaxBurst:            5,
		WarmupImage:         testWarmImage,
		HealthCheckInterval: 2 * time.Second,
		NetworkName:         testNetworkName,
		ContainerMem:        64,
		DisableHealthCheck:  true,
	}

	p := NewPool(h.dockerClient, h.logger, cfg)
	h.pool = p

	t.Log("Waiting for warmup...")
	time.Sleep(5 * time.Second)

	p.mu.Lock()
	if len(p.idleContainers) < 2 {
		p.mu.Unlock()
		// Try waiting more?
		t.Fatalf("Setup failed: expected 2 idle, got %d", len(p.idleContainers))
	}
	victim := p.idleContainers[0]
	p.mu.Unlock()

	t.Logf("Killing victim container: %s", victim.ID)
	// Kill it directly via docker
	timeout := 0
	err := h.dockerClient.ContainerStop(context.Background(), victim.ID, container.StopOptions{Timeout: &timeout})
	if err != nil {
		t.Fatalf("Failed to stop container: %v", err)
	}

	t.Log("Waiting for HealthCheck to detect and replace...")
	// Wait > HealthCheckInterval + replenish time
	time.Sleep(10 * time.Second)

	p.mu.Lock()
	count := len(p.idleContainers)
	var foundVictim bool
	for _, c := range p.idleContainers {
		if c.ID == victim.ID {
			foundVictim = true
		}
	}
	p.mu.Unlock()

	if foundVictim {
		t.Error("Victim container was not removed from pool")
	}
	if count < 2 {
		t.Errorf("Pool failed to replenish to MinIdle, count=%d", count)
	}
	t.Logf("Health check passed: Victim removed, Pool size: %d", count)
}

func TestOrphanedContainerAdoption(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx := context.Background()

	// 1. Manually create an "orphaned" container
	// It must be effectively "running" and have the correct labels/network
	orphanName := "orphan-pool-test"
	orphanCfg := &container.Config{
		Image: testWarmImage,
		Labels: map[string]string{
			"managed_by": "agent-platform",
			"project_id": "pool",
			"session_id": "orphan-session",
		},
		Cmd: []string{"top"}, // Keep running
	}
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(testNetworkName),
		Resources: container.Resources{
			Memory:   64 * 1024 * 1024,
			NanoCPUs: 100000000, // 0.1 CPU
		},
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			testNetworkName: {},
		},
	}

	resp, err := h.dockerClient.ContainerCreate(ctx, orphanCfg, hostCfg, netCfg, nil, orphanName)
	if err != nil {
		t.Fatalf("Failed to create orphan container: %v", err)
	}
	if err := h.dockerClient.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start orphan container: %v", err)
	}
	t.Logf("Created orphan container: %s", resp.ID)

	// Ensure it's running before we start the pool
	time.Sleep(2 * time.Second)

	// 2. Start Pool with MinIdle=2.
	// It should adopt the orphan, so we start with 1 idle (the orphan) + create 1 more to meet MinIdle=2.
	cfg := PoolConfig{
		MinIdle:             2,
		MaxBurst:            5,
		WarmupImage:         testWarmImage,
		HealthCheckInterval: 1 * time.Second,
		NetworkName:         testNetworkName,
		ContainerMem:        64,
		DisableHealthCheck:  true,
	}

	p := NewPool(h.dockerClient, h.logger, cfg)
	h.pool = p

	// allow pool to adopt and fill
	time.Sleep(5 * time.Second)

	p.mu.Lock()
	idleCount := len(p.idleContainers)
	managedCount := p.managedCount
	var adopted bool
	for _, c := range p.idleContainers {
		if c.ID == resp.ID {
			adopted = true
			break
		}
	}
	p.mu.Unlock()

	if !adopted {
		t.Errorf("Pool failed to adopt orphaned container %s", resp.ID)
	}

	// Should have at least MinIdle (2)
	if idleCount < 2 {
		t.Errorf("Expected at least 2 idle containers, got %d", idleCount)
	}

	// Managed count should track the adopted one correctly
	if managedCount < 2 {
		t.Errorf("Expected managedCount >= 2, got %d", managedCount)
	}

	t.Log("Orphan adoption test passed")
}

func TestAgentHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx := context.Background()

	// 1. Create a "healthy" container (listens on 50051)
	healthyName := "healthy-pool-test"
	// Alpine nc: nc -l -p 50051
	// We use a loop to keep it listening after connections
	healthyCmd := []string{"sh", "-c", "while true; do nc -l -p 50051 -e echo ok; done"}
	healthyCfg := &container.Config{
		Image: testWarmImage,
		Labels: map[string]string{
			"managed_by": "agent-platform",
			"project_id": "pool",
			"session_id": "healthy-session",
		},
		Cmd: healthyCmd,
	}
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(testNetworkName),
		Resources: container.Resources{
			Memory:   64 * 1024 * 1024,
			NanoCPUs: 100000000,
		},
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			testNetworkName: {},
		},
	}

	healthyResp, err := h.dockerClient.ContainerCreate(ctx, healthyCfg, hostCfg, netCfg, nil, healthyName)
	if err != nil {
		t.Fatalf("Failed to create healthy container: %v", err)
	}
	if err := h.dockerClient.ContainerStart(ctx, healthyResp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start healthy container: %v", err)
	}

	// 2. Create an "unhealthy" container (does NOT listen)
	unhealthyName := "unhealthy-pool-test"
	unhealthyCfg := &container.Config{
		Image: testWarmImage,
		Labels: map[string]string{
			"managed_by": "agent-platform",
			"project_id": "pool",
			"session_id": "unhealthy-session",
		},
		Cmd: []string{"top"}, // Just runs, doesn't listen
	}
	// Use same host/net config
	unhealthyResp, err := h.dockerClient.ContainerCreate(ctx, unhealthyCfg, hostCfg, netCfg, nil, unhealthyName)
	if err != nil {
		t.Fatalf("Failed to create unhealthy container: %v", err)
	}
	if err := h.dockerClient.ContainerStart(ctx, unhealthyResp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start unhealthy container: %v", err)
	}

	// Give them time to start
	time.Sleep(2 * time.Second)

	// 3. Start Pool
	// Enable HealthCheck
	cfg := PoolConfig{
		MinIdle:             2, // We have 2 orphans, both should be adopted initially
		MaxBurst:            5,
		WarmupImage:         testWarmImage,
		HealthCheckInterval: 2 * time.Second, // Fast check
		NetworkName:         testNetworkName,
		ContainerMem:        64,
		DisableHealthCheck:  false, // ENABLED
	}

	p := NewPool(h.dockerClient, h.logger, cfg)
	h.pool = p

	// Wait for adoption and initial health check
	// 5s for adoption/stable, then + HealthCheckInterval
	t.Log("Waiting for pool to adopt and check health...")
	time.Sleep(6 * time.Second)

	p.mu.Lock()
	idleCount := len(p.idleContainers)
	var foundHealthy, foundUnhealthy bool
	for _, c := range p.idleContainers {
		if c.ID == healthyResp.ID {
			foundHealthy = true
		}
		if c.ID == unhealthyResp.ID {
			foundUnhealthy = true
		}
	}
	p.mu.Unlock()

	// Healthy should be active (adopted and kept)
	if !foundHealthy {
		// It might have been replaced if it failed check? But it should pass.
		// Or maybe it wasn't adopted?
		t.Error("Healthy container was NOT found in pool (should be adopted and kept)")
	}

	// Unhealthy should be removed
	if foundUnhealthy {
		t.Error("Unhealthy container WAS found in pool (should be removed)")
	}

	// Pool should still maintain MinIdle=2, but since our "warmup" image doesn't actually listen on 50051,
	// the replacements will also fail health checks and be removed.
	// So we might see idleCount fluctuating.
	// We MUST ensure the HEALTHY one is still there (so at least 1).
	if idleCount < 1 {
		t.Errorf("Pool size dropped below 1 (healthy container missing?): %d", idleCount)
	}

	t.Log("Agent health check test passed")
}
