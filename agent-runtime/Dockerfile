FROM python:3.11-slim

# Install system dependencies (git for repos, curl for health checks)
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy and install Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy agent source code
COPY src/ /app/src/

# Create workspace directory
RUN mkdir -p /app/workspace

# Python path so "from src.xxx import yyy" works
ENV PYTHONPATH=/app

# Working directory is workspace (for .env loading by pydantic-settings)
WORKDIR /app/workspace

# Expose gRPC port
EXPOSE 50051

# Start gRPC server
# For warm pool containers this CMD is overridden by "tail -f /dev/null";
# the worker exec-starts the server after uploading .env + project files.
CMD ["python", "-m", "src.main"]
