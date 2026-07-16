# Compatibility Matrix

The matrix is updated only after the listed combination passes its applicable
contract and integration tests.

| Server | API | Contract | Console | Android | MCP | Site docs | Status |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `0.1.0-dev` | `v1` | `0.1.0` | `0.1.0` local verified | `0.1.0` CI pending | `0.1.0` local verified | `0.1.0` local verified | Phase 0 candidate |

## Policy

- A client records the exact contract version used for generation or testing.
- Additive OpenAPI changes preserve the API namespace and increment the contract
  minor version; corrections increment its patch version.
- Breaking wire changes require a new API namespace and an ADR.
- Clients must ignore unknown capability values and fail closed when a required
  capability or scope is absent.
- A row becomes supported only after cross-repository CI or a recorded release
  candidate run proves it.
