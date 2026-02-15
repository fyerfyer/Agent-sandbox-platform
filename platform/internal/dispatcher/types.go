package dispatcher

import (
	"encoding/json"
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
	case agentproto.EventType_EVENT_TYPE_STATUS:
		return eventbus.EventAgentStatus
	case agentproto.EventType_EVENT_TYPE_TEXT_CHUNK:
		return eventbus.EventAgentTextChunk
	default:
		return eventbus.EventAgentUnknown
	}
}

// buildPayload 将 protobuf 的 AgentEvent 转换为一个普通的 map，包含 SSE 客户端期望的键（"text"、"tool_name"、"arguments" 等）。
// Protobuf 消息使用自己的字段名（content、source、metadata_json）进行序列化，
// 这些字段与客户端约定不匹配，因此我们在此处进行转换。
func buildPayload(resp *agentproto.AgentEvent) map[string]any {
	payload := map[string]any{
		"text":   resp.Content,
		"source": resp.Source,
	}

	// 对于工具调用事件，尝试从 MetadataJson 中提取工具名称和参数。
	if resp.MetadataJson != "" {
		var meta map[string]any
		if err := json.Unmarshal([]byte(resp.MetadataJson), &meta); err == nil {
			if name, ok := meta["name"].(string); ok {
				payload["tool_name"] = name
			}
			if args, ok := meta["arguments"].(string); ok {
				payload["arguments"] = args
			}
			if id, ok := meta["tool_call_id"].(string); ok {
				payload["tool_call_id"] = id
			}
		}
	}

	return payload
}
