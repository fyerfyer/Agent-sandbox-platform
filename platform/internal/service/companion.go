package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
)

// CompanionService Agent 创建时附加的伴随服务容器
type CompanionService struct {
	ID          string    `json:"service_id"`
	Name        string    `json:"name"`
	Image       string    `json:"image"`
	ContainerID string    `json:"container_id"`
	IP          string    `json:"ip"`
	SessionID   string    `json:"session_id"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// CompanionManager 追踪和管理每个会话的伴随服务容器
type CompanionManager struct {
	mu       sync.RWMutex
	services map[string][]*CompanionService // sessionID -> services
	docker   *client.Client
	logger   *slog.Logger
	network  string // Docker network name for agent containers
}

func NewCompanionManager(docker *client.Client, networkName string, logger *slog.Logger) *CompanionManager {
	return &CompanionManager{
		services: make(map[string][]*CompanionService),
		docker:   docker,
		logger:   logger,
		network:  networkName,
	}
}

func (m *CompanionManager) CreateService(ctx context.Context, sessionID string, req CreateServiceRequest) (*CompanionService, error) {
	serviceID := uuid.New().String()[:8]
	containerName := fmt.Sprintf("svc-%s-%s-%s", sessionID[:8], req.Name, serviceID)

	m.logger.Info("Creating companion service",
		"session_id", sessionID,
		"name", req.Name,
		"image", req.Image,
		"container_name", containerName,
	)

	config := &container.Config{
		Image: req.Image,
		Env:   req.EnvVars,
		Labels: map[string]string{
			"managed_by":   "agent-platform",
			"service_type": "companion",
			"session_id":   sessionID,
			"service_name": req.Name,
			"service_id":   serviceID,
		},
	}
	if len(req.Cmd) > 0 {
		config.Cmd = req.Cmd
	}

	hostConfig := &container.HostConfig{
		Resources: container.Resources{
			Memory:   512 * 1024 * 1024, // 512MB
			NanoCPUs: int64(0.5 * 1e9),  // 0.5 CPU
		},
		AutoRemove: false,
	}

	netConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			m.network: {},
		},
	}

	resp, err := m.docker.ContainerCreate(ctx, config, hostConfig, netConfig, nil, containerName)
	if err != nil {
		return nil, fmt.Errorf("failed to create companion container: %w", err)
	}

	if err := m.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = m.docker.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
		return nil, fmt.Errorf("failed to start companion container: %w", err)
	}

	inspect, err := m.docker.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect companion container: %w", err)
	}

	ip := ""
	if netInfo, ok := inspect.NetworkSettings.Networks[m.network]; ok {
		ip = netInfo.IPAddress
	}

	svc := &CompanionService{
		ID:          serviceID,
		Name:        req.Name,
		Image:       req.Image,
		ContainerID: resp.ID,
		IP:          ip,
		SessionID:   sessionID,
		Status:      "running",
		CreatedAt:   time.Now(),
	}

	m.mu.Lock()
	m.services[sessionID] = append(m.services[sessionID], svc)
	m.mu.Unlock()

	m.logger.Info("Companion service created",
		"service_id", serviceID,
		"name", req.Name,
		"container_id", resp.ID,
		"ip", ip,
	)

	return svc, nil
}

func (m *CompanionManager) RemoveService(ctx context.Context, sessionID, serviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	services, ok := m.services[sessionID]
	if !ok {
		return fmt.Errorf("no services found for session %s", sessionID)
	}

	for i, svc := range services {
		if svc.ID == serviceID {
			timeout := 5
			_ = m.docker.ContainerStop(ctx, svc.ContainerID, container.StopOptions{Timeout: &timeout})
			_ = m.docker.ContainerRemove(ctx, svc.ContainerID, container.RemoveOptions{Force: true})

			m.services[sessionID] = append(services[:i], services[i+1:]...)

			m.logger.Info("Companion service removed",
				"service_id", serviceID,
				"name", svc.Name,
				"session_id", sessionID,
			)
			return nil
		}
	}

	return fmt.Errorf("service %s not found in session %s", serviceID, sessionID)
}

func (m *CompanionManager) CleanupSession(ctx context.Context, sessionID string) {
	m.mu.Lock()
	services, ok := m.services[sessionID]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.services, sessionID)
	m.mu.Unlock()

	for _, svc := range services {
		timeout := 5
		stopErr := m.docker.ContainerStop(ctx, svc.ContainerID, container.StopOptions{Timeout: &timeout})
		if stopErr != nil {
			m.logger.Warn("Failed to stop companion container", "container_id", svc.ContainerID, "error", stopErr)
		}
		rmErr := m.docker.ContainerRemove(ctx, svc.ContainerID, container.RemoveOptions{Force: true})
		if rmErr != nil {
			m.logger.Warn("Failed to remove companion container", "container_id", svc.ContainerID, "error", rmErr)
		}
	}

	m.logger.Info("Cleaned up all companion services for session", "session_id", sessionID, "count", len(services))
}

func (m *CompanionManager) ListServices(sessionID string) []*CompanionService {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.services[sessionID]
}

type CreateServiceRequest struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	EnvVars []string `json:"env_vars"`
	Cmd     []string `json:"cmd"`
}
