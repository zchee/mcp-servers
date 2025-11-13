# SPDX-FileCopyrightText: 2025 mcp-servers Authors
# SPDX-License-Identifier: Apache-2.0

from __future__ import annotations

import asyncio
import logging
import os

import orjson
import uvicorn
from dotenv import load_dotenv
from mcp.server.fastmcp import FastMCP
from mem0 import AsyncMemoryClient

# Load environment variables
# If MEM0_DOTENV_PATH is provided, use it; otherwise, auto-discover .env
_dotenv_path = os.getenv("MEM0_DOTENV_PATH")
if _dotenv_path:
    load_dotenv(dotenv_path=_dotenv_path)
else:
    load_dotenv()

# Initialize FastMCP server for mem0 tools
mcp = FastMCP("mem0")

# Lazy Mem0 client initialization (avoid import-time failures)
_mem0_client: AsyncMemoryClient | None = None
_mem0_client_lock = asyncio.Lock()
_project_configured = False
_project_config_lock = asyncio.Lock()

DEFAULT_AGENT_ID = "mem0-mcp"
CUSTOM_INSTRUCTIONS = """
Extract the Following Information:

- Code Snippets: Save the actual code for future reference.
- Explanation: Document a clear description of what the code does and how it works.
- Related Technical Details: Include information about the programming language, dependencies, and system specifications.
- Key Features: Highlight the main functionalities and important aspects of the snippet.
"""


async def _get_mem0_client() -> AsyncMemoryClient:
    """Return a lazily-initialized Mem0 async client.

    Avoids creating the client at import-time which requires MEM0_API_KEY.
    Raises a clear error if the environment isn't configured when used.
    """
    global _mem0_client
    if _mem0_client is not None:
        return _mem0_client
    async with _mem0_client_lock:
        if _mem0_client is None:
            try:
                _mem0_client = AsyncMemoryClient(
                    org_id=os.getenv("MEM0_ORG_ID", None),
                    project_id=os.getenv("MEM0_PROJECT_ID", None),
                )
            except Exception as e:  # surface a helpful message
                raise RuntimeError(
                    "Mem0 client not configured. Ensure MEM0_API_KEY (and optional MEM0_ORG_ID/MEM0_PROJECT_ID) are set."
                ) from e
        return _mem0_client


async def _ensure_project_config() -> None:
    """Apply one-time project configuration for Mem0 (idempotent)."""
    global _project_configured
    if _project_configured:
        return
    async with _project_config_lock:
        if _project_configured:
            return
        client = await _get_mem0_client()
        await client.project.update(custom_instructions=CUSTOM_INSTRUCTIONS, enable_graph=True)
        _project_configured = True


@mcp.tool(
    description="""Add a new coding preference to mem0. This tool stores code snippets, implementation details,
    and coding patterns for future reference. Store every code snippet. When storing code, you should include:
    - Complete code with all necessary imports and dependencies
    - Language/framework version information (e.g., "Python 3.9", "React 18")
    - Full implementation context and any required setup/configuration
    - Detailed comments explaining the logic, especially for complex sections
    - Example usage or test cases demonstrating the code
    - Any known limitations, edge cases, or performance considerations
    - Related patterns or alternative approaches
    - Links to relevant documentation or resources
    - Environment setup requirements (if applicable)
    - Error handling and debugging tips
    The preference will be indexed for semantic search and can be retrieved later using natural language queries."""
)
async def add_coding_preference(text: str) -> dict[str, str]:
    """Add a new coding preference to mem0.

    This tool is designed to store code snippets, implementation patterns, and programming knowledge.
    When storing code, it's recommended to include:
    - Complete code with imports and dependencies
    - Language/framework information
    - Setup instructions if needed
    - Documentation and comments
    - Example usage

    Args:
        text: The content to store in memory, including code, documentation, and context
    """
    try:
        await _ensure_project_config()
        client = await _get_mem0_client()
        messages = [{"role": "user", "content": text}]
        await client.add(
            messages,
            version="v2",
            agent_id=DEFAULT_AGENT_ID,
            output_format="v1.1",
        )
        return {"status": "ok", "message": "Preference added"}
    except Exception as e:
        return {"status": "error", "error": str(e)}


