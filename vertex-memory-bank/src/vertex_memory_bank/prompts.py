"""Pre-built MCP prompts for common memory patterns."""

from __future__ import annotations

from mcp.server.fastmcp import FastMCP


def register_prompts(server: FastMCP) -> None:
    """Attach prompt endpoints to the MCP server."""

    @server.prompt()
    async def memory_extraction(conversation: str) -> str:
        """Prompt template for extracting durable memories from chat transcripts."""
        return f"""Analyze this conversation and extract memorable facts.\n\n{conversation}\n\nFocus on:\n1. Personal details (names, preferences, relationships)\n2. Explicit commitments or plans\n3. Important factual statements\n4. Items the user explicitly asked the assistant to remember\n\nReturn bullet-point facts that stand alone without the chat history."""

    @server.prompt()
    async def memory_search(user_query: str) -> str:
        """Prompt template that converts a question into search keywords."""
        return f"""Rewrite the following question into a concise set of search keywords suitable for similarity search over stored memories. Avoid filler words and keep only the critical nouns, verbs, and entities.\n\nQuestion: {user_query}"""

    @server.prompt()
    async def memory_consolidation(existing_memories: str, new_fact: str) -> str:
        """Prompt for reconciling a new fact with existing memories."""
        return f"""Existing memories:\n{existing_memories}\n\nNew fact:\n{new_fact}\n\nDecide whether the new fact should (a) create a new memory, (b) update an existing memory, (c) replace a conflicting memory, or (d) be ignored as redundant. Explain your reasoning."""
