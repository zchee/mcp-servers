# SPDX-FileCopyrightText: 2025 mcp-servers Authors
# SPDX-License-Identifier: Apache-2.0

import os
import contextlib
import orjson
from collections.abc import AsyncIterator

import uvicorn
from dotenv import load_dotenv
from mcp.server import Server
from mcp.server.fastmcp import FastMCP
from mcp.server.streamable_http_manager import StreamableHTTPSessionManager
from mem0 import MemoryClient
from starlette.applications import Starlette
from starlette.routing import Mount
from starlette.types import Receive, Scope, Send

load_dotenv()

# Initialize FastMCP server for mem0 tools
mcp = FastMCP("mem0")

# Initialize mem0 client and set default user
mem0_client = MemoryClient(
    org_id=os.getenv("MEM0_ORG_ID", None),
    project_id=os.getenv("MEM0_PROJECT_ID", None),
)
DEFAULT_AGENT_ID = "mem0-mcp"
CUSTOM_INSTRUCTIONS = """
Extract the Following Information:

- Code Snippets: Save the actual code for future reference.
- Explanation: Document a clear description of what the code does and how it works.
- Related Technical Details: Include information about the programming language, dependencies, and system specifications.
- Key Features: Highlight the main functionalities and important aspects of the snippet.
"""
mem0_client.project.update(custom_instructions=CUSTOM_INSTRUCTIONS, enable_graph=True)


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
async def add_coding_preference(text: str) -> str:
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
        messages = [{"role": "user", "content": text}]
        mem0_client.add(messages, version="v2", agent_id=DEFAULT_AGENT_ID, output_format="v1.1")
        return f"Successfully added preference: {text}"
    except Exception as e:
        return f"Error adding preference: {str(e)}"


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
    try:
        memories = mem0_client.get_all(version="v2", agent_id=DEFAULT_AGENT_ID, page=1, page_size=50)
        flattened_memories = [memory["memory"] for memory in memories]
        return orjson.dumps(flattened_memories).decode("utf-8")
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
    try:
        memories = mem0_client.search(query, agent_id=DEFAULT_AGENT_ID, output_format="v1.1")
        flattened_memories = [memory["memory"] for memory in memories]
        return orjson.dumps(flattened_memories).decode("utf-8")
    except Exception as e:
        return f"Error searching preferences: {str(e)}"


def create_starlette_app(mcp_server: Server, *, debug: bool = False) -> Starlette:
    session_manager = StreamableHTTPSessionManager(
        app=mcp_server,
        event_store=None,
        json_response=True,
        stateless=True,
    )

    async def handle_streamable_http(scope: Scope, receive: Receive, send: Send) -> None:
        await session_manager.handle_request(scope, receive, send)

    @contextlib.asynccontextmanager
    async def lifespan(_: Starlette) -> AsyncIterator[None]:
        """Context manager for session manager."""
        async with session_manager.run():
            print("Application started with StreamableHTTP session manager")
            try:
                yield
            finally:
                print("Application shutting down")

    return Starlette(
        debug=debug,
        routes=[
            Mount("/mcp", app=handle_streamable_http),
        ],
        lifespan=lifespan,
    )


# Main entry point
def main():
    import argparse

    mcp_server = mcp._mcp_server

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
            starlette_app = create_starlette_app(mcp_server, debug=True)
            uvicorn.run(
                starlette_app,
                host=args.host if args.host else "127.0.0.1",
                port=args.port if args.port else 3001,
            )
        case _:
            mcp.run(transport="stdio")


if __name__ == "__main__":
    main()
