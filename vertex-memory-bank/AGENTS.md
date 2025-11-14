# AGENTS.md - Contributor Guide

## Overview

`vertex-memory-bank` is a Model Context Protocol (MCP) server implementation for Vertex AI memory banking. This project is part of the `mcp-servers` monorepo and follows strict Python development standards.

## Project Structure

```
vertex-memory-bank/
├── src/
│   └── vertex_memory_bank/     # Main package (snake_case naming)
│       └── __init__.py         # Entry point with main() function
├── pyproject.toml              # Project configuration and dependencies
├── .python-version             # Python version specification (3.14)
└── README.md                   # Project documentation
```

Uses **src-layout** to separate source code from project root, preventing accidental imports from development directory.

## Development Setup

**Requirements**: Python 3.14+

Install [uv](https://github.com/astral-sh/uv) package manager:
```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

Install dependencies and sync environment:
```bash
uv sync --dev
```

## Build, Test & Development Commands

```bash
# Run the application
uv run vertex-memory-bank

# Run tests (pytest)
uv run pytest

# Run tests with coverage
uv run pytest --cov=vertex_memory_bank

# Lint and format code (Ruff)
uv run ruff check --fix .
uv run ruff format .

# Type check (MyPy)
uv run mypy src

# Type check (Pyright)
uv run pyright src
```

## Code Quality Standards

### Linting & Formatting
- **Tool**: Ruff (configured in `pyproject.toml`)
- **Line length**: 120 characters
- **Target**: Python 3.14
- **Rules**: Extensive linting enabled (flake8-bugbear, pycodestyle, pydocstyle, pyupgrade, isort, etc.)
- **Auto-fixes**: Enabled with `unsafe-fixes = true`

### Type Checking
- **Tools**: MyPy and Pyright (both in **strict mode**)
- **Requirement**: All code must pass strict type checking
- **Annotations**: Type hints required for all functions and methods

## Testing Guidelines

### Framework
- **Tool**: pytest
- **Location**: Tests should be placed in `tests/` directory (to be created)
- **Naming**: Test files must start with `test_` (e.g., `test_memory_bank.py`)
- **Coverage**: Aim for comprehensive test coverage

### Test Structure
```python
def test_feature_name():
    """Test description."""
    # Arrange
    # Act
    # Assert
```

### Running Tests
```bash
# Run all tests
uv run pytest -v

# Run specific test file
uv run pytest tests/test_memory_bank.py
```

## Coding Style & Naming Conventions

### Python Conventions
- **Style Guide**: PEP 8 compliance enforced by Ruff
- **Naming**:
  - Modules/packages: `snake_case` (e.g., `vertex_memory_bank`)
  - Classes: `PascalCase` (e.g., `MemoryBank`)
  - Functions/methods: `snake_case` (e.g., `get_memory`)
  - Constants: `UPPER_SNAKE_CASE` (e.g., `MAX_RETRIES`)
- **Docstrings**: Required for all public classes, methods, and functions (enforced by D101, D102, D103)
- **Imports**: Sorted by isort rules (forced sorting, 2 lines after imports)

### Code Organization
- Keep functions focused and single-purpose
- Prefer composition over inheritance
- Use type hints consistently
- Document complex logic with comments

## Commit & Pull Request Guidelines

### Commit Message Format
Follow the established pattern from repository history:
```
<component>: <lowercase description>

Examples:
vertex-memory-bank: add memory storage functionality
vertex-memory-bank: fix type annotations
vertex-memory-bank: update dependencies
```

### Commit Best Practices
- **Scope**: Keep commits focused on single changes
- **Description**: Use imperative mood ("add" not "added")
- **Case**: Lowercase descriptions
- **Length**: Keep first line under 72 characters

### Pull Request Requirements
1. **Description**: Clearly explain the problem and solution
2. **Testing**: All tests must pass (`uv run pytest -v`)
3. **Linting**: Code must pass Ruff checks (`uv run ruff check .`)
4. **Type checking**: Must pass both MyPy and Pyright strict mode
5. **Formatting**: Code must be formatted (`uv run ruff check --fix .` and `uv run ruff format .`)
6. **Documentation**: Update docstrings and README if needed

### Pre-Submission Checklist
Before submitting a PR, run:
```bash
# Format code
uv run ruff check --fix .
uv run ruff format .

# Check linting
uv run ruff check .

# Type check with both tools
uv run mypy src
uv run pyright

# Run tests
uv run pytest -v
```

## Monorepo Context

This project is part of the `mcp-servers` monorepo containing multiple MCP server implementations (`mem0`, `weaviate`, `sequential-thinking-server`, etc.). When making changes:

- Keep changes isolated to `vertex-memory-bank/` directory
- Don't modify parent directory files without explicit need
- Be aware of shared tooling in parent `.github/` directory

## Security & Configuration

### Secrets Management
- Never commit API keys, tokens, or credentials
- Use environment variables for sensitive configuration
- Add credential files to `.gitignore`

### Configuration
- Project configuration centralized in `pyproject.toml`
- Python version specified in `.python-version`
- Git-based versioning via `uv-dynamic-versioning`

## Additional Resources

- **Python 3.14**: Latest stable Python features
- **uv docs**: https://github.com/astral-sh/uv
- **Ruff docs**: https://docs.astral.sh/ruff/
- **MCP Protocol**: https://modelcontextprotocol.io/

## Questions?

For project-specific questions, refer to the repository's issue tracker or contact the maintainer listed in `pyproject.toml`.
