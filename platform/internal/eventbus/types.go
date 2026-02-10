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
	EventAgentUnknown    EventType = "agent.unknown"
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
