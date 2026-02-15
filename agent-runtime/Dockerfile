FROM python:3.11-slim

# 1. System packages & build tools
#    Install everything the agent might need up-front so that
#    apt-get is NEVER needed at runtime (network may be restricted).
# NOTE: If you find yourself needing to add more packages here, consider whether they can be installed via pip/npm instead, and only add to this list if they truly require apt-get.
RUN apt-get update && apt-get install -y --no-install-recommends \
    # ── Core build tools ──
    build-essential \
    gcc g++ make cmake \
    pkg-config \
    # ── VCS & download ──
    git curl wget \
    # ── Common C/C++ libraries (needed by many pip/npm native modules) ──
    libffi-dev libssl-dev zlib1g-dev \
    libbz2-dev libreadline-dev libsqlite3-dev \
    # ── Database client libraries ──
    libpq-dev \
    default-libmysqlclient-dev \
    # ── Useful CLI utilities ──
    jq tree zip unzip \
    procps net-tools dnsutils \
    # ── For headless browser / playwright (optional) ──
    libnss3 libatk1.0-0 libatk-bridge2.0-0 libcups2 \
    libxkbcommon0 libgbm1 \
    && rm -rf /var/lib/apt/lists/*

# 2. Node.js 20 LTS  
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g npm@latest typescript ts-node yarn pnpm

# 3. Python runtime dependencies
WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# 4. Agent source code
COPY src/ /app/src/

RUN mkdir -p /app/workspace

ENV PYTHONPATH=/app

# Working directory is workspace (for .env loading by pydantic-settings)
WORKDIR /app/workspace

EXPOSE 50051

# For warm pool containers this CMD is overridden by "tail -f /dev/null";
# the worker exec-starts the server after uploading .env + project files.
CMD ["python", "-m", "src.main"]
