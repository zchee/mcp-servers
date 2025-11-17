"""Vertex Memory Bank MCP server package."""

from . import clients, errors, formatters, services, validators
from .server import run


def main() -> None:
    """Entry point for the `vertex-memory-bank` console script."""
    run()


__all__ = [
    "clients",
    "errors",
    "formatters",
    "main",
    "run",
    "services",
    "validators",
]
