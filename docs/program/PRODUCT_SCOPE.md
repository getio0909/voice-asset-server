# VoiceAsset v1 Product Scope

## Outcome

VoiceAsset v1 is a self-hosted voice digital-asset platform that records or
imports audio, preserves immutable originals, produces reviewable transcript
revisions, supports offline Android synchronization, and exposes scoped Agent
access through MCP. A deployment remains demonstrable with Mock ASR and Mock
LLM providers and no vendor credentials.

## In Scope

- Go API and worker with PostgreSQL metadata and durable jobs
- local filesystem and S3-compatible object storage
- resumable verified uploads and immutable media lineage
- Mock, Alibaba Cloud, and Tencent Cloud ASR adapters
- independent ASR hotwords and LLM correction glossaries
- OpenAI-compatible and Mock LLM correction with structured patches
- authentication, RBAC, token scopes, audit, export, backup, and restore
- Vue management Console and offline-first native Android application
- Go MCP server over stdio and Streamable HTTP using only public APIs
- bilingual static product, deployment, API, and developer documentation

## Out of Scope for v1

iOS, telephone recording, arbitrary SSH or shell execution, SaaS billing,
multi-region active-active, Kubernetes operators, Kafka, service mesh, custom
foundation models, video editing, social networking, and unrestricted plugins.

## Product Principles

Source audio and raw ASR are immutable. Derived work has explicit lineage.
Provider secrets never leave Server. Expensive work is asynchronous. Clients
negotiate the public contract instead of inferring server internals. Planned
features are never presented as implemented.
