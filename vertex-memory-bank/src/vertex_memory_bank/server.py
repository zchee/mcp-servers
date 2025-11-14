"""FastMCP server bootstrap."""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
import logging
import sys
from typing import Any

from mcp.server.fastmcp import FastMCP
import vertexai

from .app_state import AppState, app_state
from .concurrency import run_blocking
from .config import Config
from .engines import extract_agent_engine_name
from .prompts import register_prompts
from .tools import register_tools


logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    stream=sys.stderr,
)
logger = logging.getLogger(__name__)


async def _bootstrap_client(config: Config) -> None:
    """Attempt to create a Vertex AI client from the provided configuration."""
    if not config.has_project_configuration():
        logger.info("GOOGLE_CLOUD_PROJECT not set; waiting for initialize_memory_bank")
        return

    def _build_client() -> vertexai.Client:
        return vertexai.Client(project=config.project_id, location=config.location)

    try:
        client = await run_blocking(_build_client)
    except Exception as exc:  # pragma: no cover - Vertex AI client creation failure
        logger.error("Failed to initialize Vertex AI client: %s", exc)
        raise

    app_state.client = client
    logger.info("Vertex AI client initialized for project %s", config.project_id)

    if config.agent_engine_name:
        await _verify_agent_engine(config.agent_engine_name)


async def _verify_agent_engine(agent_engine_name: str) -> None:
    """Ensure the configured agent engine exists and cache it in application state."""
    client = app_state.client
    if client is None:
        raise RuntimeError("Vertex client not initialized")

    def _get() -> Any:
        return client.agent_engines.get(name=agent_engine_name)

    engine = await run_blocking(_get)
    app_state.agent_engine_name = extract_agent_engine_name(engine)
    logger.info("Reusing Agent Engine %s", app_state.agent_engine_name)


@asynccontextmanager
async def lifespan(_: FastMCP) -> AsyncIterator[AppState]:
    """Set up and tear down server resources."""
    logger.info("Starting Vertex Memory Bank MCP server")
    config = Config.from_env()
    app_state.config = config

    try:
        await _bootstrap_client(config)
    except Exception:  # pragma: no cover - already logged in _bootstrap_client
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
