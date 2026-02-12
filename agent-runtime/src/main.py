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
  server = grpc.aio.server(futures.ThreadPoolExecutor(max_workers=10))
  agent_pb2_grpc.add_AgentServiceServicer_to_server(AgentService(), server)
  port = settings.PORT
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