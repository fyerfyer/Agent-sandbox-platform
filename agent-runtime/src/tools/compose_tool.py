import json
import logging
import os
from typing import Any, Dict

import httpx

from src.config import settings

logger = logging.getLogger(__name__)

# Tool Schema: create_compose_stack

CREATE_COMPOSE_STACK_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "create_compose_stack",
    "description": (
      "Start a group of infrastructure services from a docker-compose.yml definition. "
      "All services are launched in the same Docker network as this sandbox, "
      "so you can reach them by their container IP. "
      "Provide either 'compose_content' (raw YAML string) or 'compose_file' "
      "(path to an existing docker-compose.yml on the host). "
      "Returns the list of services with their names, IPs, and status. "
      "Use this when you need databases (Postgres, MySQL, Redis), "
      "message brokers (RabbitMQ, Kafka), or any multi-service stack."
    ),
    "parameters": {
      "type": "object",
      "properties": {
        "compose_content": {
          "type": "string",
          "description": (
            "The full docker-compose.yml content as a YAML string. "
            "Example:\n"
            "services:\n"
            "  db:\n"
            "    image: postgres:15-alpine\n"
            "    environment:\n"
            "      POSTGRES_USER: myuser\n"
            "      POSTGRES_PASSWORD: secret\n"
            "      POSTGRES_DB: mydb\n"
            "    networks:\n"
            "      - agent-platform-net\n"
          ),
        },
        "compose_file": {
          "type": "string",
          "description": (
            "Path to an existing docker-compose.yml file on the host machine. "
            "Use this when there is a pre-existing compose file."
          ),
        },
      },
      "required": [],
    },
  },
}

# Tool Schema: teardown_compose_stack

TEARDOWN_COMPOSE_STACK_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "teardown_compose_stack",
    "description": (
      "Stop and remove all services in the current session's docker-compose stack. "
      "This also removes associated volumes. Use this to clean up infrastructure "
      "when you are done with the task."
    ),
    "parameters": {
      "type": "object",
      "properties": {},
      "required": [],
    },
  },
}

# Tool Schema: get_compose_stack

GET_COMPOSE_STACK_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "get_compose_stack",
    "description": (
      "Get the current status and IP addresses of all services in the session's "
      "docker-compose stack. Use this to check if services are running and "
      "get their connection details."
    ),
    "parameters": {
      "type": "object",
      "properties": {},
      "required": [],
    },
  },
}

# Tool Executors

async def create_compose_stack(
  compose_content: str = "",
  compose_file: str = "",
  **_kwargs: Any,
) -> str:
  session_id = settings.SESSION_ID or os.environ.get("SESSION_ID", "")
  platform_url = settings.PLATFORM_API_URL

  if not session_id:
    return "[ERROR] SESSION_ID not set — cannot call Platform API."
  if not platform_url:
    return "[ERROR] PLATFORM_API_URL not set — cannot call Platform API."

  if not compose_content and not compose_file:
    return "[ERROR] Either compose_content or compose_file must be provided."

  url = f"{platform_url}/api/v1/sessions/{session_id}/compose"
  payload: dict[str, Any] = {}
  if compose_content:
    payload["compose_content"] = compose_content
  if compose_file:
    payload["compose_file"] = compose_file

  logger.info("create_compose_stack: POST %s", url)

  try:
    async with httpx.AsyncClient(timeout=120.0) as client:
      resp = await client.post(url, json=payload)

    if resp.status_code >= 400:
      return f"[ERROR] Platform API returned {resp.status_code}: {resp.text[:500]}"

    data = resp.json()
    lines = [
      f"Compose stack created successfully:",
      f"  project: {data.get('project_name', 'unknown')}",
      f"  status: {data.get('status', 'unknown')}",
      f"  services:",
    ]
    for svc in data.get("services", []):
      lines.append(
        f"    - {svc.get('name', '?')}: ip={svc.get('ip', '?')} "
        f"status={svc.get('status', '?')} container={svc.get('container_id', '?')}"
      )
    return "\n".join(lines)

  except httpx.ConnectError:
    return f"[ERROR] Cannot connect to Platform API at {platform_url}."
  except Exception as exc:
    logger.error("create_compose_stack failed: %s", exc)
    return f"[ERROR] create_compose_stack failed: {exc}"


async def teardown_compose_stack(**_kwargs: Any) -> str:
  session_id = settings.SESSION_ID or os.environ.get("SESSION_ID", "")
  platform_url = settings.PLATFORM_API_URL

  if not session_id:
    return "[ERROR] SESSION_ID not set — cannot call Platform API."
  if not platform_url:
    return "[ERROR] PLATFORM_API_URL not set — cannot call Platform API."

  url = f"{platform_url}/api/v1/sessions/{session_id}/compose"

  logger.info("teardown_compose_stack: DELETE %s", url)

  try:
    async with httpx.AsyncClient(timeout=60.0) as client:
      resp = await client.delete(url)

    if resp.status_code >= 400:
      return f"[ERROR] Platform API returned {resp.status_code}: {resp.text[:500]}"

    return "Compose stack torn down successfully."

  except httpx.ConnectError:
    return f"[ERROR] Cannot connect to Platform API at {platform_url}."
  except Exception as exc:
    logger.error("teardown_compose_stack failed: %s", exc)
    return f"[ERROR] teardown_compose_stack failed: {exc}"


async def get_compose_stack(**_kwargs: Any) -> str:
  session_id = settings.SESSION_ID or os.environ.get("SESSION_ID", "")
  platform_url = settings.PLATFORM_API_URL

  if not session_id:
    return "[ERROR] SESSION_ID not set — cannot call Platform API."
  if not platform_url:
    return "[ERROR] PLATFORM_API_URL not set — cannot call Platform API."

  url = f"{platform_url}/api/v1/sessions/{session_id}/compose"

  logger.info("get_compose_stack: GET %s", url)

  try:
    async with httpx.AsyncClient(timeout=30.0) as client:
      resp = await client.get(url)

    if resp.status_code >= 400:
      return f"[ERROR] Platform API returned {resp.status_code}: {resp.text[:500]}"

    data = resp.json()
    lines = [
      f"Compose stack status:",
      f"  project: {data.get('project_name', 'unknown')}",
      f"  status: {data.get('status', 'unknown')}",
      f"  services:",
    ]
    for svc in data.get("services", []):
      lines.append(
        f"    - {svc.get('name', '?')}: ip={svc.get('ip', '?')} "
        f"status={svc.get('status', '?')} container={svc.get('container_id', '?')}"
      )
    return "\n".join(lines)

  except httpx.ConnectError:
    return f"[ERROR] Cannot connect to Platform API at {platform_url}."
  except Exception as exc:
    logger.error("get_compose_stack failed: %s", exc)
    return f"[ERROR] get_compose_stack failed: {exc}"
