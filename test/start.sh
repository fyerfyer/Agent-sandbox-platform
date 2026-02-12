#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PLATFORM_DIR="$ROOT_DIR/platform"
RUNTIME_DIR="$ROOT_DIR/agent-runtime"

# ── Colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
fail()  { echo -e "${RED}[FAIL]${NC}  $*"; exit 1; }

# ── Configuration ────────────────────────────────────────────────────────────
export DEEPSEEK_API_KEY="${DEEPSEEK_API_KEY:-sk-xxxx}"

export POSTGRES_USER="${POSTGRES_USER:-postgres}"

export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-postgres}"
export POSTGRES_DB="${POSTGRES_DB:-agent_platform}"
export POSTGRES_ADDR="${POSTGRES_ADDR:-localhost:5432}"
export REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"

export POOL_HOST_ROOT="${POOL_HOST_ROOT:-$HOME/.agent-platform/projects}"
export WORKER_PROJECT_DIR="${WORKER_PROJECT_DIR:-$HOME/.agent-platform/projects}"
export POOL_MIN_IDLE="${POOL_MIN_IDLE:-0}"
export POOL_MAX_BURST="${POOL_MAX_BURST:-5}"
export POOL_WARMUP_IMAGE="${POOL_WARMUP_IMAGE:-agent-runtime:latest}"
export POOL_NETWORK_NAME="${POOL_NETWORK_NAME:-agent-platform-net}"

PLATFORM_API="http://localhost:8080"
PLATFORM_PID=""

# ── Cleanup on exit ──────────────────────────────────────────────────────────
cleanup() {
    if [[ -n "$PLATFORM_PID" ]] && kill -0 "$PLATFORM_PID" 2>/dev/null; then
        info "Stopping platform server (PID $PLATFORM_PID) …"
        kill "$PLATFORM_PID" 2>/dev/null || true
        wait "$PLATFORM_PID" 2>/dev/null || true
        ok "Platform server stopped."
    fi
}
trap cleanup EXIT

# ═════════════════════════════════════════════════════════════════════════════
#  Step 1: Pre-flight checks
# ═════════════════════════════════════════════════════════════════════════════
info "Running pre-flight checks …"
command -v docker   >/dev/null 2>&1 || fail "docker is not installed"
command -v go       >/dev/null 2>&1 || fail "go is not installed"
command -v curl     >/dev/null 2>&1 || fail "curl is not installed"
command -v python3  >/dev/null 2>&1 || fail "python3 is not installed"
ok "All required tools found."

# ═════════════════════════════════════════════════════════════════════════════
#  Step 2: Start infrastructure (PostgreSQL + Redis)
# ═════════════════════════════════════════════════════════════════════════════
info "Starting infrastructure (PostgreSQL + Redis) …"
cd "$PLATFORM_DIR"

# Use docker-compose.yml to start infra
if ! docker ps --format '{{.Names}}' | grep -q "agent-platform-postgres"; then
    docker compose up -d 2>&1 | sed 's/^/  /'
    info "Waiting for PostgreSQL to be healthy …"
    for i in $(seq 1 30); do
        if docker exec agent-platform-postgres pg_isready -U postgres -d agent_platform >/dev/null 2>&1; then
            break
        fi
        sleep 1
    done
    docker exec agent-platform-postgres pg_isready -U postgres -d agent_platform >/dev/null 2>&1 || fail "PostgreSQL did not start"
    ok "PostgreSQL is ready."
else
    ok "PostgreSQL is already running."
fi

if ! docker ps --format '{{.Names}}' | grep -q "agent-platform-redis"; then
    warn "Redis container not found – docker compose should have started it."
else
    ok "Redis is already running."
fi

# ═════════════════════════════════════════════════════════════════════════════
#  Step 3: Ensure Docker network exists
# ═════════════════════════════════════════════════════════════════════════════
if ! docker network ls --format '{{.Name}}' | grep -q "^${POOL_NETWORK_NAME}$"; then
    info "Creating Docker network: $POOL_NETWORK_NAME"
    docker network create "$POOL_NETWORK_NAME" >/dev/null
fi
ok "Docker network '$POOL_NETWORK_NAME' ready."

# ═════════════════════════════════════════════════════════════════════════════
#  Step 4: Build agent-runtime Docker image
# ═════════════════════════════════════════════════════════════════════════════
info "Building agent-runtime Docker image …"
cd "$RUNTIME_DIR"
if docker images --format '{{.Repository}}:{{.Tag}}' | grep -q "^agent-runtime:latest$"; then
    ok "agent-runtime:latest image already exists (use 'docker rmi agent-runtime' to rebuild)."
else
    docker build -t agent-runtime:latest . 2>&1 | tail -5 | sed 's/^/  /'
    ok "agent-runtime:latest built."
fi

# ═════════════════════════════════════════════════════════════════════════════
#  Step 5: Build platform server
# ═════════════════════════════════════════════════════════════════════════════
info "Building platform server …"
cd "$PLATFORM_DIR"
go build -o bin/platform-server ./cmd/server/ 2>&1 | sed 's/^/  /'
ok "Platform binary built: $PLATFORM_DIR/bin/platform-server"

# ═════════════════════════════════════════════════════════════════════════════
#  Step 6: Create project directories
# ═════════════════════════════════════════════════════════════════════════════
mkdir -p "$POOL_HOST_ROOT"
ok "Project directory ready: $POOL_HOST_ROOT"

# ═════════════════════════════════════════════════════════════════════════════
#  Step 7: Clean stale data (optional but helpful)
# ═════════════════════════════════════════════════════════════════════════════
info "Cleaning stale sessions & tasks …"
docker exec agent-platform-redis redis-cli FLUSHDB >/dev/null 2>&1 || true
docker exec agent-platform-postgres psql -U postgres -d agent_platform \
    -c "DELETE FROM session_models;" >/dev/null 2>&1 || true
ok "Stale data cleaned."

# ═════════════════════════════════════════════════════════════════════════════
#  Step 8: Start platform server
# ═════════════════════════════════════════════════════════════════════════════
# Kill any lingering platform-server process
pkill -f "platform-server" 2>/dev/null || true
sleep 1

info "Starting platform server …"
cd "$PLATFORM_DIR"
"$PLATFORM_DIR/bin/platform-server" > "$PLATFORM_DIR/platform.log" 2>&1 &
PLATFORM_PID=$!
info "Platform server PID: $PLATFORM_PID (log: $PLATFORM_DIR/platform.log)"

# Wait for the API to be reachable
for i in $(seq 1 20); do
    if curl -sf "$PLATFORM_API/health" >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done
curl -sf "$PLATFORM_API/health" >/dev/null 2>&1 || fail "Platform server did not become healthy"
ok "Platform server is healthy."

# ═════════════════════════════════════════════════════════════════════════════
#  Step 9: Ready — launch the interactive client
# ═════════════════════════════════════════════════════════════════════════════
echo ""
echo -e "${GREEN}════════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Agent Platform is ready!${NC}"
echo -e "${GREEN}  API:        $PLATFORM_API${NC}"
echo -e "${GREEN}  API Key:    DEEPSEEK_API_KEY is set${NC}"
echo -e "${GREEN}  Projects:   $POOL_HOST_ROOT${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════════════════${NC}"
echo ""

# Launch interactive Python client
python3 "$SCRIPT_DIR/run_agent.py" \
    --api "$PLATFORM_API" \
    --api-key "$DEEPSEEK_API_KEY"

# After the client exits, cleanup is handled by the trap
