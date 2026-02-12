import argparse
import json
import os
import sys
import threading
import time
from urllib.request import Request, urlopen
from urllib.error import URLError, HTTPError

# ── Colours ──────────────────────────────────────────────────────────────────
CYAN = "\033[0;36m"
GREEN = "\033[0;32m"
YELLOW = "\033[1;33m"
RED = "\033[0;31m"
DIM = "\033[2m"
BOLD = "\033[1m"
NC = "\033[0m"


def info(msg): print(f"{CYAN}[INFO]{NC}  {msg}")
def ok(msg): print(f"{GREEN}[OK]{NC}    {msg}")
def warn(msg): print(f"{YELLOW}[WARN]{NC}  {msg}")
def fail(msg): print(f"{RED}[FAIL]{NC}  {msg}"); sys.exit(1)


# ── HTTP helpers ─────────────────────────────────────────────────────────────
def api_post(url: str, data: dict | None = None) -> dict:
  """POST JSON to the API and return the parsed response."""
  body = json.dumps(data).encode() if data else None
  req = Request(url, data=body, method="POST")
  req.add_header("Content-Type", "application/json")
  try:
    with urlopen(req, timeout=120) as resp:
      return json.loads(resp.read())
  except HTTPError as e:
    body = e.read().decode()
    fail(f"API error {e.code} from {url}: {body}")
  except URLError as e:
    fail(f"Cannot reach API at {url}: {e.reason}")


def api_get(url: str) -> dict:
  """GET JSON from the API."""
  req = Request(url)
  try:
    with urlopen(req, timeout=120) as resp:
      return json.loads(resp.read())
  except HTTPError as e:
    body = e.read().decode()
    fail(f"API error {e.code} from {url}: {body}")
  except URLError as e:
    fail(f"Cannot reach API at {url}: {e.reason}")


# ── SSE stream reader ───────────────────────────────────────────────────────
def stream_events(api_base: str, session_id: str, stop_event: threading.Event):
  """
  Connect to the SSE stream and print events until the agent finishes
  (agent.answer received) or the stop_event is set.
  """
  url = f"{api_base}/api/v1/sessions/{session_id}/stream"
  req = Request(url)
  req.add_header("Accept", "text/event-stream")
  answer_received = False

  try:
    with urlopen(req, timeout=300) as resp:
      buffer = ""
      while not stop_event.is_set():
        chunk = resp.read(1)
        if not chunk:
          break
        buffer += chunk.decode("utf-8", errors="replace")

        # SSE events are separated by double newlines
        while "\n\n" in buffer:
          raw_event, buffer = buffer.split("\n\n", 1)
          for line in raw_event.strip().split("\n"):
            if line.startswith("data:"):
              payload_str = line[len("data:"):].strip()
              if not payload_str:
                continue
              try:
                event = json.loads(payload_str)
              except json.JSONDecodeError:
                continue

              evt_type = event.get("type", "")
              payload = event.get("payload", "")

              if evt_type == "agent.thought":
                text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
                print(f"\n  {DIM}💭 [Thought]{NC} {text}")
              elif evt_type == "agent.tool_call":
                if isinstance(payload, dict):
                  tool = payload.get("tool_name", payload.get("toolName", ""))
                  args = payload.get("arguments", payload.get("text", ""))
                else:
                  tool = ""
                  args = str(payload)
                print(f"\n  {YELLOW}🔧 [Tool Call]{NC} {tool}")
                if args:
                  # Truncate very long args
                  args_str = str(args)
                  if len(args_str) > 500:
                    args_str = args_str[:500] + "…"
                  print(f"     {DIM}{args_str}{NC}")
              elif evt_type == "agent.tool_result":
                text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
                if len(text) > 300:
                  text = text[:300] + "…"
                print(f"  {DIM}📋 [Tool Result]{NC} {text}")
              elif evt_type == "agent.answer":
                text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
                print(f"\n  {GREEN}{BOLD}✅ [Answer]{NC}\n")
                print(f"  {text}\n")
                answer_received = True
                stop_event.set()
              elif evt_type == "agent.error" or evt_type == "session.error":
                text = payload.get("text", str(payload)) if isinstance(payload, dict) else str(payload)
                print(f"\n  {RED}❌ [Error]{NC} {text}")
                stop_event.set()
              elif evt_type == "agent.status":
                text = payload.get("text", "") if isinstance(payload, dict) else str(payload)
                print(f"  {DIM}📡 [Status]{NC} {text}")
              # Ignore pings and unknown events

          if answer_received or stop_event.is_set():
            break
  except Exception as e:
    if not stop_event.is_set():
      warn(f"SSE stream error: {e}")


