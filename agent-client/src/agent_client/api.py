from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, Iterator

import httpx


@dataclass
class SessionHandle:
  id: str
  status: str


class PlatformApiClient:
  def __init__(self, base_url: str):
    self.base_url = base_url.rstrip("/")
    self._client = httpx.Client(timeout=120.0)

  def close(self) -> None:
    self._client.close()

  def health(self) -> dict[str, Any]:
    return self._client.get(f"{self.base_url}/health").json()

  def create_session(
    self,
    project_id: str,
    user_id: str,
    strategy: str,
    image: str,
    env_vars: list[str],
  ) -> SessionHandle:
    resp = self._client.post(
      f"{self.base_url}/api/v1/sessions",
      json={
        "project_id": project_id,
        "user_id": user_id,
        "strategy": strategy,
        "image": image,
        "env_vars": env_vars,
      },
    )
    resp.raise_for_status()
    data = resp.json()
    return SessionHandle(id=data["id"], status=data.get("status", "unknown"))

  def wait_ready(self, session_id: str) -> dict[str, Any]:
    resp = self._client.get(f"{self.base_url}/api/v1/sessions/{session_id}/wait")
    resp.raise_for_status()
    return resp.json()

  def configure(
    self,
    session_id: str,
    system_prompt: str,
    builtin_tools: list[str],
    agent_config: dict[str, str],
  ) -> dict[str, Any]:
    resp = self._client.post(
      f"{self.base_url}/api/v1/sessions/{session_id}/configure",
      json={
        "system_prompt": system_prompt,
        "builtin_tools": builtin_tools,
        "agent_config": agent_config,
      },
    )
    resp.raise_for_status()
    return resp.json()

  def send_message(self, session_id: str, message: str) -> None:
    resp = self._client.post(
      f"{self.base_url}/api/v1/sessions/{session_id}/chat",
      json={"message": message},
    )
    resp.raise_for_status()

  def list_files(self, session_id: str) -> dict[str, Any]:
    resp = self._client.get(f"{self.base_url}/api/v1/sessions/{session_id}/files")
    resp.raise_for_status()
    return resp.json()

  def read_file(self, session_id: str, path: str) -> dict[str, Any]:
    resp = self._client.get(
      f"{self.base_url}/api/v1/sessions/{session_id}/files/read",
      params={"path": path},
    )
    resp.raise_for_status()
    return resp.json()

  def sync_files(self, session_id: str) -> dict[str, Any]:
    resp = self._client.post(f"{self.base_url}/api/v1/sessions/{session_id}/sync", json={})
    resp.raise_for_status()
    return resp.json()

  def session_status(self, session_id: str) -> dict[str, Any]:
    resp = self._client.get(f"{self.base_url}/api/v1/sessions/{session_id}")
    resp.raise_for_status()
    return resp.json()

  def terminate_session(self, session_id: str) -> None:
    resp = self._client.delete(f"{self.base_url}/api/v1/sessions/{session_id}")
    resp.raise_for_status()

  def stream_events(self, session_id: str) -> Iterator[dict[str, Any]]:
    with self._client.stream(
      "GET",
      f"{self.base_url}/api/v1/sessions/{session_id}/stream",
      headers={"Accept": "text/event-stream"},
      timeout=None,
    ) as resp:
      resp.raise_for_status()
      for line in resp.iter_lines():
        if not line:
          continue
        if isinstance(line, bytes):
          line = line.decode("utf-8", errors="replace")
        if line.startswith("data:"):
          raw = line[len("data:"):].strip()
          if not raw:
            continue
          try:
            yield json.loads(raw)
          except json.JSONDecodeError:
            continue
