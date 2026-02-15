from typing import List, Dict, Any, Optional
import logging

logger = logging.getLogger(__name__)

class Memory:
  def __init__(self):
    self.history: List[Dict[str, Any]] = []

  def add_message(self, role: str, content: Optional[str], tool_calls: list = None, tool_call_id: str = None):
    message = {"role": role, "content": content}
    if tool_calls:
      # 序列化 tool_calls 列表，不然无法渲染出消息
      message["tool_calls"] = [
        tc.model_dump() if hasattr(tc, "model_dump") else tc
        for tc in tool_calls
      ]
    if tool_call_id:
      message["tool_call_id"] = tool_call_id
    self.history.append(message)

  def get_history(self) -> List[Dict[str, Any]]:
    return self.history

  def clear(self):
    self.history = []

  def repair_history(self):
    # 在工具调用中断时修复对话历史。
    # 当 gRPC 流被中断时，代理的 step() 生成器可能会在
    # 添加包含 tool_calls 的 assistant 消息与添加所有相应的工具结果消息之间
    # 被关闭。下一次 LLM 调用会因此失败，出现：
    # "An assistant message with 'tool_calls' must be followed by tool
    #  messages responding to each 'tool_call_id'."

    # 扫描历史记录，并为每个缺少工具响应的 tool_call 插入合成的错误结果消息。
    repaired: List[Dict[str, Any]] = []
    i = 0
    patched = False
    while i < len(self.history):
      msg = self.history[i]
      repaired.append(msg)
      i += 1

      if msg.get("role") == "assistant" and msg.get("tool_calls"):
        expected_ids = []
        for tc in msg["tool_calls"]:
          tc_id = tc.get("id", "")
          if tc_id:
            expected_ids.append(tc_id)

        # 收集相关消息
        found_ids: set = set()
        while i < len(self.history) and self.history[i].get("role") == "tool":
          repaired.append(self.history[i])
          tc_id = self.history[i].get("tool_call_id", "")
          if tc_id:
            found_ids.add(tc_id)
          i += 1

        for tc_id in expected_ids:
          if tc_id not in found_ids:
            repaired.append({
              "role": "tool",
              "content": "[ERROR] Tool execution was interrupted (connection lost).",
              "tool_call_id": tc_id,
            })
            patched = True

    if patched:
      self.history = repaired
      logger.warning("Repaired conversation history — added missing tool result messages")