# AGENTS.md

Project: Pulse

Pulse is a programmable reliability and load testing engine written in Go.

## Product direction
- Library-first, CLI-second
- MVP first, no non-MVP features
- Code-first API in Go
- Arrival-rate execution model
- Bounded concurrency
- Central metrics aggregator
- JSON output in MVP
- HTTP transport will exist, but only after core engine foundations

## Current repository status
- Repository already created
- Initial package structure already exists
- Design document already exists under docs/
- This phase is focused on building the public API skeleton only

## Rules
- Keep the public API minimal and stable
- Use idiomatic Go
- Prefer simple, explicit, testable code
- Do not over-engineer
- Do not add unnecessary abstractions
- Do not add external dependencies unless clearly justified
- Do not implement non-MVP features
- Do not add chaos engineering features yet
- Do not add web UI
- Do not implement HTTP transport behavior yet
- Do not implement scheduler internals yet

## Package boundaries
- The root package `pulse` is the public API
- `internal/` contains private implementation details
- Keep package responsibilities clean
- Avoid circular dependencies

## What to optimize for
- Clarity
- Testability
- Small diffs
- Clean public API