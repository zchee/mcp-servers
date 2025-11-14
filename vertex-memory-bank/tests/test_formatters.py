"""Tests for formatter helpers."""

from datetime import UTC, datetime

from vertex_memory_bank import formatters


class MemoryStub:
    def __init__(self) -> None:
        self.name = "projects/p/locations/l/memories/1"
        self.fact = "User likes Python"
        self.scope = {"user_id": "abc"}
        self.created_time = datetime.now(tz=UTC)
        self.updated_time = datetime.now(tz=UTC)


def test_format_memory_handles_simple_objects() -> None:
    payload = formatters.format_memory(MemoryStub())
    assert payload["fact"] == "User likes Python"
    assert payload["scope"] == {"user_id": "abc"}


def test_format_conversation_events_maps_turns() -> None:
    events = formatters.format_conversation_events([
        {"role": "user", "content": "Hello"},
        {"role": "assistant", "content": "Hi"},
    ])
    assert events[0]["content"]["role"] == "user"
    assert events[1]["content"]["parts"][0]["text"] == "Hi"


def test_format_ttl_expiration_returns_future_timestamp() -> None:
    timestamp = formatters.format_ttl_expiration(60)
    assert timestamp.endswith("Z")
