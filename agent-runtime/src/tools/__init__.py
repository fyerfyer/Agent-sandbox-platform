from src.tools.bash_tool import bash_execute, BASH_TOOL_SCHEMA
from src.tools.file_tool import (
  file_read,
  file_write,
  list_files,
  FILE_READ_TOOL_SCHEMA,
  FILE_WRITE_TOOL_SCHEMA,
  LIST_FILES_TOOL_SCHEMA,
)
from src.tools.platform_tool import (
  export_files,
  EXPORT_FILES_TOOL_SCHEMA,
)
from src.tools.compose_tool import (
  create_compose_stack,
  teardown_compose_stack,
  get_compose_stack,
  CREATE_COMPOSE_STACK_TOOL_SCHEMA,
  TEARDOWN_COMPOSE_STACK_TOOL_SCHEMA,
  GET_COMPOSE_STACK_TOOL_SCHEMA,
)

# name -> (openai-function-schema, async executor)
TOOL_REGISTRY: dict = {
  "bash": {
    "schema": BASH_TOOL_SCHEMA,
    "executor": bash_execute,
  },
  "file_read": {
    "schema": FILE_READ_TOOL_SCHEMA,
    "executor": file_read,
  },
  "file_write": {
    "schema": FILE_WRITE_TOOL_SCHEMA,
    "executor": file_write,
  },
  "list_files": {
    "schema": LIST_FILES_TOOL_SCHEMA,
    "executor": list_files,
  },
  "export_files": {
    "schema": EXPORT_FILES_TOOL_SCHEMA,
    "executor": export_files,
  },
  "create_compose_stack": {
    "schema": CREATE_COMPOSE_STACK_TOOL_SCHEMA,
    "executor": create_compose_stack,
  },
  "teardown_compose_stack": {
    "schema": TEARDOWN_COMPOSE_STACK_TOOL_SCHEMA,
    "executor": teardown_compose_stack,
  },
  "get_compose_stack": {
    "schema": GET_COMPOSE_STACK_TOOL_SCHEMA,
    "executor": get_compose_stack,
  },
}


def get_tool_schemas(names: list[str]) -> list[dict]:
  schemas = []
  for name in names:
    entry = TOOL_REGISTRY.get(name)
    if entry:
      schemas.append(entry["schema"])
  return schemas


def get_tool_executor(name: str):
  entry = TOOL_REGISTRY.get(name)
  return entry["executor"] if entry else None