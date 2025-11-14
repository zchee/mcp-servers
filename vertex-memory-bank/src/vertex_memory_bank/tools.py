"""MCP tool implementations for Vertex Memory Bank."""

from __future__ import annotations

from collections.abc import Iterable
import logging
from typing import Any, Protocol, cast

from mcp.server.fastmcp import FastMCP
import vertexai

from .app_state import app_state
from .concurrency import run_blocking
from .config import Config
from .engines import extract_agent_engine_name
from .formatters import (
    format_conversation_events,
    format_error_response,
    format_memory,
    format_success_response,
    format_ttl_expiration,
    serialize_memories,
)
from .validators import (
    validate_conversation,
    validate_memory_fact,
    validate_memory_topics,
    validate_scope,
    validate_top_k,
)


logger = logging.getLogger(__name__)


class _ConfigProtocol(Protocol):
    project_id: str
    location: str
    agent_engine_name: str | None
    credentials_path: Any

    def copy_with_agent_engine(self, agent_engine_name: str) -> Config: ...


def register_tools(server: FastMCP) -> None:
    """Bind tool handlers to the FastMCP server instance."""

    async def _ensure_client(project_id: str, location: str) -> vertexai.Client:
        current_config = cast(_ConfigProtocol, app_state.config)
        if (
            app_state.client is not None
            and current_config.project_id == project_id
            and current_config.location == location
        ):
            return app_state.client

        def _build() -> vertexai.Client:
            return vertexai.Client(project=project_id, location=location)

        client = await run_blocking(_build)
        app_state.client = client
        app_state.config = Config(
            project_id=project_id,
            location=location,
            agent_engine_name=current_config.agent_engine_name,
            credentials_path=current_config.credentials_path,
        )
        logger.info("Vertex client refreshed for project %s", project_id)
        return client

    async def _ensure_agent_engine(
        client: vertexai.Client,
        *,
        existing_name: str | None,
        memory_topics: Iterable[str] | None,
        force_new: bool,
    ) -> str:
        if existing_name and not force_new:
            logger.info("Using user-specified Agent Engine %s", existing_name)
            engine = await run_blocking(client.agent_engines.get, name=existing_name)
            name = extract_agent_engine_name(engine)
        elif app_state.agent_engine_name and not force_new:
            logger.info("Reusing cached Agent Engine %s", app_state.agent_engine_name)
            name = app_state.agent_engine_name
        else:
            creation_config = None
            if memory_topics:
                creation_config = {
                    "context_spec": {
                        "memory_bank_config": {
                            "customization_configs": [
                                {
                                    "memory_topics": [
                                        {
                                            "managed_memory_topic": {
                                                "managed_topic_enum": topic,
                                            }
                                        }
                                        for topic in memory_topics
                                    ]
                                }
                            ]
                        }
                    }
                }

            logger.info("Creating new Agent Engine with Memory Bank enabled")
            engine = await run_blocking(client.agent_engines.create, config=creation_config)
            name = extract_agent_engine_name(engine)

        app_state.agent_engine_name = name
        config_state = cast(_ConfigProtocol, app_state.config)
        app_state.config = config_state.copy_with_agent_engine(name)
        return name

    def _get_client() -> vertexai.Client:
        client = app_state.client
        if client is None:
            raise RuntimeError("Vertex client is not initialized")
        return client

    def _get_agent_engine_name() -> str:
        name = app_state.agent_engine_name
        if name is None:
            raise RuntimeError("Agent Engine is not initialized")
        return name

    def _require_initialized() -> dict[str, Any] | None:
        if not app_state.is_ready():
            return format_error_response("Memory Bank not initialized. Call initialize_memory_bank first.")
        return None

    @server.tool()
    async def initialize_memory_bank(  # noqa: D401 - docstring expanded below
        project_id: str,
        location: str = "us-central1",
        memory_topics: list[str] | None = None,
        agent_engine_name: str | None = None,
        force_new_agent_engine: bool = False,
    ) -> dict[str, Any]:
        """Initialize or reuse Vertex AI Memory Bank resources."""

        if not project_id:
            return format_error_response("project_id is required")

        if error := validate_memory_topics(memory_topics):
            return format_error_response(error)

        client = await _ensure_client(project_id, location)
        agent_name = await _ensure_agent_engine(
            client,
            existing_name=agent_engine_name,
            memory_topics=memory_topics,
            force_new=force_new_agent_engine,
        )

        return format_success_response({
            "project_id": project_id,
            "location": location,
            "agent_engine_name": agent_name,
        })

    @server.tool()
    async def generate_memories(
        conversation: list[dict[str, str]],
        scope: dict[str, str],
        wait_for_completion: bool = True,
    ) -> dict[str, Any]:
        """Generate long-term memories from a conversation transcript."""

        init_error = _require_initialized()
        if init_error:
            return init_error

        scope_error = validate_scope(scope)
        if scope_error:
            return format_error_response(scope_error)

        conversation_error = validate_conversation(conversation)
        if conversation_error:
            return format_error_response(conversation_error)

        events = format_conversation_events(conversation)
        client = _get_client()
        agent_name = _get_agent_engine_name()

        def _generate() -> Any:
            return client.agent_engines.generate_memories(
                name=agent_name,
                direct_contents_source={"events": events},
                scope=scope,
                config={"wait_for_completion": wait_for_completion},
            )

        try:
            operation = await run_blocking(_generate)
        except Exception as exc:  # pragma: no cover - network failure
            logger.error("Failed to generate memories: %s", exc)
            return format_error_response(str(exc))

        response: dict[str, Any] = {
            "operation_name": getattr(operation, "name", None),
            "done": getattr(operation, "done", False),
            "scope": scope,
        }
        generated = getattr(operation, "response", None)
        if generated and hasattr(generated, "generated_memories"):
            memories = generated.generated_memories
        elif generated and hasattr(generated, "generatedMemories"):
            memories = generated.generatedMemories
        else:
            memories = None
        if memories:
            response["generated_memories"] = [format_memory(memory) for memory in memories]

        logger.info("Generated memories for scope %s", scope)
        return format_success_response(response)

    @server.tool()
    async def retrieve_memories(
        scope: dict[str, str],
        search_query: str | None = None,
        top_k: int = 5,
    ) -> dict[str, Any]:
        """Retrieve stored memories, optionally using similarity search."""

        init_error = _require_initialized()
        if init_error:
            return init_error

        scope_error = validate_scope(scope)
        if scope_error:
            return format_error_response(scope_error)

        top_k_error = validate_top_k(top_k)
        if top_k_error:
            return format_error_response(top_k_error)

        client = _get_client()
        agent_name = _get_agent_engine_name()

        def _retrieve() -> Any:
            if search_query:
                return client.agent_engines.retrieve_memories(
                    name=agent_name,
                    scope=scope,
                    similarity_search_params={
                        "search_query": search_query,
                        "top_k": top_k,
                    },
                )
            return client.agent_engines.retrieve_memories(name=agent_name, scope=scope)

        try:
            results = await run_blocking(_retrieve)
        except Exception as exc:  # pragma: no cover
            logger.error("Failed to retrieve memories: %s", exc)
            return format_error_response(str(exc))

        formatted: list[dict[str, Any]] = []
        for memory in list(results):
            payload = format_memory(memory)
            if search_query and hasattr(memory, "distance"):
                payload["similarity_score"] = memory.distance
            formatted.append(payload)

        return format_success_response({
            "scope": scope,
            "memories_count": len(formatted),
            "memories": formatted,
        })

    @server.tool()
    async def create_memory(
        fact: str,
        scope: dict[str, str],
        ttl_seconds: int | None = None,
    ) -> dict[str, Any]:
        """Create a single memory for the given scope."""

        init_error = _require_initialized()
        if init_error:
            return init_error

        fact_error = validate_memory_fact(fact)
        if fact_error:
            return format_error_response(fact_error)

        scope_error = validate_scope(scope)
        if scope_error:
            return format_error_response(scope_error)

        expire_time = format_ttl_expiration(ttl_seconds) if ttl_seconds is not None and ttl_seconds > 0 else None
        client = _get_client()
        agent_name = _get_agent_engine_name()

        def _create() -> Any:
            return client.agent_engines.create_memory(
                name=agent_name,
                fact=fact.strip(),
                scope=scope,
                config={"expire_time": expire_time} if expire_time else None,
            )

        try:
            operation = await run_blocking(_create)
        except Exception as exc:  # pragma: no cover
            logger.error("Failed to create memory: %s", exc)
            return format_error_response(str(exc))

        memory = getattr(operation, "response", operation)
        return format_success_response({"memory": format_memory(memory)})

    @server.tool()
    async def delete_memory(memory_name: str) -> dict[str, Any]:
        """Delete a memory by fully qualified resource name."""

        init_error = _require_initialized()
        if init_error:
            return init_error
        if not memory_name:
            return format_error_response("memory_name is required")

        client = _get_client()

        def _delete() -> None:
            client.agent_engines.delete_memory(name=memory_name)

        try:
            await run_blocking(_delete)
        except Exception as exc:  # pragma: no cover
            logger.error("Failed to delete memory %s: %s", memory_name, exc)
            return format_error_response(str(exc))

        return format_success_response({"deleted": memory_name})

    @server.tool()
    async def list_memories(page_size: int = 50) -> dict[str, Any]:
        """List all memories within the configured Agent Engine."""

        init_error = _require_initialized()
        if init_error:
            return init_error

        client = _get_client()
        agent_name = _get_agent_engine_name()

        def _list() -> list[dict[str, Any]]:
            pager = client.agent_engines.list_memories(
                name=agent_name,
                config={"page_size": page_size} if page_size else None,
            )
            return serialize_memories(list(pager))

        try:
            memories = await run_blocking(_list)
        except Exception as exc:  # pragma: no cover
            logger.error("Failed to list memories: %s", exc)
            return format_error_response(str(exc))

        return format_success_response({"count": len(memories), "memories": memories})

    _ = (
        initialize_memory_bank,
        generate_memories,
        retrieve_memories,
        create_memory,
        delete_memory,
        list_memories,
    )
