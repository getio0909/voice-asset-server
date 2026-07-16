# VoiceAsset Server

VoiceAsset Server is the API, worker, migration, and administration foundation
for the self-hosted VoiceAsset platform. The repository is currently at
**Phase 0**: governance, public contracts, process lifecycle, and the initial
PostgreSQL schema are implemented. Asset upload and transcription are not yet
advertised as available.

## Implemented Surface

- `GET /healthz`, `GET /livez`, and `GET /readyz`
- `GET /api/v1/system/capabilities`
- structured JSON errors, request IDs, and version headers
- checksum-protected, transactional PostgreSQL migrations
- API, worker, migration, and `adminctl` binaries
- hardened multi-stage container and local PostgreSQL Compose stack

The REST source of truth is [`contracts/openapi.yaml`](contracts/openapi.yaml).

## Requirements

- Go 1.26.5 or newer in the 1.26 release line
- GNU Make (optional convenience commands)
- Node.js 24+ only for OpenAPI linting
- Docker Compose only for the containerized stack

## Local Development

```bash
go mod download
make test
make dev
```

The API listens on `http://localhost:8080`. Check it with:

```bash
curl http://localhost:8080/api/v1/system/capabilities
```

Validate migration ordering and checksums without PostgreSQL:

```bash
go run ./cmd/migrate -dry-run
```

To run PostgreSQL, migrations, API, and worker together:

```bash
docker compose -f deploy/compose/compose.yaml up --build
```

The Compose password is a documented local-only value. Set a strong
`POSTGRES_PASSWORD` before using the stack outside an isolated workstation.

## Commands

- `make test` runs all Go tests.
- `make lint` runs `go vet`.
- `make build` compiles every command.
- `make contract` lints the OpenAPI 3.1 contract.
- `make migrate` applies migrations using `DATABASE_URL`.
- `go run ./cmd/adminctl -- capabilities` prints the offline capability model.

## Architecture and Governance

Start with [`docs/architecture/README.md`](docs/architecture/README.md), then
read the [domain model](docs/architecture/domain-model.md),
[version strategy](docs/architecture/versioning.md), and accepted
[architecture decisions](docs/adr/). Cross-repository delivery state lives in
[`docs/program/PROGRAM_STATUS.md`](docs/program/PROGRAM_STATUS.md).

## License

Copyright 2026 VoiceAsset contributors. Licensed under
`AGPL-3.0-or-later`; see [`LICENSE`](LICENSE).
