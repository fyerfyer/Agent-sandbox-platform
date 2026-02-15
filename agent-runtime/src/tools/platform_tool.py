import json
import logging
import os
from typing import Any, Dict

import httpx

from src.config import settings

logger = logging.getLogger(__name__)

# TODO：这些是新写的 Go Platform 专用工具，只是封装了Go Platform相关方法，待测试和完善
CREATE_SERVICE_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "create_service",
    "description": (
      "Create a companion Docker service container (e.g., PostgreSQL, Redis, MySQL) "
      "in the same network as this sandbox. The service will be accessible via its IP. "
      "Returns the service ID, IP address, and connection details. "
      "Use this when your task requires external services like databases or message queues."
    ),
    "parameters": {
      "type": "object",
      "properties": {
        "name": {
          "type": "string",
          "description": "A short name for the service (e.g., 'postgres', 'redis', 'mysql').",
        },
        "image": {
          "type": "string",
          "description": "Docker image to use (e.g., 'postgres:15-alpine', 'redis:7-alpine').",
        },
        "env_vars": {
          "type": "array",
          "items": {"type": "string"},
          "description": (
            "Environment variables for the service container, "
            "e.g., ['POSTGRES_USER=myuser', 'POSTGRES_PASSWORD=secret', 'POSTGRES_DB=mydb']."
          ),
        },
        "cmd": {
          "type": "array",
          "items": {"type": "string"},
          "description": "Optional: override command for the container.",
        },
      },
      "required": ["name", "image"],
    },
  },
}


REMOVE_SERVICE_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "remove_service",
    "description": (
      "Remove a previously created companion service container. "
      "Use this to clean up services you no longer need."
    ),
    "parameters": {
      "type": "object",
      "properties": {
        "service_id": {
          "type": "string",
          "description": "The service_id returned by create_service.",
        },
      },
      "required": ["service_id"],
    },
  },
}


EXPORT_FILES_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "export_files",
    "description": (
      "Export files from the container workspace to the user's local host machine. "
      "This copies files from the sandbox to the user's project directory on the host. "
      "Use this tool when the user asks to save, download, export, or copy completed "
      "work out of the container. By default it exports the entire workspace. "
      "You can optionally specify a sub-path to export only specific files or directories."
    ),
    "parameters": {
      "type": "object",
      "properties": {
        "src_path": {
          "type": "string",
          "description": (
            "Optional: path inside the container workspace to export. "
            "Relative to /app/workspace/. Leave empty to export the entire workspace. "
            "Examples: 'src/', 'main.py', 'build/output/'"
          ),
        },
        "dest_path": {
          "type": "string",
          "description": (
            "Optional: destination sub-directory on the host project directory. "
            "Leave empty to export to the project root."
          ),
        },
      },
      "required": [],
    },
  },
}


async def create_service(
  name: str,
  image: str,
  env_vars: list[str] | None = None,
  cmd: list[str] | None = None,
  **_kwargs: Any,
) -> str:
  session_id = settings.SESSION_ID or os.environ.get("SESSION_ID", "")
  platform_url = settings.PLATFORM_API_URL

  if not session_id:
    return "[ERROR] SESSION_ID not set — cannot call Platform API."
  if not platform_url:
    return "[ERROR] PLATFORM_API_URL not set — cannot call Platform API."

  url = f"{platform_url}/api/v1/sessions/{session_id}/services"
  payload = {
    "name": name,
    "image": image,
    "env_vars": env_vars or [],
    "cmd": cmd or [],
  }

  logger.info("create_service: POST %s payload=%s", url, json.dumps(payload)[:200])

  try:
    async with httpx.AsyncClient(timeout=60.0) as client:
      resp = await client.post(url, json=payload)

    if resp.status_code >= 400:
      return f"[ERROR] Platform API returned {resp.status_code}: {resp.text[:500]}"

    data = resp.json()
    result_lines = [
      f"Service created successfully:",
      f"  service_id: {data.get('service_id', 'unknown')}",
      f"  name: {data.get('name', name)}",
      f"  ip: {data.get('ip', 'unknown')}",
      f"  status: {data.get('status', 'unknown')}",
    ]
    return "\n".join(result_lines)

  except httpx.ConnectError:
    return f"[ERROR] Cannot connect to Platform API at {platform_url}. Is the Go Platform running?"
  except Exception as exc:
    logger.error("create_service failed: %s", exc)
    return f"[ERROR] create_service failed: {exc}"


async def remove_service(
  service_id: str,
  **_kwargs: Any,
) -> str:
  # TODO：调用 Go Platform API 删除一个伴随服务容器
  session_id = settings.SESSION_ID or os.environ.get("SESSION_ID", "")
  platform_url = settings.PLATFORM_API_URL

  if not session_id:
    return "[ERROR] SESSION_ID not set — cannot call Platform API."
  if not platform_url:
    return "[ERROR] PLATFORM_API_URL not set — cannot call Platform API."

  url = f"{platform_url}/api/v1/sessions/{session_id}/services/{service_id}"

  logger.info("remove_service: DELETE %s", url)

  try:
    async with httpx.AsyncClient(timeout=30.0) as client:
      resp = await client.delete(url)

    if resp.status_code >= 400:
      return f"[ERROR] Platform API returned {resp.status_code}: {resp.text[:500]}"

    return f"Service {service_id} removed successfully."

  except httpx.ConnectError:
    return f"[ERROR] Cannot connect to Platform API at {platform_url}."
  except Exception as exc:
    logger.error("remove_service failed: %s", exc)
    return f"[ERROR] remove_service failed: {exc}"


async def export_files(
  src_path: str = "",
  dest_path: str = "",
  **_kwargs: Any,
) -> str:
  session_id = settings.SESSION_ID or os.environ.get("SESSION_ID", "")
  platform_url = settings.PLATFORM_API_URL

  if not session_id:
    return "[ERROR] SESSION_ID not set — cannot call Platform API."
  if not platform_url:
    return "[ERROR] PLATFORM_API_URL not set — cannot call Platform API."

  url = f"{platform_url}/api/v1/sessions/{session_id}/sync"
  payload: Dict[str, Any] = {}
  if src_path:
    payload["src_path"] = src_path
  if dest_path:
    payload["dest_path"] = dest_path

  logger.info("export_files: POST %s payload=%s", url, json.dumps(payload)[:200])

  try:
    async with httpx.AsyncClient(timeout=120.0) as client:
      resp = await client.post(url, json=payload)

    if resp.status_code >= 400:
      return f"[ERROR] Platform API returned {resp.status_code}: {resp.text[:500]}"

    data = resp.json()
    msg = data.get("message", "Files exported successfully")
    return (
      f"Files exported to host successfully.\n"
      f"  status: {data.get('status', 'ok')}\n"
      f"  message: {msg}\n"
      f"The files are now available in the user's local project directory."
    )

  except httpx.ConnectError:
    return f"[ERROR] Cannot connect to Platform API at {platform_url}. Is the Go Platform running?"
  except Exception as exc:
    logger.error("export_files failed: %s", exc)
    return f"[ERROR] export_files failed: {exc}"
