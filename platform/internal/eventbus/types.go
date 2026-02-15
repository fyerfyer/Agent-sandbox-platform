package eventbus

import "time"

type EventType string

const (
	// Session Events
	EventSessionReady  EventType = "session.ready"
	EventSessionClosed EventType = "session.closed"
	EventSessionError  EventType = "session.error"

	// Agent Events (映射自 Proto)
	EventAgentThought    EventType = "agent.thought"
	EventAgentToolCall   EventType = "agent.tool_call"
	EventAgentToolResult EventType = "agent.tool_result"
	EventAgentAnswer     EventType = "agent.answer"
	EventAgentError      EventType = "agent.error"
	EventAgentStatus     EventType = "agent.status"
	EventAgentTextChunk  EventType = "agent.text_chunk"
	EventAgentUnknown    EventType = "agent.unknown"

	// EventStreamDone 由调度器在 gRPC 流结束时发布（无论是正常结束还是发生错误）。
	// SSE 处理程序使用该事件来优雅关闭连接。
	EventStreamDone EventType = "stream.done"
)

type Event struct {
	Type      EventType `json:"type"`
	SessionID string    `json:"session_id"`
	Payload   any       `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

func SessionChannelKey(sessionID string) string {
	return "session:" + sessionID + ":events"
}
