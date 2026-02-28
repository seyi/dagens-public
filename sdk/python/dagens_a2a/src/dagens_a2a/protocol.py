"""
A2A Protocol Types

Defines the JSON-RPC 2.0 based protocol types for Agent-to-Agent communication.
These types align with the Go implementation in pkg/a2a/protocol.go.
"""

from datetime import datetime
from enum import Enum
from typing import Any, Dict, List, Optional

from pydantic import BaseModel, Field


class Modality(str, Enum):
    """Supported interaction modalities"""
    TEXT = "text"
    FORM = "form"
    MEDIA = "media"
    STREAM = "stream"


class CommunicationPattern(str, Enum):
    """Supported communication patterns"""
    REQUEST_RESPONSE = "request_response"
    SERVER_SENT_EVENTS = "server_sent_events"
    ASYNC_PUSH = "async_push"


class AuthType(str, Enum):
    """Authentication types"""
    NONE = "none"
    BASIC = "basic"
    BEARER = "bearer"
    API_KEY = "api_key"
    MTLS = "mtls"


class AuthScheme(BaseModel):
    """Authentication scheme configuration"""
    type: AuthType = AuthType.NONE
    parameters: Dict[str, Any] = Field(default_factory=dict)


class Capability(BaseModel):
    """Describes what an agent can do"""
    name: str
    description: str = ""
    input_schema: Optional[Dict[str, Any]] = None
    output_schema: Optional[Dict[str, Any]] = None
    metadata: Dict[str, Any] = Field(default_factory=dict)


class AgentCard(BaseModel):
    """
    Agent capability declaration (A2A standard).

    This is the primary way agents advertise their capabilities
    to other agents and the orchestration framework.
    """
    id: str
    name: str
    description: str = ""
    version: str = "1.0.0"
    endpoint: str = ""
    capabilities: List[Capability] = Field(default_factory=list)
    modalities: List[Modality] = Field(default_factory=lambda: [Modality.TEXT])
    auth_scheme: AuthScheme = Field(default_factory=AuthScheme)
    metadata: Dict[str, Any] = Field(default_factory=dict)
    supported_patterns: List[CommunicationPattern] = Field(
        default_factory=lambda: [CommunicationPattern.REQUEST_RESPONSE]
    )
    created_at: Optional[datetime] = None
    updated_at: Optional[datetime] = None

    class Config:
        use_enum_values = True


class RPCError(BaseModel):
    """JSON-RPC 2.0 error object"""
    code: int
    message: str
    data: Optional[Any] = None


class InvocationRequest(BaseModel):
    """
    JSON-RPC 2.0 invocation request.

    This is the standard request format for agent invocations.
    """
    jsonrpc: str = "2.0"
    id: str
    method: str
    params: Dict[str, Any] = Field(default_factory=dict)


class InvocationResponse(BaseModel):
    """
    JSON-RPC 2.0 invocation response.

    Contains either a result or an error, never both.
    """
    jsonrpc: str = "2.0"
    id: str
    result: Optional[Any] = None
    error: Optional[RPCError] = None


class StreamChunk(BaseModel):
    """Represents a chunk of streaming data"""
    id: str
    sequence: int
    data: Any
    final: bool = False
    timestamp: datetime = Field(default_factory=datetime.now)


class AgentStatus(str, Enum):
    """Agent availability status"""
    AVAILABLE = "available"
    BUSY = "busy"
    MAINTENANCE = "maintenance"
    OFFLINE = "offline"


class AgentInfo(BaseModel):
    """Basic agent information for discovery"""
    id: str
    name: str
    description: str = ""
    endpoint: str
    capabilities: List[str] = Field(default_factory=list)
    modalities: List[Modality] = Field(default_factory=list)
    status: AgentStatus = AgentStatus.AVAILABLE

    class Config:
        use_enum_values = True


# Standard JSON-RPC 2.0 error codes
class RPCErrorCode:
    """Standard JSON-RPC 2.0 error codes"""
    PARSE_ERROR = -32700
    INVALID_REQUEST = -32600
    METHOD_NOT_FOUND = -32601
    INVALID_PARAMS = -32602
    INTERNAL_ERROR = -32603

    # Custom A2A error codes (server error range: -32000 to -32099)
    AGENT_NOT_FOUND = -32001
    AGENT_UNAVAILABLE = -32002
    AGENT_TIMEOUT = -32003
    CAPABILITY_NOT_FOUND = -32004
