from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class EventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    EVENT_TYPE_UNSPECIFIED: _ClassVar[EventType]
    EVENT_TYPE_THOUGHT: _ClassVar[EventType]
    EVENT_TYPE_TOOL_CALL: _ClassVar[EventType]
    EVENT_TYPE_TOOL_RESULT: _ClassVar[EventType]
    EVENT_TYPE_ANSWER: _ClassVar[EventType]
    EVENT_TYPE_ERROR: _ClassVar[EventType]
    EVENT_TYPE_STATUS: _ClassVar[EventType]

class ServiceStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SERVICE_STATUS_UNSPECIFIED: _ClassVar[ServiceStatus]
    SERVICE_STATUS_OK: _ClassVar[ServiceStatus]
    SERVICE_STATUS_BUSY: _ClassVar[ServiceStatus]
EVENT_TYPE_UNSPECIFIED: EventType
EVENT_TYPE_THOUGHT: EventType
EVENT_TYPE_TOOL_CALL: EventType
EVENT_TYPE_TOOL_RESULT: EventType
EVENT_TYPE_ANSWER: EventType
EVENT_TYPE_ERROR: EventType
EVENT_TYPE_STATUS: EventType
SERVICE_STATUS_UNSPECIFIED: ServiceStatus
SERVICE_STATUS_OK: ServiceStatus
SERVICE_STATUS_BUSY: ServiceStatus

class ConfigureRequest(_message.Message):
    __slots__ = ("session_id", "system_prompt", "tools", "agent_config", "builtin_tools")
    class AgentConfigEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_PROMPT_FIELD_NUMBER: _ClassVar[int]
    TOOLS_FIELD_NUMBER: _ClassVar[int]
    AGENT_CONFIG_FIELD_NUMBER: _ClassVar[int]
    BUILTIN_TOOLS_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    system_prompt: str
    tools: _containers.RepeatedCompositeFieldContainer[ToolDef]
    agent_config: _containers.ScalarMap[str, str]
    builtin_tools: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, session_id: _Optional[str] = ..., system_prompt: _Optional[str] = ..., tools: _Optional[_Iterable[_Union[ToolDef, _Mapping]]] = ..., agent_config: _Optional[_Mapping[str, str]] = ..., builtin_tools: _Optional[_Iterable[str]] = ...) -> None: ...

class ConfigureResponse(_message.Message):
    __slots__ = ("success", "message", "available_tools")
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    AVAILABLE_TOOLS_FIELD_NUMBER: _ClassVar[int]
    success: bool
    message: str
    available_tools: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, success: bool = ..., message: _Optional[str] = ..., available_tools: _Optional[_Iterable[str]] = ...) -> None: ...

class ToolDef(_message.Message):
    __slots__ = ("name", "description", "parameters_json")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PARAMETERS_JSON_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    parameters_json: str
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., parameters_json: _Optional[str] = ...) -> None: ...

class RunRequest(_message.Message):
    __slots__ = ("session_id", "input_text", "env_vars")
    class EnvVarsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    INPUT_TEXT_FIELD_NUMBER: _ClassVar[int]
    ENV_VARS_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    input_text: str
    env_vars: _containers.ScalarMap[str, str]
    def __init__(self, session_id: _Optional[str] = ..., input_text: _Optional[str] = ..., env_vars: _Optional[_Mapping[str, str]] = ...) -> None: ...

class StopRequest(_message.Message):
    __slots__ = ("session_id",)
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    def __init__(self, session_id: _Optional[str] = ...) -> None: ...

class StopResponse(_message.Message):
    __slots__ = ("success", "message")
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    success: bool
    message: str
    def __init__(self, success: bool = ..., message: _Optional[str] = ...) -> None: ...

class AgentEvent(_message.Message):
    __slots__ = ("type", "content", "source", "timestamp", "metadata_json")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    METADATA_JSON_FIELD_NUMBER: _ClassVar[int]
    type: EventType
    content: str
    source: str
    timestamp: int
    metadata_json: str
    def __init__(self, type: _Optional[_Union[EventType, str]] = ..., content: _Optional[str] = ..., source: _Optional[str] = ..., timestamp: _Optional[int] = ..., metadata_json: _Optional[str] = ...) -> None: ...

class Ping(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class Pong(_message.Message):
    __slots__ = ("status",)
    STATUS_FIELD_NUMBER: _ClassVar[int]
    status: ServiceStatus
    def __init__(self, status: _Optional[_Union[ServiceStatus, str]] = ...) -> None: ...
