import asyncio
import logging
import grpc
from concurrent import futures
from src.config import settings
from src.service import AgentService
from src.pb import agent_pb2_grpc

logging.basicConfig(
  level=logging.INFO,
  format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

async def serve():
  server = grpc.aio.server(
    futures.ThreadPoolExecutor(max_workers=10),
    options=[
      # Match the Go dispatcher keepalive — allow pings every 5s
      ('grpc.keepalive_time_ms', 30000),
      ('grpc.keepalive_timeout_ms', 10000),
      ('grpc.keepalive_permit_without_calls', True),
      ('grpc.http2.min_recv_ping_interval_without_data_ms', 5000),
      ('grpc.http2.max_pings_without_data', 0),
    ],
  )
  agent_pb2_grpc.add_AgentServiceServicer_to_server(AgentService(), server)
  port = settings.GRPC_PORT
  server.add_insecure_port(f'[::]:{port}')
  logger.info(f"Starting Agent Runtime gRPC server on port {port}...")
  await server.start()
  
  # 等待终止
  await server.wait_for_termination()

if __name__ == '__main__':
  try:
    asyncio.run(serve())
  except KeyboardInterrupt:
    logger.info("Server stopped by user")
  except Exception as e:
    logger.error(f"Server failed to start: {e}")