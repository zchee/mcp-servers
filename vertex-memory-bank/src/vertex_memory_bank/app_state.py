"""Runtime application state management."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import TYPE_CHECKING

from .config import Config


if TYPE_CHECKING:
    from .clients import VertexClientManager
    from .services import MemoryBankService


@dataclass
class AppState:
    """Holds mutable context shared by MCP tools and server lifecycle."""

    config: Config = field(default_factory=Config)
    client_manager: VertexClientManager | None = None
    memory_service: MemoryBankService | None = None

    def is_ready(self) -> bool:
        """Return True when both the Vertex client and agent engine are available."""
        if self.client_manager is None:
            return False
        return (
            self.client_manager.client is not None
            and self.client_manager.agent_engine_name is not None
        )

    def reset(self) -> None:
        """Clear cached clients and agent state."""
        if self.client_manager is not None:
            self.client_manager.reset()
        self.config = Config()
        self.client_manager = None
        self.memory_service = None

    def attach(
        self,
        *,
        config: Config,
        client_manager: VertexClientManager,
        memory_service: MemoryBankService,
    ) -> None:
        """Store initialized collaborators for later retrieval."""
        self.config = config
        self.client_manager = client_manager
        self.memory_service = memory_service


app_state = AppState()
"""Singleton-style state container bound to the running process."""
