# Contributing to VoiceAsset Server

## Before You Start

Open an issue before large behavioral or architectural changes. Security
reports must follow `SECURITY.md`, not a public issue. Keep pull requests scoped
to one repository and one coherent purpose.

## Development Workflow

1. Install the Go version declared in `go.mod`.
2. Run `go mod download`.
3. Make the smallest complete change and add tests.
4. Run `gofmt -w` on changed Go files.
5. Run `make test`, `make lint`, and `make build`.
6. Run `make contract` whenever the REST surface changes.

Use standard Go conventions: tabs as produced by `gofmt`, lowercase package
names, exported `PascalCase` identifiers, and error wrapping with `%w`.

## Contracts and Migrations

`contracts/openapi.yaml` is the sole REST source of truth. An API change must
update the implementation, OpenAPI, tests, compatibility matrix, and changelog
in the same pull request. Breaking changes require an ADR and a new API version.

Never edit an applied migration. Add the next zero-padded pair, for example
`000002_upload_sessions.up.sql` and `000002_upload_sessions.down.sql`. Migrations
must be transactional, deterministic, and safe on an empty PostgreSQL database.

## Commits and Pull Requests

Use Conventional Commits, such as `feat(api): expose storage capability` or
`fix(migrate): reject duplicate versions`. Pull requests must explain user
impact, link relevant issues, list exact validation commands, and call out API,
schema, security, or deployment implications. Do not commit secrets, local
`.env` files, generated binaries, real provider responses, or customer audio.
