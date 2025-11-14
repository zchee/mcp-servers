"""Runtime application state management."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import TYPE_CHECKING

from .config import Config


if TYPE_CHECKING:
    from vertexai import Client


@dataclass
class AppState:
    """Holds mutable context shared by MCP tools and server lifecycle."""

    config: Config = field(default_factory=Config)
    client: Client | None = None
    agent_engine_name: str | None = None

    def is_ready(self) -> bool:
        """Return True when both the Vertex client and agent engine are available."""
        return self.client is not None and self.agent_engine_name is not None

    def reset(self) -> None:
        """Clear cached clients and agent state."""
        self.client = None
        self.agent_engine_name = None


app_state = AppState()
"""Singleton-style state container bound to the running process."""
