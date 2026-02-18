"""
Session History — 本地持久化会话记录。

在项目目录下存储 .agent-platform/sessions.json，
记录每次 session 的 ID、时间、状态等信息，
支持 CLI 展示历史、恢复会话、切换会话。

Session 状态流转：
    active   -> stopped  (用户执行 /stop，容器保留)
    active   -> ended    (用户执行 /quit，容器销毁)
    stopped  -> active   (用户执行 /resume，重新连接)
    stopped  -> ended    (用户执行 /quit on stopped session)
"""

from __future__ import annotations

import json
import logging
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Optional, List

logger = logging.getLogger(__name__)

HISTORY_DIR = ".agent-platform"
HISTORY_FILE = "sessions.json"
CHAT_DIR = "chats"


@dataclass
class ChatMessage:
  role: str            # "user" or "assistant"
  content: str
  timestamp: float = field(default_factory=time.time)
  msg_type: str = "message"  # "message", "answer", "thought", "tool_call"


class ChatLog:
  """
  Per-session 聊天记录，存储在 .agent-platform/chats/{session_id}.jsonl。
  每行一条 JSON 消息，避免单个大文件膨胀。
  """

  def __init__(self, project_dir: Optional[str] = None):
    root = Path(project_dir) if project_dir else Path.cwd()
    self._dir = root / HISTORY_DIR / CHAT_DIR

  def _path(self, session_id: str) -> Path:
    return self._dir / f"{session_id}.jsonl"

  def append(self, session_id: str, role: str, content: str, msg_type: str = "message") -> None:
    try:
      self._dir.mkdir(parents=True, exist_ok=True)
      entry = {
        "role": role,
        "content": content,
        "type": msg_type,
        "ts": time.time(),
      }
      with open(self._path(session_id), "a", encoding="utf-8") as f:
        f.write(json.dumps(entry, ensure_ascii=False) + "\n")
    except Exception as e:
      logger.warning("Failed to save chat message: %s", e)

  def load(self, session_id: str) -> List[ChatMessage]:
    path = self._path(session_id)
    if not path.is_file():
      return []
    messages: List[ChatMessage] = []
    try:
      with open(path, "r", encoding="utf-8") as f:
        for line in f:
          line = line.strip()
          if not line:
            continue
          try:
            data = json.loads(line)
            messages.append(ChatMessage(
              role=data.get("role", "unknown"),
              content=data.get("content", ""),
              timestamp=data.get("ts", 0),
              msg_type=data.get("type", "message"),
            ))
          except json.JSONDecodeError:
            continue
    except Exception as e:
      logger.warning("Failed to load chat log for %s: %s", session_id, e)
    return messages

  def has_messages(self, session_id: str) -> bool:
    path = self._path(session_id)
    return path.is_file() and path.stat().st_size > 0

  def remove(self, session_id: str) -> None:
    try:
      path = self._path(session_id)
      if path.is_file():
        path.unlink()
    except Exception:
      pass


@dataclass
class SessionRecord:
  session_id: str
  project_id: str
  strategy: str = "Cold-Strategy"
  agent_type: str = "default"
  created_at: float = field(default_factory=time.time)
  ended_at: Optional[float] = None
  status: str = "active"           # active | stopped | ended
  summary: str = ""                # 最后一条消息的摘要
  container_id: str = ""           # Docker 容器 ID（用于 resume 时检测）
  image: str = ""                  # 使用的 runtime 镜像
  message_count: int = 0           # 发送过的消息数量


class SessionHistory:
  """
  管理本地 session 历史。
  历史文件路径：{project_dir}/.agent-platform/sessions.json
  每个项目目录维护独立的历史记录。
  """

  def __init__(self, project_dir: Optional[str] = None, max_records: int = 100):
    root = Path(project_dir) if project_dir else Path.cwd()
    self._dir = root / HISTORY_DIR
    self._file = self._dir / HISTORY_FILE
    self._max_records = max_records
    self._records: list[SessionRecord] = []
    self._load()

  def _load(self) -> None:
    if not self._file.is_file():
      self._records = []
      return
    try:
      data = json.loads(self._file.read_text(encoding="utf-8"))
      self._records = [
        SessionRecord(**{
          k: v for k, v in r.items()
          if k in SessionRecord.__dataclass_fields__
        })
        for r in data
      ]
    except Exception as e:
      logger.warning("Failed to load session history: %s", e)
      self._records = []

  def _save(self) -> None:
    try:
      self._dir.mkdir(parents=True, exist_ok=True)
      self._file.write_text(
        json.dumps(
          [asdict(r) for r in self._records],
          indent=2,
          ensure_ascii=False,
        ),
        encoding="utf-8",
      )
    except Exception as e:
      logger.warning("Failed to save session history: %s", e)

  def add(self, record: SessionRecord) -> None:
    self._records.append(record)
    self._trim()
    self._save()

  def _trim(self) -> None:
    if len(self._records) <= self._max_records:
      return
    # 先按 ended → stopped → active 排序，同组内按时间升序；裁剪最旧的
    priority = {"active": 2, "stopped": 1, "ended": 0}
    self._records.sort(key=lambda r: (priority.get(r.status, 0), r.created_at))
    self._records = self._records[-self._max_records :]

  def mark_stopped(self, session_id: str, summary: str = "") -> None:
    for r in self._records:
      if r.session_id == session_id:
        r.status = "stopped"
        if summary:
          r.summary = summary[:200]
        break
    self._save()

  def mark_ended(self, session_id: str, summary: str = "") -> None:
    for r in self._records:
      if r.session_id == session_id:
        r.ended_at = time.time()
        r.status = "ended"
        if summary:
          r.summary = summary[:200]
        break
    self._save()

  def mark_active(self, session_id: str) -> None:
    for r in self._records:
      if r.session_id == session_id:
        r.status = "active"
        r.ended_at = None
        break
    self._save()

  def increment_messages(self, session_id: str, summary: str = "") -> None:
    for r in self._records:
      if r.session_id == session_id:
        r.message_count += 1
        if summary:
          r.summary = summary[:200]
        break
    self._save()

  def update_container_id(self, session_id: str, container_id: str) -> None:
    for r in self._records:
      if r.session_id == session_id:
        r.container_id = container_id
        break
    self._save()

  def recent(self, limit: int = 20) -> list[SessionRecord]:
    return sorted(
      self._records,
      key=lambda r: r.created_at,
      reverse=True,
    )[:limit]

  def find(self, session_id: str) -> Optional[SessionRecord]:
    # 精确匹配
    for r in self._records:
      if r.session_id == session_id:
        return r
    # 前缀匹配
    if len(session_id) >= 6:
      matches = [r for r in self._records if r.session_id.startswith(session_id)]
      if len(matches) == 1:
        return matches[0]
    return None

  def active_sessions(self) -> list[SessionRecord]:
    return [r for r in self._records if r.status == "active"]

  def stopped_sessions(self) -> list[SessionRecord]:
    return [r for r in self._records if r.status == "stopped"]

  def resumable_sessions(self) -> list[SessionRecord]:
    return [r for r in self._records if r.status in ("active", "stopped")]

  def remove_record(self, session_id: str) -> None:
    self._records = [r for r in self._records if r.session_id != session_id]
    self._save()