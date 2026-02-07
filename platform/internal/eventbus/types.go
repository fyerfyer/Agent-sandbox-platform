package eventbus

import "time"

type EventType string

const (
	EventSessionReady  EventType = "session.ready"
	EventSessionClosed EventType = "session.closed"
	EventSessionError  EventType = "session.error"
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
