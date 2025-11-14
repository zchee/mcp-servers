"""Configuration helpers for the Vertex Memory Bank MCP server."""

from __future__ import annotations

import os
from pathlib import Path

from dotenv import load_dotenv
from pydantic import BaseModel, Field


load_dotenv()


class Config(BaseModel):
    """Parsed configuration derived from environment variables."""

    project_id: str = Field(default="", description="Google Cloud project ID")
    location: str = Field(default="us-central1", description="Vertex AI location")
    agent_engine_name: str | None = Field(
        default=None,
        description="Existing Agent Engine resource name",
    )
    credentials_path: Path | None = Field(
        default=None,
        description="Path to service-account JSON credentials",
    )

    @classmethod
    def from_env(cls) -> Config:
        """Create a Config instance from process environment variables."""
        credentials = os.getenv("GOOGLE_APPLICATION_CREDENTIALS")
        return cls(
            project_id=os.getenv("GOOGLE_CLOUD_PROJECT", ""),
            location=os.getenv("GOOGLE_CLOUD_LOCATION", "us-central1"),
            agent_engine_name=os.getenv("AGENT_ENGINE_NAME"),
            credentials_path=Path(credentials) if credentials else None,
        )

    def has_project_configuration(self) -> bool:
        """Return True when the configuration is sufficient to create a Vertex client."""
        return bool(self.project_id)

    def copy_with_agent_engine(self, agent_engine_name: str) -> Config:
        """Return a new Config with the given agent engine resource name."""
        data = self.model_dump()
        data["agent_engine_name"] = agent_engine_name
        return Config(**data)
