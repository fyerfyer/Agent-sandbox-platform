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
		Time:                30 * time.Second,
		Timeout:             10 * time.Second,
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

	// 使用后台上下文作为 gRPC 流的上下文，避免在 HTTP 请求处理返回时被取消。
	// 该流必须比短时的 POST /chat 请求存活更久。
	streamCtx := context.Background()

	stream, err := client.RunStep(streamCtx, req)
	if err != nil {
		return fmt.Errorf("failed to start run step: %w", err)
	}

	go func() {
		defer func() {
			// 发布一个 stream-done 事件，以便 SSE 处理程序可以优雅地关闭
			// 而不是在代理完成后在 Redis 订阅上挂起。
			d.bus.Publish(streamCtx, container.Config.SessionID, eventbus.Event{
				Type:      eventbus.EventStreamDone,
				SessionID: container.Config.SessionID,
				Payload:   map[string]string{"text": "stream completed"},
				Timestamp: time.Now(),
			})
		}()
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
				Payload:   buildPayload(resp),
				Timestamp: time.Now(),
			}

			if err := d.bus.Publish(streamCtx, container.Config.SessionID, event); err != nil {
				d.logger.Error("Failed to publish event", "error", err, "session_id", container.Config.SessionID)
			}
		}
	}()

	return nil
}

func (d *Dispatcher) Configure(ctx context.Context, container *sandbox.Container, req *agentproto.ConfigureRequest) (*agentproto.ConfigureResponse, error) {
	client, err := d.GetClient(ctx, container)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent client: %w", err)
	}
	return client.Configure(ctx, req)
}

func (d *Dispatcher) Stop(ctx context.Context, container *sandbox.Container, sessionID string) (*agentproto.StopResponse, error) {
	client, err := d.GetClient(ctx, container)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent client: %w", err)
	}
	return client.Stop(ctx, &agentproto.StopRequest{SessionId: sessionID})
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
