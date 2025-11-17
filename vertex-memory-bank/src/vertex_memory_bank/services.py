"""High-level services that implement Vertex Memory Bank workflows."""

from __future__ import annotations

from collections.abc import Callable, Iterable
from dataclasses import dataclass
import logging
from typing import Any, TypeVar

from google.api_core.operation import Operation

from .clients import VertexClientManager
from .concurrency import run_blocking
from .errors import InitializationError, ValidationError, VertexServiceError
from .formatters import (
    format_conversation_events,
    format_memory,
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

T = TypeVar("T")


@dataclass(slots=True)
class MemoryBankService:
    """Facade for Vertex Memory Bank operations used by MCP tools."""

    client_manager: VertexClientManager

    async def initialize_memory_bank(
        self,
        *,
        project_id: str,
        location: str,
        memory_topics: Iterable[str] | None,
        agent_engine_name: str | None,
        force_new_agent_engine: bool,
    ) -> dict[str, Any]:
        """Initialize or reuse a Memory Bank-enabled Agent Engine."""
        if not project_id:
            raise ValidationError("project_id is required")
        self._raise_on_error(validate_memory_topics(memory_topics))

        client = await self.client_manager.ensure_client(project_id, location)
        del client  # client cached for later use
        agent_name = await self.client_manager.ensure_agent_engine(
            existing_name=agent_engine_name,
            memory_topics=memory_topics,
            force_new=force_new_agent_engine,
        )
        return {
            "project_id": project_id,
            "location": location,
            "agent_engine_name": agent_name,
        }

    async def generate_memories(
        self,
        *,
        conversation: list[dict[str, str]],
        scope: dict[str, str],
        wait_for_completion: bool,
    ) -> dict[str, Any]:
        """Generate durable memories from a chat transcript."""
        self._raise_on_error(validate_scope(scope))
        self._raise_on_error(validate_conversation(conversation))

        client, agent_name = self.client_manager.require_ready()
        events = format_conversation_events(conversation)

        def _generate() -> Operation:
            return client.agent_engines.generate_memories(
                name=agent_name,
                direct_contents_source={"events": events},
                scope=scope,
                config={"wait_for_completion": wait_for_completion},
            )

        operation = await self._call_vertex(_generate, "generate memories")
        response: dict[str, Any] = {
            "operation_name": getattr(operation, "name", None),
            "done": getattr(operation, "done", False),
            "scope": scope,
        }
        generated = getattr(operation, "response", None)
        memories = self._extract_generated_memories(generated)
        if memories:
            response["generated_memories"] = [format_memory(memory) for memory in memories]
        logger.info("Generated memories for scope %s", scope)
        return response

    async def retrieve_memories(
        self,
        *,
        scope: dict[str, str],
        search_query: str | None,
        top_k: int,
    ) -> dict[str, Any]:
        """Retrieve stored memories, optionally running similarity search."""
        self._raise_on_error(validate_scope(scope))
        self._raise_on_error(validate_top_k(top_k))
        client, agent_name = self.client_manager.require_ready()

        def _retrieve() -> Iterable[Any]:
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

        results = await self._call_vertex(_retrieve, "retrieve memories")

        formatted: list[dict[str, Any]] = []
        for memory in list(results):
            payload = format_memory(memory)
            if search_query and hasattr(memory, "distance"):
                payload["similarity_score"] = memory.distance
            formatted.append(payload)

        return {
            "scope": scope,
            "memories_count": len(formatted),
            "memories": formatted,
        }

    async def create_memory(
        self,
        *,
        fact: str,
        scope: dict[str, str],
        ttl_seconds: int | None,
    ) -> dict[str, Any]:
        """Create a manual fact scoped to a particular user/context."""
        self._raise_on_error(validate_memory_fact(fact))
        self._raise_on_error(validate_scope(scope))
        client, agent_name = self.client_manager.require_ready()
        expire_time = (
            format_ttl_expiration(ttl_seconds)
            if ttl_seconds is not None and ttl_seconds > 0
            else None
        )

        def _create() -> Any:
            return client.agent_engines.create_memory(
                name=agent_name,
                fact=fact.strip(),
                scope=scope,
                config={"expire_time": expire_time} if expire_time else None,
            )

        operation = await self._call_vertex(_create, "create memory")
        memory = getattr(operation, "response", operation)
        return {"memory": format_memory(memory)}

    async def delete_memory(self, *, memory_name: str) -> dict[str, Any]:
        """Delete a memory by fully qualified resource name."""
        if not memory_name:
            raise ValidationError("memory_name is required")
        client, _ = self.client_manager.require_ready()

        def _delete() -> None:
            client.agent_engines.delete_memory(name=memory_name)

        await self._call_vertex(_delete, "delete memory")
        return {"deleted": memory_name}

    async def list_memories(self, *, page_size: int) -> dict[str, Any]:
        """Return all memories configured on the active Agent Engine."""
        client, agent_name = self.client_manager.require_ready()

        def _list() -> list[dict[str, Any]]:
            pager = client.agent_engines.list_memories(
                name=agent_name,
                config={"page_size": page_size} if page_size else None,
            )
            return serialize_memories(list(pager))

        memories = await self._call_vertex(_list, "list memories")
        return {"count": len(memories), "memories": memories}

    async def _call_vertex(self, func: Callable[[], T], action: str) -> T:
        """Execute a blocking Vertex SDK call without stalling the event loop."""
        try:
            return await run_blocking(func)
        except InitializationError:
            raise
        except Exception as exc:  # pragma: no cover - network errors
            logger.error("Failed to %s: %s", action, exc)
            raise VertexServiceError(str(exc)) from exc

    @staticmethod
    def _raise_on_error(message: str | None) -> None:
        """Convert validator return strings into typed exceptions."""
        if message:
            raise ValidationError(message)

    @staticmethod
    def _extract_generated_memories(result: Any) -> Iterable[Any] | None:
        """Best-effort extraction for Vertex responses with generated memories."""
        if not result:
            return None
        if hasattr(result, "generated_memories"):
            return result.generated_memories
        if hasattr(result, "generatedMemories"):
            return result.generatedMemories
        return None
