# Repository Guidelines

## Architecture

This repository is the VoiceAsset modular monolith. Keep executable wiring in
`cmd/`, domain and platform code in `internal/`, migrations in `migrations/`,
and the only public REST contract in `contracts/openapi.yaml`. API and Worker
are separate processes over the same domain model; do not introduce a v1
microservice boundary.

## Commands

- `make test`: run the Go test suite.
- `make lint`: run static checks.
- `make contract`: lint OpenAPI 3.1.
- `make build`: build all Server commands.

Run the smallest relevant test first, then all four checks before opening a
pull request. Use `gofmt`, lowercase packages, explicit errors, and table-driven
tests. Every API behavior change must update implementation, OpenAPI, and tests
in one change. Every schema change needs an ordered reversible migration.

Use Conventional Commits such as `feat(upload): add resumable session`. Never
commit provider secrets, master keys, real transcripts, audio, or local `.env`
files. Preserve original audio, raw provider responses, transcript revisions,
and audit records as immutable data.
