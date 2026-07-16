# ADR 0001: Modular Monolith and Contract-First Clients

- Status: Accepted
- Date: 2026-07-16

## Context

VoiceAsset has API, worker, Console, Android, MCP, and documentation surfaces,
but its initial team and deployment target are a single self-hosted machine.
Premature service boundaries would multiply consistency, deployment, and
observability work before domain boundaries are proven.

## Decision

The Server is one Go module organized by domain modules. API and worker are
separate processes built from the same source and migrations. PostgreSQL is the
metadata and job authority; audio lives in a storage adapter. Console, Android,
and MCP access the Server only through the versioned public contract in
`contracts/openapi.yaml`.

## Consequences

Transactions and refactoring remain local, and Docker Compose stays sufficient
for v1.0. Domain packages must avoid accidental coupling. If an independently
scaled boundary becomes necessary, it requires evidence, a new ADR, and a
compatible public migration path. Kafka, Kubernetes, and a service mesh are not
introduced for v1.0.
