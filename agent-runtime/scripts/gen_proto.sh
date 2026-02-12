#!/bin/bash
set -e

# Generate Python Proto files
echo "Generating Python proto files from ../platform/internal/agentproto/agent.proto..."
python -m grpc_tools.protoc \
    -I../platform/internal/agentproto \
    --python_out=./src/pb \
    --grpc_python_out=./src/pb \
    --pyi_out=./src/pb \
    ../platform/internal/agentproto/agent.proto

# Fix import path in generated gRPC file
# Change "import agent_pb2" to "from . import agent_pb2"
echo "Fixing relative imports in generated files..."
sed -i 's/^import agent_pb2 as agent__pb2/from . import agent_pb2 as agent__pb2/' src/pb/agent_pb2_grpc.py

# Ensure __init__.py exists
touch src/pb/__init__.py

echo "Done! Proto files generated in src/pb/"
