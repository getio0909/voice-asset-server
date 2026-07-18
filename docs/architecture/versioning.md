# Version and Compatibility Strategy

VoiceAsset uses independent Semantic Versioning for each of the five
repositories. During initial development versions remain `0.y.z`; compatibility
is explicit and may change between minor releases.

The Server publishes three distinct values:

- `server_version`: the binary release, for example `1.2.3`;
- `api_version`: the stable URL namespace, currently `v1`;
- `contract_version`: the OpenAPI document version, currently `0.22.0` locally.

Additive contract changes increment the contract minor version. Compatible
clarifications and fixes increment the patch version. A breaking wire change
requires a new `/api/vN` namespace, an ADR, a migration path, and a documented
support window. Server version changes do not imply a wire break.

Console, Android, MCP, and Site releases record the contract version used to
build or test them. At startup, clients call
`GET /api/v1/system/capabilities` and compare both `api_version` and required
feature identifiers. Unknown feature values are ignored; missing required
values disable only the affected client behavior. The maintained combinations
are recorded in `docs/program/COMPATIBILITY_MATRIX.md`.
