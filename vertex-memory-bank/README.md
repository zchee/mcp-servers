# Vertex Memory Bank MCP Server

A production-ready Model Context Protocol (MCP) server that exposes Vertex AI Memory Bank tools to MCP-compatible clients (Claude Desktop, devbox agents, etc.). The server boots quickly, fails fast when configuration is incomplete, and keeps blocking Google Cloud SDK calls off the asyncio event loop.

## Requirements

- Python 3.14+
- `uv` or `pip` for dependency management
- A Google Cloud project with the Vertex AI API enabled
- A service account or Application Default Credentials able to manage Vertex AI Agent Engines

## Installation

```bash
uv sync
# or
pip install -e .
```

## Configuration

Set the required environment variables (a `.env` file is supported via `python-dotenv`):

| Variable | Description |
| --- | --- |
| `GOOGLE_CLOUD_PROJECT` | Target Google Cloud project ID |
| `GOOGLE_CLOUD_LOCATION` | Vertex AI location (`us-central1` by default) |
| `AGENT_ENGINE_NAME` | *(optional)* Existing Agent Engine resource name |
| `GOOGLE_APPLICATION_CREDENTIALS` | *(optional)* Path to a service-account JSON key |

If you omit `AGENT_ENGINE_NAME`, call the `initialize_memory_bank` tool once to create or select the engine you want to reuse.

## Usage

```bash
uv run vertex-memory-bank
```

Add the server to Claude Desktop:

```json
{
  "mcpServers": {
    "vertex-memory-bank": {
      "command": "uv",
      "args": ["run", "vertex-memory-bank"],
      "env": {
        "GOOGLE_CLOUD_PROJECT": "your-project",
        "GOOGLE_CLOUD_LOCATION": "us-central1"
      }
    }
  }
}
```

## Available MCP Tools

| Tool | Purpose |
| --- | --- |
| `initialize_memory_bank` | Create or reuse an Agent Engine with Memory Bank enabled |
| `generate_memories` | Convert a chat transcript into durable memories |
| `retrieve_memories` | Fetch stored memories, optionally via similarity search |
| `create_memory` | Manually author a memory (with optional TTL) |
| `delete_memory` | Remove a memory by resource name |
| `list_memories` | Enumerate all memories for the configured engine |

Each tool validates its inputs up front and emits structured success/error responses that MCP clients can render directly.

## Development

```bash
uv run ruff format .
uv run ruff check .
uv run mypy src
uv run pyright
uv run pytest
```

The repository follows a strict-src layout; all source lives under `src/vertex_memory_bank/`. Tests belong in `tests/`.
