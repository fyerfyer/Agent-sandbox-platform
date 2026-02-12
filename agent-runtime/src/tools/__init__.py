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
  create_service,
  remove_service,
  export_files,
  CREATE_SERVICE_TOOL_SCHEMA,
  REMOVE_SERVICE_TOOL_SCHEMA,
  EXPORT_FILES_TOOL_SCHEMA,
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
  "create_service": {
    "schema": CREATE_SERVICE_TOOL_SCHEMA,
    "executor": create_service,
  },
  "remove_service": {
    "schema": REMOVE_SERVICE_TOOL_SCHEMA,
    "executor": remove_service,
  },
  "export_files": {
    "schema": EXPORT_FILES_TOOL_SCHEMA,
    "executor": export_files,
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