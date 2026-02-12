from typing import List, Dict, Any

class Memory:
  def __init__(self):
    self.history: List[Dict[str, Any]] = []

  def add_message(self, role: str, content: str, tool_calls: list = None, tool_call_id: str = None):
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