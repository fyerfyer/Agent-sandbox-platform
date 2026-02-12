import logging
import os
from pathlib import Path
from typing import Any, Dict

logger = logging.getLogger(__name__)

MAX_READ_CHARS = 50_000

def _resolve_safe(relative_path: str) -> str:
  workspace = os.environ.get("WORKSPACE_DIR", "/app/workspace")
  base = Path(workspace).resolve()
  target = (base / relative_path).resolve()
  if not str(target).startswith(str(base)):
    raise ValueError(f"Path escapes workspace: {relative_path}")
  return str(target)


FILE_READ_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "file_read",
    "description": (
      "Read the contents of a file in the workspace. "
      "Returns the file text (truncated if very large)."
    ),
    "parameters": {
      "type": "object",
      "properties": {
        "path": {
          "type": "string",
          "description": "Relative path to the file inside the workspace.",
        },
        "start_line": {
          "type": "integer",
          "description": "Optional 1-based start line (inclusive).",
        },
        "end_line": {
          "type": "integer",
          "description": "Optional 1-based end line (inclusive).",
        },
      },
      "required": ["path"],
    },
  },
}


async def file_read(
  path: str,
  start_line: int | None = None,
  end_line: int | None = None,
  **_kwargs: Any,
) -> str:
  target = _resolve_safe(path)
  logger.info("file_read: %s (lines %s–%s)", target, start_line, end_line)

  try:
    with open(target, "r", encoding="utf-8", errors="replace") as fh:
      if start_line or end_line:
        lines = fh.readlines()
        s = (start_line or 1) - 1
        e = end_line or len(lines)
        content = "".join(lines[s:e])
      else:
        content = fh.read()
  except FileNotFoundError:
    return f"[ERROR] File not found: {path}"
  except Exception as exc:
    return f"[ERROR] {exc}"

  if len(content) > MAX_READ_CHARS:
    content = content[:MAX_READ_CHARS] + f"\n... [truncated, {len(content)} chars total]"
  return content


FILE_WRITE_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "file_write",
    "description": (
      "Create or overwrite a file in the workspace. "
      "Parent directories are created automatically."
    ),
    "parameters": {
      "type": "object",
      "properties": {
        "path": {
          "type": "string",
          "description": "Relative path to the file inside the workspace.",
        },
        "content": {
          "type": "string",
          "description": "The full content to write.",
        },
        "append": {
          "type": "boolean",
          "description": "If true, append instead of overwrite.",
        },
      },
      "required": ["path", "content"],
    },
  },
}


async def file_write(
  path: str,
  content: str,
  append: bool = False,
  **_kwargs: Any,
) -> str:
  """Write *content* to a file."""
  target = _resolve_safe(path)
  logger.info("file_write: %s (append=%s, %d chars)", target, append, len(content))

  try:
    os.makedirs(os.path.dirname(target), exist_ok=True)
    mode = "a" if append else "w"
    with open(target, mode, encoding="utf-8") as fh:
      fh.write(content)
    return f"Successfully wrote {len(content)} chars to {path}"
  except Exception as exc:
    return f"[ERROR] {exc}"


LIST_FILES_TOOL_SCHEMA: Dict[str, Any] = {
  "type": "function",
  "function": {
    "name": "list_files",
    "description": (
      "List files and directories in the workspace. "
      "Returns names with trailing '/' for directories."
    ),
    "parameters": {
      "type": "object",
      "properties": {
        "path": {
          "type": "string",
          "description": "Relative directory path (default: workspace root '.').",
        },
        "recursive": {
          "type": "boolean",
          "description": "If true, list recursively.",
        },
      },
      "required": [],
    },
  },
}


async def list_files(
  path: str = ".",
  recursive: bool = False,
  **_kwargs: Any,
) -> str:
  """List directory contents."""
  target = _resolve_safe(path)
  logger.info("list_files: %s (recursive=%s)", target, recursive)

  try:
    if not os.path.isdir(target):
      return f"[ERROR] Not a directory: {path}"

    entries: list[str] = []

    if recursive:
      for root, dirs, files in os.walk(target):
        rel_root = os.path.relpath(root, target)
        for d in sorted(dirs):
          entries.append(os.path.join(rel_root, d) + "/")
        for f in sorted(files):
          entries.append(os.path.join(rel_root, f))
    else:
      for entry in sorted(os.listdir(target)):
        full = os.path.join(target, entry)
        if os.path.isdir(full):
          entries.append(entry + "/")
        else:
          entries.append(entry)

    return "\n".join(entries) if entries else "(empty directory)"

  except Exception as exc:
    return f"[ERROR] {exc}"