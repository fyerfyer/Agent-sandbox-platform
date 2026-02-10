package dispatcher

import (
	"platform/internal/agentproto"
	"platform/internal/eventbus"
)

func mapProtoEventType(t agentproto.EventType) eventbus.EventType {
	switch t {
	case agentproto.EventType_EVENT_TYPE_ANSWER:
		return eventbus.EventAgentAnswer
	case agentproto.EventType_EVENT_TYPE_ERROR:
		return eventbus.EventAgentError
	case agentproto.EventType_EVENT_TYPE_THOUGHT:
		return eventbus.EventAgentThought
	case agentproto.EventType_EVENT_TYPE_TOOL_CALL:
		return eventbus.EventAgentToolCall
	case agentproto.EventType_EVENT_TYPE_TOOL_RESULT:
		return eventbus.EventAgentToolResult
	default:
		return eventbus.EventAgentUnknown
	}
}
