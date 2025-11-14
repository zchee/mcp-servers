"""Unit tests for validator helpers."""

from vertex_memory_bank import validators


def test_validate_scope_accepts_string_pairs() -> None:
    assert validators.validate_scope({"user_id": "abc"}) is None


def test_validate_scope_rejects_non_dict() -> None:
    assert validators.validate_scope([]) == "Scope must be a dictionary"


def test_validate_conversation_enforces_roles() -> None:
    assert (
        validators.validate_conversation([
            {"role": "user", "content": "hi"},
            {"role": "assistant", "content": "hey"},
        ])
        is None
    )
    assert "invalid role" in (validators.validate_conversation([{"role": "foo", "content": "bad"}]) or "")


def test_validate_memory_fact_bounds() -> None:
    assert validators.validate_memory_fact("ok") is None
    assert validators.validate_memory_fact("") == "Fact cannot be empty"
    long_fact = "x" * 10_001
    assert validators.validate_memory_fact(long_fact) == "Fact exceeds 10k character limit"


def test_validate_memory_topics_checks_iterables() -> None:
    assert validators.validate_memory_topics(["USER_PREFERENCES"]) is None
    assert validators.validate_memory_topics([""]) == "memory_topics entries must be non-empty strings"


def test_validate_top_k_bounds() -> None:
    assert validators.validate_top_k(5) is None
    assert validators.validate_top_k(0) == "top_k must be positive"
