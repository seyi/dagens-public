"""
CLI for running A2A agents.

Provides a command-line interface to start A2A servers from adapter modules.
"""

import argparse
import importlib
import logging
import sys
from typing import Optional

from dagens_a2a.adapter import A2AAdapter
from dagens_a2a.server import A2AServer

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)
logger = logging.getLogger(__name__)


def load_adapter(module_path: str, attr_name: Optional[str] = None) -> A2AAdapter:
    """
    Dynamically load an adapter from a module path.

    Args:
        module_path: Python module path (e.g., "myagent.adapter")
        attr_name: Attribute name within the module. If not provided,
                   looks for 'adapter' or 'create_adapter()'.

    Returns:
        The loaded A2AAdapter instance
    """
    try:
        module = importlib.import_module(module_path)
    except ImportError as e:
        raise ImportError(f"Could not import module '{module_path}': {e}")

    # If attribute name provided, use it directly
    if attr_name:
        if not hasattr(module, attr_name):
            raise AttributeError(f"Module '{module_path}' has no attribute '{attr_name}'")
        obj = getattr(module, attr_name)
        if callable(obj) and not isinstance(obj, A2AAdapter):
            # It's a factory function
            return obj()
        return obj

    # Try common names
    for name in ['adapter', 'create_adapter', 'get_adapter']:
        if hasattr(module, name):
            obj = getattr(module, name)
            if isinstance(obj, A2AAdapter):
                return obj
            if callable(obj):
                return obj()

    raise AttributeError(
        f"Module '{module_path}' has no 'adapter' attribute or 'create_adapter()' function. "
        f"Available attributes: {dir(module)}"
    )


def main():
    """CLI entry point"""
    parser = argparse.ArgumentParser(
        description="Start an A2A server for a Python agent",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Start server from adapter module
  dagens-a2a --adapter myagent.adapter --port 8081

  # Specify adapter attribute name
  dagens-a2a --adapter myagent --attr research_adapter --port 8082

  # With host binding
  dagens-a2a --adapter myagent.adapter --host 127.0.0.1 --port 8081
        """
    )

    parser.add_argument(
        "--adapter",
        required=True,
        help="Python module path containing the adapter (e.g., 'myagent.adapter')"
    )
    parser.add_argument(
        "--attr",
        help="Attribute name within the module (default: looks for 'adapter' or 'create_adapter')"
    )
    parser.add_argument(
        "--port",
        type=int,
        default=8080,
        help="Port to listen on (default: 8080)"
    )
    parser.add_argument(
        "--host",
        default="0.0.0.0",
        help="Host to bind to (default: 0.0.0.0)"
    )
    parser.add_argument(
        "--no-telemetry",
        action="store_true",
        help="Disable OpenTelemetry instrumentation"
    )
    parser.add_argument(
        "--log-level",
        choices=["DEBUG", "INFO", "WARNING", "ERROR"],
        default="INFO",
        help="Logging level (default: INFO)"
    )

    args = parser.parse_args()

    # Configure logging
    logging.getLogger().setLevel(getattr(logging, args.log_level))

    # Load adapter
    logger.info(f"Loading adapter from '{args.adapter}'")
    try:
        adapter = load_adapter(args.adapter, args.attr)
    except Exception as e:
        logger.error(f"Failed to load adapter: {e}")
        sys.exit(1)

    logger.info(f"Loaded adapter: {adapter.get_card().name} ({adapter.get_card().id})")

    # Create and run server
    server = A2AServer(
        adapter=adapter,
        port=args.port,
        host=args.host,
        enable_telemetry=not args.no_telemetry,
    )

    logger.info(f"Starting A2A server on {args.host}:{args.port}")
    try:
        server.run()
    except KeyboardInterrupt:
        logger.info("Server stopped")


if __name__ == "__main__":
    main()
