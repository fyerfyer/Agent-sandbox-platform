from dataclasses import dataclass
from typing import AsyncGenerator
from openai import AsyncOpenAI
from src.config import settings


@dataclass
class ToolCallFunction:
  name: str
  arguments: str


@dataclass
class ToolCall:
  # 由 stream_complete() 返回的工具调用对象。

  # 与 openai.types.chat.ChatCompletionMessageToolCall 的结构一致，
  # 以便调用方可以使用相同的属性访问方式
  id: str
  function: ToolCallFunction
  type: str = "function"

  def model_dump(self):
    return {
      "id": self.id,
      "type": self.type,
      "function": {
        "name": self.function.name,
        "arguments": self.function.arguments,
      },
    }


class LLMClient:
  # TODO：引入OpenAI等其他LLM客户端的适配器模式
  def __init__(self):
    self.client = AsyncOpenAI(
      api_key=settings.DEEPSEEK_API_KEY,
      base_url=settings.DEEPSEEK_BASE_URL
    )
    self.model = settings.MODEL_NAME

  async def complete(self, messages: list, tools: list = None):
    completion = await self.client.chat.completions.create(
      model=self.model,
      messages=messages,
      tools=tools
    )
    return completion.choices[0].message

  async def stream_complete(
    self,
    messages: list,
    tools: list = None,
  ) -> AsyncGenerator[dict, None]:
    # 流式完成
    # 返回 Tool Call 或者 Chat 结果
    stream = await self.client.chat.completions.create(
      model=self.model,
      messages=messages,
      tools=tools,
      stream=True,
    )

    content_parts: list[str] = []
    tool_calls_map: dict[int, dict] = {}

    async for chunk in stream:
      if not chunk.choices:
        continue

      choice = chunk.choices[0]
      delta = choice.delta

      # text
      if delta and delta.content:
        content_parts.append(delta.content)
        yield {"type": "content_delta", "delta": delta.content}

      # tool-call
      if delta and delta.tool_calls:
        for tc_delta in delta.tool_calls:
          idx = tc_delta.index
          if idx not in tool_calls_map:
            tool_calls_map[idx] = {"id": "", "name": "", "arguments": ""}
          if tc_delta.id:
            tool_calls_map[idx]["id"] = tc_delta.id
          if tc_delta.function:
            if tc_delta.function.name:
              tool_calls_map[idx]["name"] += tc_delta.function.name
            if tc_delta.function.arguments:
              tool_calls_map[idx]["arguments"] += tc_delta.function.arguments

      # Finished
      if choice.finish_reason is not None:
        content = "".join(content_parts) or None
        tool_calls = None
        if tool_calls_map:
          tool_calls = [
            ToolCall(
              id=tc["id"],
              function=ToolCallFunction(
                name=tc["name"],
                arguments=tc["arguments"],
              ),
            )
            for _, tc in sorted(tool_calls_map.items())
          ]
        yield {"type": "done", "content": content, "tool_calls": tool_calls}