@mcp.tool(
    description="""Retrieve all stored coding preferences for the default user. Call this tool when you need
    complete context of all previously stored preferences. This is useful when:
    - You need to analyze all available code patterns
    - You want to check all stored implementation examples
    - You need to review the full history of stored solutions
    - You want to ensure no relevant information is missed
    Returns a comprehensive list of:
    - Code snippets and implementation patterns
    - Programming knowledge and best practices
    - Technical documentation and examples
    - Setup and configuration guides
    Results are returned in JSON format with metadata."""
)
async def get_all_coding_preferences() -> str:
    """Get all coding preferences for the default user.

    Returns a JSON formatted list of all stored preferences, including:
    - Code implementations and patterns
    - Technical documentation
    - Programming best practices
    - Setup guides and examples
    Each preference includes metadata about when it was created and its content type.
    """
    await _ensure_project_config()
    try:
        client = await _get_mem0_client()
        memories = await client.get_all(
            version="v2",
            page=1,
            filters={"AND": [{"agent_id": DEFAULT_AGENT_ID}]},
        )
        return orjson.dumps(list(memories)).decode("utf-8")
    except Exception as e:
        return f"Error getting preferences: {str(e)}"


@mcp.tool(
    description="""Search through stored coding preferences using semantic search. This tool should be called
    for EVERY user query to find relevant code and implementation details. It helps find:
    - Specific code implementations or patterns
    - Solutions to programming problems
    - Best practices and coding standards
    - Setup and configuration guides
    - Technical documentation and examples
    The search uses natural language understanding to find relevant matches, so you can
    describe what you're looking for in plain English. Always search the preferences before
    providing answers to ensure you leverage existing knowledge."""
)
async def search_coding_preferences(query: str) -> str:
    """Search coding preferences using semantic search.

    The search is powered by natural language understanding, allowing you to find:
    - Code implementations and patterns
    - Programming solutions and techniques
    - Technical documentation and guides
    - Best practices and standards
    Results are ranked by relevance to your query.

    Args:
        query: Search query string describing what you're looking for. Can be natural language
              or specific technical terms.
    """
    await _ensure_project_config()
    try:
        client = await _get_mem0_client()
        memories = await client.search(
            query,
            agent_id=DEFAULT_AGENT_ID,
            version="v2",
            output_format="v1.1",
            filters={"AND": [{"agent_id": DEFAULT_AGENT_ID}]},
        )
        return orjson.dumps(list(memories)).decode("utf-8")
    except Exception as e:
        return f"Error getting preferences: {str(e)}"


def main() -> None:
    """CLI entrypoint for running the Mem0 MCP server.

    Provides two transports:
    - stdio (default): for MCP clients
    - HTTP/SSE (--http): for web/HTTP clients
    """
    import argparse

    logging.basicConfig(level=logging.DEBUG)

    parser = argparse.ArgumentParser(description="Run a Mem0 MCP server")

    parser.add_argument(
        "--http",
        action="store_true",
        help="Run the server with Streamable HTTP and SSE transport rather than STDIO (default: False)",
    )
    parser.add_argument("--host", default=None, help="Host to bind to (default: 127.0.0.1)")
    parser.add_argument("--port", type=int, default=None, help="Port to listen on (default: 3001)")
    args = parser.parse_args()

    use_http = args.http

    if not use_http and (args.host or args.port):
        parser.error("Host and port arguments are only valid when using streamable HTTP transport (see: --http).")

    match use_http:
        case True:
            # Use FastMCP's built-in Streamable HTTP app
            starlette_app = mcp.streamable_http_app()
            uvicorn.run(
                starlette_app,
                host=args.host if args.host else "127.0.0.1",
                port=args.port if args.port else 3001,
            )
        case _:
            mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
