# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Weaviate MCP (Model Context Protocol) server that provides AI tools for interacting with Weaviate vector databases. It's part of a larger mcp-servers project and follows a clean 3-file architecture:

- `main.go`: Application entry point with OpenTelemetry setup and environment configuration
- `weaviate.go`: Weaviate client wrapper with MCP tool implementations
- `mcp.go`: MCP server setup and tool registration
- `version.go`: Version management

## Development Commands

### Building and Running
```bash
# Build the server
go build -o weaviate-mcp .

# Run directly
go run .

# Run with environment variables loaded (recommended)
direnv allow  # first time only
go run .
```

### Code Quality
```bash
# Format code
go fmt ./...

# Vet code for issues  
go vet ./...

# Update dependencies
go mod tidy

# Update vendor directory
go mod vendor
```

### Testing
```bash
# Run tests (currently no tests exist - this is important to implement)
go test ./...

# Run tests with coverage
go test -cover ./...
```

## Environment Setup

The server requires several environment variables for Weaviate and AI service integration. Use `.envrc` with direnv for local development:

### Required Variables
- `WEAVIATE_URL`: Weaviate instance URL (without https://)
- `WEAVIATE_GRPC_URL`: Weaviate gRPC endpoint 
- `WEAVIATE_API_KEY`: Weaviate authentication token

### Optional AI Service Keys
- `HUGGINGFACE_API_KEY`: For HuggingFace model integration
- `VOYAGEAI_API_KEY`: For VoyageAI embeddings
- `COHERE_API_KEY`: For Cohere services
- `JINAAI_API_KEY`: For JinaAI services

Check `main.go:35-42` for the complete list of environment variables.

## Architecture Patterns

### MCP Tool Registration
Tools are registered in `mcp.go:56-80` using this pattern:
```go
tool := &mcp.Tool{
    Name:        "tool_name",
    Description: "Tool description",
}
mcp.AddTool(s.Server, tool, client.ToolMethod)
```

### Weaviate Client Structure
The `weaviateClient` struct in `weaviate.go:38` wraps the official Weaviate Go client with MCP-specific tool methods. All tool methods follow the signature:
```go
func (w *weaviateClient) ToolName(ctx context.Context, _ *mcp.CallToolRequest, args ArgsType) (*mcp.CallToolResult, ReturnType, error)
```

### Error Handling
- Connection failures are checked during client initialization (`weaviate.go:78-81`)
- Batch operation errors are aggregated using `errors.Join` (`weaviate.go:215-221`)
- MCP tool errors are returned as `CallToolResult` with error content, not protocol errors

## Key Implementation Details

### Available Tools
1. **get_schema**: Retrieves Weaviate schema configuration
2. **create_schema_class**: Creates a hardcoded "go" collection with HuggingFace vectorizer
3. **insert_one**: Inserts objects into collections using batch operations for efficiency
4. **query**: Performs hybrid search queries with configurable target properties

### Dependency Management
- Uses Go modules with vendor directory committed
- Key dependencies: Weaviate Go client v5, MCP Go SDK, OpenTelemetry
- See `go.mod:5-14` for primary dependencies

### OpenTelemetry Integration
- Tracing setup in `main.go:45-81` (currently commented out)
- HTTP transport instrumentation with `otelhttp.NewTransport`
- Service name: "weaviate-mcp"

## Development Guidelines

### Adding New Tools
1. Add tool method to `weaviateClient` struct in `weaviate.go`
2. Define input args struct with `jsonschema` tags if needed
3. Register tool in `mcp.go:AddTools` method
4. Follow existing error handling patterns

### Testing Strategy
Currently no tests exist. When implementing:
- Use standard Go testing framework
- Test each tool method with mock Weaviate responses
- Integration tests with test Weaviate instance
- Use `context.Context` properly in all tests

### Code Conventions
- Standard Go formatting with `go fmt`
- Environment variables as constants in `main.go`
- Structured logging would be beneficial (currently uses basic `log` package)
- JSON marshaling uses `encoding/json/v2` for better performance