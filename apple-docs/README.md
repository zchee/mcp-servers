# Apple Developer Documentation MCP Server

An MCP server that provides access to Apple Developer Documentation.

## Features

- **Search**: Search for APIs, frameworks, and guides.
- **Documentation**: Get detailed documentation for specific APIs.
- **Technologies**: Browse available technologies and frameworks.
- **Framework Symbols**: Search for symbols (classes, methods, etc.) within a framework.

## Tools

- `search`: Search Apple Developer Documentation.
- `get_content`: Get detailed content from a documentation URL.
- `list_tech`: List available technologies.
- `search_framework`: Search symbols within a framework.
- `list_wwdc_videos`: List WWDC videos with filtering.
- `search_wwdc_content`: Search within WWDC video transcripts and code.
- `get_wwdc_video`: Get details for a specific WWDC video.
- `get_wwdc_code_examples`: Extract code examples from WWDC videos.
- `browse_wwdc_topics`: Browse WWDC topics.

## Usage

Run with `mcp`:

```bash
mcp run src/apple_docs/server.py
```

Or install and run:

```bash
pip install .
apple-docs
```
