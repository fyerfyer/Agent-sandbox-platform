package sandbox_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"platform/internal/sandbox"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

const (
	testImage       = "alpine:latest"
	testNetworkName = "test-agent-net"
	testTimeout     = 60 * time.Second
)

// TestHarness 管理测试基础设施
type TestHarness struct {
	t            *testing.T
	dockerClient *client.Client
	networkID    string
	containers   []string
	hostRoot     string
	logger       *slog.Logger
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

	// 创建临时目录用于主机挂载
	hostRoot, err := os.MkdirTemp("", "sandbox-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	h := &TestHarness{
		t:            t,
		dockerClient: dockerClient,
		hostRoot:     hostRoot,
		logger:       slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}

	// 创建测试网络
	h.createNetwork()

	return h
}

func (h *TestHarness) createNetwork() {
	ctx := context.Background()

	h.dockerClient.NetworkRemove(ctx, testNetworkName)

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

	for _, containerID := range h.containers {
		h.dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
		h.t.Logf("Cleaned up container: %s", containerID)
	}

	if h.networkID != "" {
		h.dockerClient.NetworkRemove(ctx, h.networkID)
		h.t.Logf("Cleaned up network: %s", h.networkID)
	}

	if h.hostRoot != "" {
		os.RemoveAll(h.hostRoot)
	}

	h.dockerClient.Close()
}

func (h *TestHarness) TrackContainer(containerID string) {
	h.containers = append(h.containers, containerID)
}

func (h *TestHarness) NewContainer(sessionID, projectID string) *sandbox.Container {
	cfg := sandbox.ContainerConfig{
		SessionID:   sessionID,
		ProjectID:   projectID,
		Image:       testImage,
		EnvVars:     []string{"TEST_VAR=hello"},
		MemoryLimit: 128 * 1024 * 1024, // 128MB
		CPULimit:    1,
		NetworkName: testNetworkName,
	}
	return sandbox.NewContainer(h.dockerClient, cfg, h.hostRoot, h.logger)
}

func (h *TestHarness) NewContainerWithConfig(cfg sandbox.ContainerConfig) *sandbox.Container {
	return sandbox.NewContainer(h.dockerClient, cfg, h.hostRoot, h.logger)
}

func TestContainerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sessionID := fmt.Sprintf("test-lifecycle-%d", time.Now().UnixNano())
	projectID := "test-project"

	t.Run("NewContainer_Start_Stop_Remove", func(t *testing.T) {
		c := h.NewContainer(sessionID, projectID)

		t.Log("Starting container...")
		if err := c.Start(ctx); err != nil {
			t.Fatalf("Failed to start container: %v", err)
		}
		h.TrackContainer(c.ID)
		t.Logf("Container started with ID: %s", c.ID)

		// Verify: Container exists and is running using docker inspect
		inspect, err := h.dockerClient.ContainerInspect(ctx, c.ID)
		if err != nil {
			t.Fatalf("Failed to inspect container: %v", err)
		}
		if !inspect.State.Running {
			t.Errorf("Container should be running, but state is: %s", inspect.State.Status)
		}
		t.Logf("Container state verified: Running=%v", inspect.State.Running)

		// Verify: Container has correct labels
		if inspect.Config.Labels["session_id"] != sessionID {
			t.Errorf("Expected session_id label %s, got %s", sessionID, inspect.Config.Labels["session_id"])
		}
		if inspect.Config.Labels["managed_by"] != "agent-platform" {
			t.Errorf("Expected managed_by label 'agent-platform', got %s", inspect.Config.Labels["managed_by"])
		}

		// Verify: Container has IP address
		if c.IP == "" {
			t.Error("Container should have an IP address")
		}
		t.Logf("Container IP: %s", c.IP)

		// Step 2: Get status via sandbox API
		status, err := c.GetStatus(ctx)
		if err != nil {
			t.Fatalf("Failed to get status: %v", err)
		}
		if status != "running" {
			t.Errorf("Expected status 'running', got '%s'", status)
		}

		// Step 3: Stop container
		t.Log("Stopping container...")
		if err := c.Stop(ctx, 10); err != nil {
			t.Fatalf("Failed to stop container: %v", err)
		}

		// Verify: Container is stopped
		inspect, err = h.dockerClient.ContainerInspect(ctx, c.ID)
		if err != nil {
			t.Fatalf("Failed to inspect container after stop: %v", err)
		}
		if inspect.State.Running {
			t.Error("Container should be stopped, but is still running")
		}
		t.Logf("Container stopped, state: %s", inspect.State.Status)

		// Step 4: Remove container
		t.Log("Removing container...")
		if err := c.Remove(ctx); err != nil {
			t.Fatalf("Failed to remove container: %v", err)
		}

		// Verify: Container no longer exists
		_, err = h.dockerClient.ContainerInspect(ctx, c.ID)
		if err == nil {
			t.Error("Container should not exist after removal")
		}
		t.Log("Container removed successfully")
	})
}

func TestFileOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sessionID := fmt.Sprintf("test-files-%d", time.Now().UnixNano())
	projectID := "test-project"
	c := h.NewContainer(sessionID, projectID)

	// Start container
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	h.TrackContainer(c.ID)

	t.Run("WriteFile_And_ReadBack", func(t *testing.T) {
		testContent := "#!/bin/sh\necho 'Hello from test script'\n"
		testFile := "test-script.sh"

		t.Log("Writing file via sandbox API...")
		err := c.WriteFile(ctx, testFile, strings.NewReader(testContent), 0755)
		if err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}

		// Verify file exists on host
		hostPath := filepath.Join(h.hostRoot, projectID, testFile)
		hostContent, err := os.ReadFile(hostPath)
		if err != nil {
			t.Fatalf("Failed to read file from host: %v", err)
		}
		if string(hostContent) != testContent {
			t.Errorf("Host content mismatch.\nExpected: %q\nGot: %q", testContent, string(hostContent))
		}
		t.Logf("Host file verified at: %s", hostPath)

		// Verify file via Exec (cat command inside container)
		t.Log("Verifying file via exec cat...")
		result, err := c.Exec(ctx, []string{"cat", testFile}, nil, "")
		if err != nil {
			t.Fatalf("Failed to exec cat: %v", err)
		}
		if !strings.Contains(result.Stdout, "Hello from test script") {
			t.Errorf("Exec cat should return file content.\nGot stdout: %q\nStderr: %q", result.Stdout, result.Stderr)
		}
		t.Logf("Exec cat result: exit_code=%d", result.ExitCode)

		// Verify file via OpenFile (read back)
		t.Log("Reading file via OpenFile...")
		reader, err := c.OpenFile(ctx, testFile)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		defer reader.Close()

		var buf bytes.Buffer
		_, err = buf.ReadFrom(reader)
		if err != nil {
			t.Fatalf("Failed to read from file: %v", err)
		}
		if buf.String() != testContent {
			t.Errorf("OpenFile content mismatch.\nExpected: %q\nGot: %q", testContent, buf.String())
		}
		t.Log("OpenFile verification passed")
	})

	t.Run("ListFiles", func(t *testing.T) {
		// Create multiple files
		files := []string{"file1.txt", "file2.txt", "subdir/file3.txt"}
		for _, f := range files {
			dir := filepath.Dir(filepath.Join(h.hostRoot, projectID, f))
			_ = os.MkdirAll(dir, 0755)
			err := os.WriteFile(filepath.Join(h.hostRoot, projectID, f), []byte("content"), 0644)
			if err != nil {
				t.Fatalf("Failed to create test file %s: %v", f, err)
			}
		}

		// List root directory
		t.Log("Listing files in root...")
		fileInfos, err := c.ListFiles(ctx, ".")
		if err != nil {
			t.Fatalf("Failed to list files: %v", err)
		}

		// Should see file1.txt, file2.txt, subdir, and test-script.sh from previous test
		foundCount := 0
		for _, fi := range fileInfos {
			t.Logf("  Found: %s (dir=%v, size=%d)", fi.Path, fi.IsDir, fi.Size)
			foundCount++
		}
		if foundCount < 3 {
			t.Errorf("Expected at least 3 items, got %d", foundCount)
		}
	})

	t.Run("FilePermissions", func(t *testing.T) {
		execFile := "executable.sh"
		content := "#!/bin/sh\necho test"

		// Write with executable permissions
		err := c.WriteFile(ctx, execFile, strings.NewReader(content), 0755)
		if err != nil {
			t.Fatalf("Failed to write executable: %v", err)
		}

		// Check file permissions on host
		hostPath := filepath.Join(h.hostRoot, projectID, execFile)
		info, err := os.Stat(hostPath)
		if err != nil {
			t.Fatalf("Failed to stat file: %v", err)
		}

		// Check if executable bit is set
		perm := info.Mode().Perm()
		if perm&0100 == 0 {
			t.Errorf("Expected executable permission, got %o", perm)
		}
		t.Logf("File permissions: %o", perm)

		// Execute the script
		result, err := c.Exec(ctx, []string{"sh", execFile}, nil, "")
		if err != nil {
			t.Fatalf("Failed to exec script: %v", err)
		}
		if result.ExitCode != 0 {
			t.Errorf("Script should exit with 0, got %d", result.ExitCode)
		}
	})
}

