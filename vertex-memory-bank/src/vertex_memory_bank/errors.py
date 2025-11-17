"""Custom exception hierarchy for Vertex Memory Bank services."""

from __future__ import annotations


class MemoryBankError(RuntimeError):
    """Base exception for all Memory Bank failures."""


class ValidationError(MemoryBankError):
    """Raised when input payloads fail validation."""


class InitializationError(MemoryBankError):
    """Raised when the Vertex client or agent engine is unavailable."""


class VertexServiceError(MemoryBankError):
    """Raised when Vertex AI operations fail or return unexpected payloads."""
