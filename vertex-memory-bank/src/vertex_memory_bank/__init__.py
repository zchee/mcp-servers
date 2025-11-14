"""Vertex Memory Bank MCP server package."""

from . import formatters, validators
from .server import run


def main() -> None:
    """Entry point for the `vertex-memory-bank` console script."""
    run()


__all__ = ["formatters", "main", "run", "validators"]
