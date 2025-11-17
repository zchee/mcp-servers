"""MCP tool implementations for Vertex Memory Bank."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
import logging
from typing import Any

from fastmcp import FastMCP

from .app_state import app_state
from .errors import InitializationError, MemoryBankError, ValidationError
from .formatters import format_error_response, format_success_response
from .services import MemoryBankService


logger = logging.getLogger(__name__)

ToolCall = Callable[[MemoryBankService], Awaitable[dict[str, Any]]]


def register_tools(server: FastMCP) -> None:
    """Bind tool handlers to the FastMCP server instance."""

    async def _dispatch(call: ToolCall) -> dict[str, Any]:
        try:
            service = _service()
            payload = await call(service)
        except ValidationError as exc:
            return format_error_response(str(exc))
        except InitializationError as exc:
            return format_error_response(str(exc))
        except MemoryBankError as exc:
            logger.error("Memory Bank operation failed: %s", exc)
            return format_error_response(str(exc))
        except Exception as exc:  # pragma: no cover - defensive
            logger.exception("Unexpected tool failure: %s", exc)
            return format_error_response("Unexpected error while processing request")
        return format_success_response(payload)

    def _service() -> MemoryBankService:
        service = app_state.memory_service
        if service is None:
            raise InitializationError("Memory Bank service not initialized")
        return service

    @server.tool()
    async def initialize_memory_bank(
        project_id: str,
        location: str = "us-central1",
        memory_topics: list[str] | None = None,
        agent_engine_name: str | None = None,
        force_new_agent_engine: bool = False,
    ) -> dict[str, Any]:
        return await _dispatch(
            lambda svc: svc.initialize_memory_bank(
                project_id=project_id,
                location=location,
                memory_topics=memory_topics,
                agent_engine_name=agent_engine_name,
                force_new_agent_engine=force_new_agent_engine,
            )
        )

    @server.tool()
    async def generate_memories(
        conversation: list[dict[str, str]],
        scope: dict[str, str],
        wait_for_completion: bool = True,
    ) -> dict[str, Any]:
        return await _dispatch(
            lambda svc: svc.generate_memories(
                conversation=conversation,
                scope=scope,
                wait_for_completion=wait_for_completion,
            )
        )

    @server.tool()
    async def retrieve_memories(
        scope: dict[str, str],
        search_query: str | None = None,
        top_k: int = 5,
    ) -> dict[str, Any]:
        return await _dispatch(
            lambda svc: svc.retrieve_memories(
                scope=scope,
                search_query=search_query,
                top_k=top_k,
            )
        )

    @server.tool()
    async def create_memory(
        fact: str,
        scope: dict[str, str],
        ttl_seconds: int | None = None,
    ) -> dict[str, Any]:
        return await _dispatch(
            lambda svc: svc.create_memory(
                fact=fact,
                scope=scope,
                ttl_seconds=ttl_seconds,
            )
        )

    @server.tool()
    async def delete_memory(memory_name: str) -> dict[str, Any]:
        return await _dispatch(lambda svc: svc.delete_memory(memory_name=memory_name))

    @server.tool()
    async def list_memories(page_size: int = 50) -> dict[str, Any]:
        return await _dispatch(lambda svc: svc.list_memories(page_size=page_size))

    _ = (
        initialize_memory_bank,
        generate_memories,
        retrieve_memories,
        create_memory,
        delete_memory,
        list_memories,
    )
