"""FastMCP server bootstrap."""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
import logging
import sys

from fastmcp import FastMCP

from .app_state import AppState, app_state
from .clients import VertexClientManager
from .config import Config
from .prompts import register_prompts
from .services import MemoryBankService
from .tools import register_tools


logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    stream=sys.stderr,
)
logger = logging.getLogger(__name__)


async def _initialize_state(config: Config) -> None:
    """Construct stateful collaborators for the FastMCP lifespan."""
    manager = VertexClientManager(config=config)
    service = MemoryBankService(client_manager=manager)
    app_state.attach(config=config, client_manager=manager, memory_service=service)
    try:
        await manager.bootstrap()
    except Exception:
        logger.exception("Failed to initialize Vertex client")
        raise


@asynccontextmanager
async def lifespan(_: FastMCP) -> AsyncIterator[AppState]:
    """Set up and tear down server resources."""
    logger.info("Starting Vertex Memory Bank MCP server")
    config = Config.from_env()

    try:
        await _initialize_state(config)
    except Exception:  # pragma: no cover - already logged
        app_state.reset()

    try:
        yield app_state
    finally:
        logger.info("Shutting down Vertex Memory Bank MCP server")
        app_state.reset()


def create_server() -> FastMCP:
    """Build and configure the FastMCP server instance."""
    server = FastMCP("Vertex AI Memory Bank", lifespan=lifespan)
    register_tools(server)
    register_prompts(server)
    return server


def run() -> None:
    """Start the MCP server, exiting with code 1 on failure."""
    try:
        server = create_server()
        server.run()
    except KeyboardInterrupt:
        logger.info("Server interrupted by user")
    except Exception as exc:  # pragma: no cover - process-level failure
        logger.error("Fatal server error: %s", exc)
        raise SystemExit(1) from exc
