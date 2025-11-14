"""Utility helpers for normalizing responses returned to MCP clients."""

from __future__ import annotations

from collections.abc import Iterable
from datetime import UTC, datetime, timedelta
from typing import Any


def format_memory(memory: Any) -> dict[str, Any]:
    """Convert a Vertex memory object into a serializable dictionary."""
    if memory is None:
        return {"status": "empty"}

    def _safe_get(obj: Any, attr: str) -> Any:
        value = getattr(obj, attr, None)
        if value is not None:
            return value
        api_resource = getattr(obj, "api_resource", None)
        if api_resource is not None:
            return getattr(api_resource, attr, None)
        return None

    memory_obj = getattr(memory, "memory", memory)
    return {
        "name": _safe_get(memory_obj, "name"),
        "fact": _safe_get(memory_obj, "fact"),
        "scope": _safe_get(memory_obj, "scope"),
        "created_time": str(_safe_get(memory_obj, "created_time")),
        "updated_time": str(_safe_get(memory_obj, "updated_time")),
    }


def format_conversation_events(conversation: list[dict[str, str]]) -> list[dict[str, Any]]:
    """Translate a chat transcript into the event payload expected by Vertex AI."""
    return [
        {
            "content": {
                "role": turn["role"],
                "parts": [{"text": turn["content"]}],
            }
        }
        for turn in conversation
    ]


def format_error_response(message: str, *, details: dict[str, Any] | None = None) -> dict[str, Any]:
    """Return a consistent error payload for MCP clients."""
    response: dict[str, Any] = {"status": "error", "error": message}
    if details:
        response["details"] = details
    return response


def format_success_response(data: dict[str, Any] | None = None, *, message: str | None = None) -> dict[str, Any]:
    """Return a consistent success payload."""
    response: dict[str, Any] = {"status": "success"}
    if message:
        response["message"] = message
    if data:
        response.update(data)
    return response


def format_ttl_expiration(ttl_seconds: int) -> str:
    """Return an RFC3339 timestamp representing ttl_seconds in the future."""
    expiration = datetime.now(tz=UTC) + timedelta(seconds=ttl_seconds)
    return expiration.isoformat().replace("+00:00", "Z")


def serialize_memories(memories: Iterable[Any]) -> list[dict[str, Any]]:
    """Serialize an iterable of Vertex memories into plain dictionaries."""
    return [format_memory(memory) for memory in memories]
