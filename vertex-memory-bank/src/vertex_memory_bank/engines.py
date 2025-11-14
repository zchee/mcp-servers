"""Agent Engine helper utilities."""

from __future__ import annotations

from typing import Any


def extract_agent_engine_name(engine: Any) -> str:
    """Return the fully qualified resource name for an Agent Engine."""
    candidates = [engine]
    for attr in ("api_resource", "result", "_pb"):
        nested = getattr(engine, attr, None)
        if nested is not None:
            candidates.append(nested)
    for candidate in candidates:
        name = getattr(candidate, "name", None)
        if name:
            return str(name)
    raise ValueError("Unable to determine agent engine resource name")
