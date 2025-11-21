# Python Port Plan

## Context & Constraints
- Goal: reimplement the Apple Docs MCP server in Python while correcting the defects identified in the TypeScript codebase (error reporting, cache/timer leaks, UA rotation, etc.).
- Transport stack: must use [`fastmcp`](https://github.com/jlowin/fastmcp) for tool registration and stdio wiring.
- Hosting/timeline: no hard platform or schedule requirements, but the deliverable must remain a CLI-friendly MCP server with the same MIT licensing posture.

## MVP Scope (Phase 1 Tooling)
Unless you override this list, the first Python drop will ship the four core documentation tools:
1. `search_apple_docs`
2. `get_apple_doc_content`
3. `list_technologies`
4. `search_framework_symbols`

These cover the full docs-discovery workflow and force us to exercise the HTTP client, caching, schema validation, and result formatting machinery. WWDC/video/sample-code tooling will land in later phases.

## Work Breakdown
1. **SDK Due Diligence**
   - Read fastmcp’s server API to understand how it maps tools, schemas, and streaming.
   - Confirm how it signals structured errors so we can wire the improved AppError responses.
2. **Project Scaffolding**
   - Create a Python package (likely `apple_docs_mcp`) with Poetry or Hatch, MIT license, and console entrypoint (`python -m apple_docs_mcp`).
   - Mirror the existing data directory layout (or document deviations) for bundled WWDC artifacts once later phases require them.
3. **Shared Infrastructure**
   - Implement an asyncio-friendly HTTP client using `httpx`, including:
     - Rotating User-Agent pool with per-attempt regeneration.
     - Rate limiter and retry/backoff logic.
     - Structured error translation that preserves Apple endpoint context.
   - Build cancellable TTL caches (no immortal `setInterval`) plus instrumentation hooks for future `get_cache_stats`.
   - Define logging, configuration, and graceful shutdown (SIGINT/SIGTERM) behavior.
4. **MVP Tool Ports**
   - Port each of the four selected tools, adapting schemas to fastmcp’s expectations and fixing known bugs (e.g., framework compare mode, cache-key mismatches).
   - Ensure handlers throw/return `AppError` objects so clients can distinguish failure vs success.
5. **Testing & Quality Gates**
   - Stand up pytest with recorded fixtures for Apple endpoints to avoid hammering prod.
   - Add unit tests for HTTP client, caches, and each tool handler.
   - Wire linting (ruff + mypy) and CI scripts so the package mirrors the TypeScript repo’s rigor.
6. **Packaging & Docs**
   - Publishable wheel/sdist, README instructions for installing via pip and configuring MCP hosts.
   - Migration notes comparing TypeScript vs Python behavior, including any intentionally fixed breaking changes (error metadata, timer cleanup).

## Future Phases (Post-MVP)
1. **Phase 2 – WWDC & Sample-Code Tools**
   - Port WWDC data loaders/handlers, determine packaging for large static datasets, and restore features like `search_wwdc_content`.
2. **Phase 3 – Operational Tooling**
   - Reintroduce cache/performance reports, rate-limiter telemetry, and any admin utilities.
3. **Phase 4 – Parity + Enhancements**
   - Close the gap with anything still missing from the TS server and consider new Python-native improvements (async streaming, richer caching backends, etc.).

## Risks & Mitigations
- **fastmcp maturity**: If the SDK lacks a feature (e.g., streaming or structured errors), we may need to submit upstream PRs or shim functionality locally.
- **Apple endpoint variability**: Use recorded fixtures and defensive parsing to avoid brittle failures when Apple tweaks JSON schemas.
- **Data packaging size**: WWDC bundle is large; may require optional download step or git-lfs alternative once we reach Phase 2.

## Immediate Next Steps
1. Inspect fastmcp code/docs and spike a tiny toy server to validate error semantics.
2. Spin up the Python package scaffold + tooling config.
3. Implement the shared HTTP/caching infrastructure, then port `search_apple_docs` and `get_apple_doc_content` first.
4. Expand to the remaining two MVP tools, round out tests, and iterate with you before tackling WWDC features.