func TestResourceLimits(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	t.Run("MemoryLimit_128MB", func(t *testing.T) {
		sessionID := fmt.Sprintf("test-mem-%d", time.Now().UnixNano())
		projectID := "test-project"

		memoryLimit := int64(128 * 1024 * 1024) // 128MB
		cfg := sandbox.ContainerConfig{
			SessionID:   sessionID,
			ProjectID:   projectID,
			Image:       testImage,
			MemoryLimit: memoryLimit,
			CPULimit:    1,
			NetworkName: testNetworkName,
		}
		c := h.NewContainerWithConfig(cfg)

		if err := c.Start(ctx); err != nil {
			t.Fatalf("Failed to start container: %v", err)
		}
		h.TrackContainer(c.ID)

		// Verify memory limit via docker inspect
		inspect, err := h.dockerClient.ContainerInspect(ctx, c.ID)
		if err != nil {
			t.Fatalf("Failed to inspect container: %v", err)
		}

		actualMemory := inspect.HostConfig.Memory
		if actualMemory != memoryLimit {
			t.Errorf("Expected memory limit %d, got %d", memoryLimit, actualMemory)
		}
		t.Logf("Memory limit verified: %d bytes (%d MB)", actualMemory, actualMemory/(1024*1024))
	})

	t.Run("CPULimit", func(t *testing.T) {
		sessionID := fmt.Sprintf("test-cpu-%d", time.Now().UnixNano())
		projectID := "test-project"

		cpuLimit := float64(2) // 2 CPUs
		cfg := sandbox.ContainerConfig{
			SessionID:   sessionID,
			ProjectID:   projectID,
			Image:       testImage,
			MemoryLimit: 64 * 1024 * 1024,
			CPULimit:    cpuLimit,
			NetworkName: testNetworkName,
		}
		c := h.NewContainerWithConfig(cfg)

		if err := c.Start(ctx); err != nil {
			t.Fatalf("Failed to start container: %v", err)
		}
		h.TrackContainer(c.ID)

		// Verify CPU limit via docker inspect
		inspect, err := h.dockerClient.ContainerInspect(ctx, c.ID)
		if err != nil {
			t.Fatalf("Failed to inspect container: %v", err)
		}

		expectedNanoCPUs := int64(cpuLimit * 1e9)
		actualNanoCPUs := inspect.HostConfig.NanoCPUs
		if actualNanoCPUs != expectedNanoCPUs {
			t.Errorf("Expected NanoCPUs %d, got %d", expectedNanoCPUs, actualNanoCPUs)
		}
		t.Logf("CPU limit verified: %d NanoCPUs (%.1f CPUs)", actualNanoCPUs, float64(actualNanoCPUs)/1e9)
	})

	t.Run("EnvironmentVariables", func(t *testing.T) {
		sessionID := fmt.Sprintf("test-env-%d", time.Now().UnixNano())
		projectID := "test-project"

		envVars := []string{"MY_VAR=test_value", "ANOTHER_VAR=123"}
		cfg := sandbox.ContainerConfig{
			SessionID:   sessionID,
			ProjectID:   projectID,
			Image:       testImage,
			EnvVars:     envVars,
			MemoryLimit: 64 * 1024 * 1024,
			CPULimit:    1,
			NetworkName: testNetworkName,
		}
		c := h.NewContainerWithConfig(cfg)

		if err := c.Start(ctx); err != nil {
			t.Fatalf("Failed to start container: %v", err)
		}
		h.TrackContainer(c.ID)

		// Verify env via exec
		result, err := c.Exec(ctx, []string{"sh", "-c", "echo $MY_VAR"}, nil, "")
		if err != nil {
			t.Fatalf("Failed to exec: %v", err)
		}
		if !strings.Contains(result.Stdout, "test_value") {
			t.Errorf("Expected MY_VAR=test_value, got: %s", result.Stdout)
		}
		t.Logf("Environment variable verified: MY_VAR=%s", strings.TrimSpace(result.Stdout))

		// Verify via docker inspect
		inspect, err := h.dockerClient.ContainerInspect(ctx, c.ID)
		if err != nil {
			t.Fatalf("Failed to inspect: %v", err)
		}

		envMap := make(map[string]bool)
		for _, e := range inspect.Config.Env {
			envMap[e] = true
		}
		for _, expected := range envVars {
			if !envMap[expected] {
				t.Errorf("Expected env var %s not found", expected)
			}
		}
	})
}

