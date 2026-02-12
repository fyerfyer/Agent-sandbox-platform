from openai import AsyncOpenAI
from src.config import settings

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