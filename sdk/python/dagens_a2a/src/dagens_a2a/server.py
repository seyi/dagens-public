"""
A2A HTTP Server

FastAPI-based server that exposes an A2A adapter as HTTP endpoints.
"""

import logging
import uuid
from contextlib import asynccontextmanager
from typing import Any, Dict, Optional

import uvicorn
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse
from opentelemetry import trace
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.trace import SpanKind, Status, StatusCode

from dagens_a2a.adapter import A2AAdapter
from dagens_a2a.protocol import (
    AgentCard,
    InvocationRequest,
    InvocationResponse,
    RPCError,
    RPCErrorCode,
)

logger = logging.getLogger(__name__)
tracer = trace.get_tracer(__name__)


class A2AServer:
    """
    HTTP server exposing an A2A adapter.

    This server implements the A2A protocol endpoints:
    - POST /a2a/invoke - Execute agent invocations
    - GET /a2a/card - Get agent capability card
    - GET /health - Health check endpoint

    Example:
        adapter = MyAdapter(...)
        server = A2AServer(adapter, port=8081)
        server.run()
    """

    def __init__(
        self,
        adapter: A2AAdapter,
        port: int = 8080,
        host: str = "0.0.0.0",
        enable_telemetry: bool = True,
    ):
        """
        Create an A2A server.

        Args:
            adapter: The A2A adapter to expose
            port: Port to listen on
            host: Host to bind to
            enable_telemetry: Whether to enable OpenTelemetry instrumentation
        """
        self.adapter = adapter
        self.port = port
        self.host = host
        self.enable_telemetry = enable_telemetry
        self._card: Optional[AgentCard] = None

        # Create FastAPI app with lifespan
        @asynccontextmanager
        async def lifespan(app: FastAPI):
            # Startup
            logger.info(f"Starting A2A server for agent: {self.adapter.get_card().name}")
            self._card = self.adapter.get_card()
            await self.adapter.on_startup()
            yield
            # Shutdown
            logger.info("Shutting down A2A server")
            await self.adapter.on_shutdown()

        self.app = FastAPI(
            title=f"A2A Agent: {adapter.get_card().name}",
            description=adapter.get_card().description,
            version=adapter.get_card().version,
            lifespan=lifespan,
        )

        self._setup_routes()

        if enable_telemetry:
            self._setup_telemetry()

    def _setup_routes(self):
        """Configure HTTP routes"""

        @self.app.post("/a2a/invoke", response_model=InvocationResponse)
        async def invoke(request: InvocationRequest) -> InvocationResponse:
            """Handle agent invocation requests"""
            with tracer.start_as_current_span(
                "a2a.invoke",
                kind=SpanKind.SERVER,
            ) as span:
                span.set_attribute("a2a.method", request.method)
                span.set_attribute("a2a.request.id", request.id)
                span.set_attribute("a2a.agent.id", self._card.id if self._card else "unknown")

                try:
                    result = await self.adapter.invoke(request.method, request.params)

                    span.set_status(Status(StatusCode.OK))
                    return InvocationResponse(
                        id=request.id,
                        result=result,
                    )

                except Exception as e:
                    logger.exception(f"Invocation failed: {e}")
                    span.record_exception(e)
                    span.set_status(Status(StatusCode.ERROR, str(e)))

                    return InvocationResponse(
                        id=request.id,
                        error=RPCError(
                            code=RPCErrorCode.INTERNAL_ERROR,
                            message=str(e),
                        ),
                    )

        @self.app.get("/a2a/card", response_model=AgentCard)
        async def card() -> AgentCard:
            """Return the agent's capability card"""
            if self._card is None:
                self._card = self.adapter.get_card()
            return self._card

        @self.app.get("/health")
        async def health() -> Dict[str, Any]:
            """Health check endpoint"""
            return await self.adapter.health_check()

        @self.app.get("/a2a/discover")
        async def discover() -> Dict[str, Any]:
            """Discovery endpoint for this agent"""
            card = self._card or self.adapter.get_card()
            return {
                "id": card.id,
                "name": card.name,
                "description": card.description,
                "capabilities": [cap.name for cap in card.capabilities],
                "status": "available",
            }

        # Error handler for validation errors
        @self.app.exception_handler(Exception)
        async def generic_exception_handler(request: Request, exc: Exception):
            logger.exception(f"Unhandled exception: {exc}")
            return JSONResponse(
                status_code=500,
                content={
                    "jsonrpc": "2.0",
                    "id": None,
                    "error": {
                        "code": RPCErrorCode.INTERNAL_ERROR,
                        "message": "Internal server error",
                    },
                },
            )

    def _setup_telemetry(self):
        """Configure OpenTelemetry instrumentation"""
        FastAPIInstrumentor.instrument_app(self.app)

    def run(self):
        """Start the server (blocking)"""
        logger.info(f"Starting A2A server on {self.host}:{self.port}")
        uvicorn.run(
            self.app,
            host=self.host,
            port=self.port,
            log_level="info",
        )

    async def run_async(self):
        """Start the server (async)"""
        config = uvicorn.Config(
            self.app,
            host=self.host,
            port=self.port,
            log_level="info",
        )
        server = uvicorn.Server(config)
        await server.serve()


def create_server(
    adapter: A2AAdapter,
    port: int = 8080,
    host: str = "0.0.0.0",
) -> A2AServer:
    """
    Factory function to create an A2A server.

    Args:
        adapter: The adapter to expose
        port: Port to listen on
        host: Host to bind to

    Returns:
        Configured A2AServer instance
    """
    return A2AServer(adapter, port=port, host=host)
