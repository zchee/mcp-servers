"""Input validation helpers for MCP tools."""

from __future__ import annotations

from collections.abc import Iterable


_VALID_ROLES = {"user", "assistant", "system"}


def validate_scope(scope: dict[str, str]) -> str | None:
    """Ensure the provided scope dictionary only contains string keys and values."""
    if not isinstance(scope, dict):
        return "Scope must be a dictionary"
    if not scope:
        return "Scope cannot be empty"
    for key, value in scope.items():
        if not isinstance(key, str) or not isinstance(value, str):
            return "Scope keys and values must be strings"
    return None


def validate_conversation(conversation: list[dict[str, str]]) -> str | None:
    """Verify that each conversation turn contains a role and text content."""
    if not isinstance(conversation, list):
        return "Conversation must be a list"
    if not conversation:
        return "Conversation cannot be empty"
    for index, turn in enumerate(conversation):
        if not isinstance(turn, dict):
            return f"Turn {index} must be a dictionary"
        role = turn.get("role")
        if role not in _VALID_ROLES:
            return f"Turn {index} has invalid role"
        content = turn.get("content")
        if not content or not isinstance(content, str):
            return f"Turn {index} must include non-empty content"
    return None


def validate_memory_fact(fact: str) -> str | None:
    """Ensure that facts are non-empty and bounded."""
    if not isinstance(fact, str):
        return "Fact must be a string"
    if not fact.strip():
        return "Fact cannot be empty"
    if len(fact) > 10_000:
        return "Fact exceeds 10k character limit"
    return None


def validate_memory_topics(topics: Iterable[str] | None) -> str | None:
    """Validate custom memory topics passed to initialize_memory_bank."""
    if topics is None:
        return None
    if not isinstance(topics, Iterable):
        return "memory_topics must be an iterable of strings"
    for topic in topics:
        if not isinstance(topic, str) or not topic:
            return "memory_topics entries must be non-empty strings"
    return None


def validate_top_k(value: int) -> str | None:
    """Validate requested retrieval size."""
    if value <= 0:
        return "top_k must be positive"
    if value > 100:
        return "top_k cannot exceed 100"
    return None
