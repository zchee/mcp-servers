"""Client management utilities for Vertex AI Agent Engines."""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
import logging
from typing import Any, cast

import vertexai

from .concurrency import run_blocking
from .config import Config
from .engines import extract_agent_engine_name
from .errors import InitializationError


vertexai = cast(Any, vertexai)
VertexClient = Any


logger = logging.getLogger(__name__)

_NOT_INITIALIZED_MESSAGE = "Memory Bank not initialized. Call initialize_memory_bank first."


@dataclass(slots=True)
class VertexClientManager:
    """Owns the lifecycle of the Vertex AI client and agent engine state."""

    config: Config
    client: VertexClient | None = None
    agent_engine_name: str | None = None

    async def bootstrap(self) -> None:
        """Eagerly construct the client/agent engine if the config already has them."""
        if not self.config.has_project_configuration():
            logger.info("GOOGLE_CLOUD_PROJECT not set; waiting for initialize_memory_bank")
            return
        await self.ensure_client(self.config.project_id, self.config.location)
        if self.config.agent_engine_name:
            await self.ensure_agent_engine(existing_name=self.config.agent_engine_name)

    async def ensure_client(self, project_id: str, location: str) -> VertexClient:
        """Create or reuse a Vertex client for the provided project and location."""
        if (
            self.client is not None
            and self.config.project_id == project_id
            and self.config.location == location
        ):
            return self.client

        def _build() -> VertexClient:
            client_ctor = cast(Any, vertexai.Client)
            return client_ctor(project=project_id, location=location)

        self.client = await run_blocking(_build)
        self.config = Config(
            project_id=project_id,
            location=location,
            agent_engine_name=self.config.agent_engine_name,
            credentials_path=self.config.credentials_path,
        )
        logger.info("Vertex client refreshed for project %s", project_id)
        return self.client

    async def ensure_agent_engine(
        self,
        *,
        existing_name: str | None = None,
        memory_topics: Iterable[str] | None = None,
        force_new: bool = False,
    ) -> str:
        """Return an agent-engine resource name, creating one when necessary."""
        client = self.require_client()
        if existing_name and not force_new:
            logger.info("Using user-specified Agent Engine %s", existing_name)
            engine = await run_blocking(client.agent_engines.get, name=existing_name)
            name = extract_agent_engine_name(engine)
        elif self.agent_engine_name and not force_new:
            logger.info("Reusing cached Agent Engine %s", self.agent_engine_name)
            name = self.agent_engine_name
        else:
            creation_config = self._build_creation_config(memory_topics)
            logger.info("Creating new Agent Engine with Memory Bank enabled")
            engine = await run_blocking(client.agent_engines.create, config=creation_config)
            name = extract_agent_engine_name(engine)

        self.agent_engine_name = name
        self.config = self.config.copy_with_agent_engine(name)
        return name

    def require_client(self) -> VertexClient:
        """Return the cached client or raise an initialization error."""
        if self.client is None:
            raise InitializationError(_NOT_INITIALIZED_MESSAGE)
        return self.client

    def require_agent_engine_name(self) -> str:
        """Return the cached agent-engine resource name or fail eagerly."""
        if self.agent_engine_name is None:
            raise InitializationError(_NOT_INITIALIZED_MESSAGE)
        return self.agent_engine_name

    def require_ready(self) -> tuple[VertexClient, str]:
        """Convenience helper returning both the client and agent engine name."""
        return self.require_client(), self.require_agent_engine_name()

    def reset(self) -> None:
        """Clear cached state so a fresh configuration can be applied."""
        self.client = None
        self.agent_engine_name = None

    @staticmethod
    def _build_creation_config(memory_topics: Iterable[str] | None) -> dict[str, Any] | None:
        if not memory_topics:
            return None
        return {
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