func TestVolumeMounting(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sessionID := fmt.Sprintf("test-volume-%d", time.Now().UnixNano())
	projectID := "test-project"

	t.Run("HostPathMounting", func(t *testing.T) {
		c := h.NewContainer(sessionID, projectID)

		// Create file on host before starting container
		hostDir := filepath.Join(h.hostRoot, projectID)
		if err := os.MkdirAll(hostDir, 0755); err != nil {
			t.Fatalf("Failed to create host directory: %v", err)
		}

		preExistingFile := "pre-existing.txt"
		preExistingContent := "I was here before the container started"
		err := os.WriteFile(filepath.Join(hostDir, preExistingFile), []byte(preExistingContent), 0644)
		if err != nil {
			t.Fatalf("Failed to create pre-existing file: %v", err)
		}

		// Start container
		if err := c.Start(ctx); err != nil {
			t.Fatalf("Failed to start container: %v", err)
		}
		h.TrackContainer(c.ID)

		// Verify pre-existing file is visible inside container
		t.Log("Verifying pre-existing file is visible in container...")
		result, err := c.Exec(ctx, []string{"cat", preExistingFile}, nil, "")
		if err != nil {
			t.Fatalf("Failed to exec cat: %v", err)
		}
		if !strings.Contains(result.Stdout, "I was here before") {
			t.Errorf("Pre-existing file content not found in container.\nGot: %s", result.Stdout)
		}
		t.Log("Pre-existing file verified in container")

		// Create file inside container via exec
		t.Log("Creating file inside container via exec...")
		containerCreatedFile := "created-in-container.txt"
		containerContent := "Created inside container"
		result, err = c.Exec(ctx, []string{"sh", "-c", fmt.Sprintf("echo '%s' > %s", containerContent, containerCreatedFile)}, nil, "")
		if err != nil {
			t.Fatalf("Failed to create file in container: %v", err)
		}

		// Verify file appears on host
		hostFilePath := filepath.Join(hostDir, containerCreatedFile)
		hostContent, err := os.ReadFile(hostFilePath)
		if err != nil {
			t.Fatalf("Failed to read container-created file from host: %v", err)
		}
		if !strings.Contains(string(hostContent), containerContent) {
			t.Errorf("Expected content '%s', got '%s'", containerContent, string(hostContent))
		}
		t.Logf("Container-created file verified on host: %s", hostFilePath)
	})

	t.Run("BindMountVerification", func(t *testing.T) {
		sessionID2 := fmt.Sprintf("test-bind-%d", time.Now().UnixNano())
		c := h.NewContainer(sessionID2, projectID)

		if err := c.Start(ctx); err != nil {
			t.Fatalf("Failed to start container: %v", err)
		}
		h.TrackContainer(c.ID)

		// Verify bind mount via docker inspect
		inspect, err := h.dockerClient.ContainerInspect(ctx, c.ID)
		if err != nil {
			t.Fatalf("Failed to inspect container: %v", err)
		}

		foundBind := false
		expectedHostPath := filepath.Join(h.hostRoot, projectID)
		for _, mount := range inspect.Mounts {
			t.Logf("Mount: Source=%s, Destination=%s, Type=%s", mount.Source, mount.Destination, mount.Type)
			if mount.Source == expectedHostPath {
				foundBind = true
				break
			}
		}
		if !foundBind {
			t.Errorf("Expected bind mount from %s not found", expectedHostPath)
		}
	})
}

func TestExecCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sessionID := fmt.Sprintf("test-exec-%d", time.Now().UnixNano())
	projectID := "test-project"
	c := h.NewContainer(sessionID, projectID)

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	h.TrackContainer(c.ID)

	t.Run("SimpleCommand", func(t *testing.T) {
		result, err := c.Exec(ctx, []string{"echo", "hello world"}, nil, "")
		if err != nil {
			t.Fatalf("Failed to exec: %v", err)
		}
		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got %d", result.ExitCode)
		}
		if !strings.Contains(result.Stdout, "hello world") {
			t.Errorf("Expected 'hello world' in stdout, got: %s", result.Stdout)
		}
		t.Logf("Exec result: exit=%d, stdout=%q", result.ExitCode, strings.TrimSpace(result.Stdout))
	})

	t.Run("CommandWithExitCode", func(t *testing.T) {
		result, err := c.Exec(ctx, []string{"sh", "-c", "exit 42"}, nil, "")
		if err != nil {
			t.Fatalf("Failed to exec: %v", err)
		}
		if result.ExitCode != 42 {
			t.Errorf("Expected exit code 42, got %d", result.ExitCode)
		}
		t.Logf("Exit code verified: %d", result.ExitCode)
	})

	t.Run("CommandWithEnv", func(t *testing.T) {
		customEnv := []string{"CUSTOM_VAR=custom_value"}
		result, err := c.Exec(ctx, []string{"sh", "-c", "echo $CUSTOM_VAR"}, customEnv, "")
		if err != nil {
			t.Fatalf("Failed to exec with env: %v", err)
		}
		if !strings.Contains(result.Stdout, "custom_value") {
			t.Errorf("Expected 'custom_value' in stdout, got: %s", result.Stdout)
		}
	})

	t.Run("CommandWithWorkDir", func(t *testing.T) {
		result, err := c.Exec(ctx, []string{"pwd"}, nil, "/tmp")
		if err != nil {
			t.Fatalf("Failed to exec with workdir: %v", err)
		}
		if !strings.Contains(result.Stdout, "/tmp") {
			t.Errorf("Expected '/tmp' in stdout, got: %s", result.Stdout)
		}
	})

	t.Run("LongRunningCommand", func(t *testing.T) {
		start := time.Now()
		result, err := c.Exec(ctx, []string{"sleep", "1"}, nil, "")
		if err != nil {
			t.Fatalf("Failed to exec sleep: %v", err)
		}
		elapsed := time.Since(start)
		if elapsed < 1*time.Second {
			t.Errorf("Sleep command should take at least 1 second, took %v", elapsed)
		}
		if result.ExitCode != 0 {
			t.Errorf("Sleep should exit with 0, got %d", result.ExitCode)
		}
		t.Logf("Long running command completed in %v", elapsed)
	})
}

func TestContainerStateTransitions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sessionID := fmt.Sprintf("test-state-%d", time.Now().UnixNano())
	projectID := "test-project"
	c := h.NewContainer(sessionID, projectID)

	t.Run("IsRunning_BeforeStart", func(t *testing.T) {
		// Before start, IsRunning should return false
		if c.IsRunning(ctx) {
			t.Error("Container should not be running before Start()")
		}
	})

	t.Run("IsRunning_AfterStart", func(t *testing.T) {
		if err := c.Start(ctx); err != nil {
			t.Fatalf("Failed to start: %v", err)
		}
		h.TrackContainer(c.ID)

		if !c.IsRunning(ctx) {
			t.Error("Container should be running after Start()")
		}
	})

	t.Run("IsRunning_AfterStop", func(t *testing.T) {
		if err := c.Stop(ctx, 5); err != nil {
			t.Fatalf("Failed to stop: %v", err)
		}

		if c.IsRunning(ctx) {
			t.Error("Container should not be running after Stop()")
		}
	})
}

func TestGetLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sessionID := fmt.Sprintf("test-logs-%d", time.Now().UnixNano())
	projectID := "test-project"
	c := h.NewContainer(sessionID, projectID)

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	h.TrackContainer(c.ID)
	c.Exec(ctx, []string{"sh", "-c", "echo 'log line 1' > /proc/1/fd/1"}, nil, "")
	c.Exec(ctx, []string{"sh", "-c", "echo 'log line 2' > /proc/1/fd/1"}, nil, "")

	t.Run("GetAllLogs", func(t *testing.T) {
		logs, err := c.GetLogs(ctx, 0)
		if err != nil {
			t.Fatalf("Failed to get logs: %v", err)
		}
		t.Logf("Container logs (stdout): %s", logs.Stdout)
	})

	t.Run("GetTailLogs", func(t *testing.T) {
		logs, err := c.GetLogs(ctx, 10)
		if err != nil {
			t.Fatalf("Failed to get tail logs: %v", err)
		}
		t.Logf("Tail logs: stdout=%q, stderr=%q", logs.Stdout, logs.Stderr)
	})
}

func TestErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	t.Run("RemoveNonExistentContainer", func(t *testing.T) {
		c := h.NewContainer("non-existent-session", "test-project")
		c.ID = "non-existent-container-id"

		err := c.Remove(ctx)
		if err == nil {
			t.Error("Expected error when removing non-existent container")
		}
		t.Logf("Remove non-existent container error: %v", err)
	})

	t.Run("StopNonExistentContainer", func(t *testing.T) {
		c := h.NewContainer("non-existent-session-2", "test-project")
		c.ID = "non-existent-container-id-2"

		err := c.Stop(ctx, 5)
		if err == nil {
			t.Error("Expected error when stopping non-existent container")
		}
		t.Logf("Stop non-existent container error: %v", err)
	})

	t.Run("ExecOnStoppedContainer", func(t *testing.T) {
		sessionID := fmt.Sprintf("test-exec-stopped-%d", time.Now().UnixNano())
		c := h.NewContainer(sessionID, "test-project")

		if err := c.Start(ctx); err != nil {
			t.Fatalf("Failed to start: %v", err)
		}
		h.TrackContainer(c.ID)

		// Stop container
		if err := c.Stop(ctx, 5); err != nil {
			t.Fatalf("Failed to stop: %v", err)
		}

		// Try to exec on stopped container
		_, err := c.Exec(ctx, []string{"echo", "test"}, nil, "")
		if err == nil {
			t.Error("Expected error when exec on stopped container")
		}
		t.Logf("Exec on stopped container error: %v", err)
	})
}

func TestExecLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	h := NewTestHarness(t)
	defer h.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Create a temp directory for logs
	tmpDir, err := os.MkdirTemp("", "docker-logs-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cwd, _ := os.Getwd()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := sandbox.ContainerConfig{
		ProjectID:       "test-proj-logs",
		SessionID:       "test-session-logs",
		Image:           testImage,
		UseAnonymousVol: true,
		NetworkName:     testNetworkName,
		MemoryLimit:     128 * 1024 * 1024,
		CPULimit:        0.5,
		LogDir:          tmpDir,
	}

	c := sandbox.NewContainer(h.dockerClient, cfg, cwd, logger)

	// Clean up any existing container
	if err := c.Remove(ctx); err == nil {
		t.Log("Removed existing container")
	}

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	h.TrackContainer(c.ID)

	// Run a few commands
	cmds := []struct {
		cmd    []string
		output string
	}{
		{[]string{"echo", "hello"}, "hello\n"},
		{[]string{"echo", "world"}, "world\n"},
	}

	for _, tc := range cmds {
		res, err := c.Exec(ctx, tc.cmd, nil, "")
		if err != nil {
			t.Fatalf("Exec failed: %v", err)
		}
		if res.ExitCode != 0 {
			t.Fatalf("Exec exit code %d", res.ExitCode)
		}
	}

	// Verify logs
	logs, err := c.GetExecLogs(ctx)
	if err != nil {
		t.Fatalf("Failed to get exec logs: %v", err)
	}

	if len(logs) != len(cmds) {
		t.Fatalf("Expected %d log entries, got %d", len(cmds), len(logs))
	}

	for i, entry := range logs {
		expected := cmds[i].output
		if entry.Output != expected {
			t.Errorf("Log entry %d: expected output %q, got %q", i, expected, entry.Output)
		}
		if entry.Command[0] != cmds[i].cmd[0] {
			t.Errorf("Log entry %d: expected command %v, got %v", i, cmds[i].cmd, entry.Command)
		}
		if entry.ID == "" {
			t.Errorf("Log entry %d: ID is empty", i)
		}
		if entry.DurationMs < 0 {
			t.Errorf("Log entry %d: invalid duration", i)
		}
	}
}
