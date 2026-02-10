package dispatcher

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"platform/internal/agentproto"
	"platform/internal/eventbus"
	"platform/internal/sandbox"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type Dispatcher struct {
	mu          sync.RWMutex
	connections map[string]*grpc.ClientConn
	bus         eventbus.EventBus
	logger      *slog.Logger
}

func NewDispatcher(bus eventbus.EventBus, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{
		mu:          sync.RWMutex{},
		connections: make(map[string]*grpc.ClientConn),
		bus:         bus,
		logger:      logger,
	}
}

func (d *Dispatcher) GetClient(ctx context.Context, container *sandbox.Container) (agentproto.AgentServiceClient, error) {
	d.mu.RLock()
	conn, ok := d.connections[container.Config.SessionID]
	d.mu.RUnlock()

	if ok && conn.GetState() != connectivity.Shutdown {
		return agentproto.NewAgentServiceClient(conn), nil
	}

	d.logger.Info("Dialing new agent", "ip", container.IP, "session_id", container.Config.SessionID)
	target := fmt.Sprintf("%s:50051", container.IP)
	kacp := keepalive.ClientParameters{
		Time:                10 * time.Second,
		Timeout:             3 * time.Second,
		PermitWithoutStream: true,
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(kacp),
	}

	newConn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial agent: %w", err)
	}

	d.mu.Lock()
	d.connections[container.Config.SessionID] = newConn
	d.mu.Unlock()

	return agentproto.NewAgentServiceClient(newConn), nil
}

func (d *Dispatcher) Dispatch(ctx context.Context, container *sandbox.Container, input string) error {
	client, err := d.GetClient(ctx, container)
	if err != nil {
		return err
	}

	req := &agentproto.RunRequest{
		SessionId: container.Config.SessionID,
		InputText: input,
	}

	stream, err := client.RunStep(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to start run step: %w", err)
	}

	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				d.logger.Info("Stream finished", "session_id", container.Config.SessionID)
				return
			}

			if err != nil {
				d.logger.Error("Stream error", "error", err, "session_id", container.Config.SessionID)
				d.publishError(container.Config.SessionID, err)
				return
			}

			event := eventbus.Event{
				Type:      mapProtoEventType(resp.Type),
				SessionID: container.Config.SessionID,
				Payload:   resp,
				Timestamp: time.Now(),
			}

			if err := d.bus.Publish(ctx, container.Config.SessionID, event); err != nil {
				d.logger.Error("Failed to publish event", "error", err, "session_id", container.Config.SessionID)
			}
		}
	}()

	return nil
}

func (d *Dispatcher) publishError(sessionID string, err error) {
	d.bus.Publish(context.Background(), sessionID, eventbus.Event{
		Type:      eventbus.EventSessionError,
		SessionID: sessionID,
		Payload:   map[string]string{"error": err.Error()},
		Timestamp: time.Now(),
	})
}

func (d *Dispatcher) CleanUp(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if conn, ok := d.connections[sessionID]; ok {
		conn.Close()
		delete(d.connections, sessionID)
	}
}