# ── Main ─────────────────────────────────────────────────────────────────────
def main():
  parser = argparse.ArgumentParser(description="Agent Platform – Interactive Client")
  parser.add_argument("--api", default="http://localhost:8080", help="Platform API base URL")
  parser.add_argument("--api-key", default=os.environ.get("DEEPSEEK_API_KEY", ""), help="DeepSeek API key")
  parser.add_argument("--strategy", default="Cold-Strategy", choices=["Cold-Strategy", "Warm-Strategy"],
                      help="Container strategy")
  parser.add_argument("--project", default="interactive", help="Project ID")
  parser.add_argument("--system-prompt", default="", help="Custom system prompt for the agent")
  args = parser.parse_args()

  if not args.api_key:
    fail("DEEPSEEK_API_KEY is not set. Pass --api-key or export DEEPSEEK_API_KEY.")

  api_base = args.api.rstrip("/")

  # ── 1. Health check ──────────────────────────────────────────────────────
  info("Checking platform health …")
  health = api_get(f"{api_base}/health")
  if health.get("status") != "ok":
    fail(f"Platform is not healthy: {health}")
  ok("Platform is healthy.")

  # ── 2. Create session ────────────────────────────────────────────────────
  info(f"Creating session (strategy={args.strategy}, project={args.project}) …")
  env_vars = [
    f"DEEPSEEK_API_KEY={args.api_key}",
    f"DEEPSEEK_BASE_URL=https://api.deepseek.com",
    f"MODEL_NAME=deepseek-chat",
    f"MAX_LOOPS=20",
  ]

  session = api_post(f"{api_base}/api/v1/sessions", {
    "project_id": args.project,
    "user_id": "interactive-user",
    "strategy": args.strategy,
    "image": "agent-runtime:latest",
    "env_vars": env_vars,
  })
  session_id = session["id"]
  info(f"Session created: {session_id}   (status: {session['status']})")

  # ── 3. Wait for session to be ready ──────────────────────────────────────
  info("Waiting for session to be ready (container starting …) …")
  try:
    ready_resp = api_get(f"{api_base}/api/v1/sessions/{session_id}/wait")
    ok(f"Session is ready — container {ready_resp.get('container_id', '?')[:12]}")
  except SystemExit:
    # api_get calls fail() which does sys.exit; re-check status for detail
    s = api_get(f"{api_base}/api/v1/sessions/{session_id}")
    fail(f"Session failed to start. Status: {s.get('status')}. "
         f"Check platform.log for details.")

  # ── 4. Configure agent ───────────────────────────────────────────────────
  info("Configuring agent (builtin tools: bash, file_read, file_write, list_files, export_files) …")

  configure_body = {
    "builtin_tools": ["bash", "file_read", "file_write", "list_files", "export_files"],
    "agent_config": {
      "max_loops": "20",
    },
  }
  if args.system_prompt:
    configure_body["system_prompt"] = args.system_prompt

  configure_resp = api_post(f"{api_base}/api/v1/sessions/{session_id}/configure", configure_body)
  ok(f"Agent configured — tools: {configure_resp.get('available_tools', [])}")

  # ── 5. Interactive loop ──────────────────────────────────────────────────
  print()
  print(f"{GREEN}{'═' * 64}{NC}")
  print(f"{GREEN}  Agent is ready! Type your task / prompt below.{NC}")
  print(f"{GREEN}  Commands:  /files     — list workspace files{NC}")
  print(f"{GREEN}             /read PATH — read a file from workspace{NC}")
  print(f"{GREEN}             /sync      — sync files to host{NC}")
  print(f"{GREEN}             /status    — show session status{NC}")
  print(f"{GREEN}             /quit      — terminate session and exit{NC}")
  print(f"{GREEN}{'═' * 64}{NC}")
  print()

  try:
    while True:
      try:
        user_input = input(f"{BOLD}You > {NC}").strip()
      except EOFError:
        break

      if not user_input:
        continue

      # ── Slash commands ────────────────────────────────────────────
      if user_input.lower() in ("/quit", "/exit", "/q"):
        info("Terminating session …")
        try:
          api_post(f"{api_base}/api/v1/sessions/{session_id}", None)
        except Exception:
          pass
        # Use DELETE for termination
        try:
          req = Request(f"{api_base}/api/v1/sessions/{session_id}", method="DELETE")
          urlopen(req, timeout=10)
        except Exception:
          pass
        ok("Session terminated. Goodbye!")
        break

      if user_input.lower() == "/files":
        resp = api_get(f"{api_base}/api/v1/sessions/{session_id}/files")
        print(f"\n{DIM}{resp.get('output', '(empty)')}{NC}\n")
        continue

      if user_input.lower().startswith("/read "):
        path = user_input[6:].strip()
        resp = api_get(f"{api_base}/api/v1/sessions/{session_id}/files/read?path={path}")
        print(f"\n{DIM}── {path} ──{NC}")
        print(resp.get("content", "(empty)"))
        print(f"{DIM}{'─' * 40}{NC}\n")
        continue

      if user_input.lower() == "/sync":
        resp = api_post(f"{api_base}/api/v1/sessions/{session_id}/sync", {})
        ok(resp.get("message", "synced"))
        continue

      if user_input.lower() == "/status":
        s = api_get(f"{api_base}/api/v1/sessions/{session_id}")
        print(json.dumps(s, indent=2))
        continue

      # ── Send message to agent ─────────────────────────────────────
      info("Sending message to agent …")
      stop_event = threading.Event()
      stream_thread = threading.Thread(
        target=stream_events,
        args=(api_base, session_id, stop_event),
        daemon=True,
      )
      stream_thread.start()

      # Small delay so SSE subscription is ready before we send the message
      time.sleep(0.3)

      api_post(f"{api_base}/api/v1/sessions/{session_id}/chat", {
        "message": user_input,
      })

      # Wait for the agent to finish (stream_events sets stop_event)
      stream_thread.join(timeout=300)
      if stream_thread.is_alive():
        warn("Agent timed out (5 min). You can /quit or send another message.")
        stop_event.set()

  except KeyboardInterrupt:
    print()
    info("Interrupted — terminating session …")
    try:
      req = Request(f"{api_base}/api/v1/sessions/{session_id}", method="DELETE")
      urlopen(req, timeout=10)
    except Exception:
      pass
    ok("Session terminated. Goodbye!")


if __name__ == "__main__":
  main()