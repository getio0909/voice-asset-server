# Third-Party Dependencies

Runtime dependencies are intentionally small and locked in `go.mod` and
`go.sum`.

| Dependency | Version | License | Purpose | Replacement |
| --- | --- | --- | --- | --- |
| `github.com/jackc/pgx/v5` | `v5.10.0` | MIT | PostgreSQL protocol, connection, and transactions | `database/sql` with a maintained PostgreSQL driver |
| `github.com/jackc/pgpassfile` | `v1.0.0` | MIT | Standard PostgreSQL password-file parsing used by pgx | Explicit connection configuration |
| `github.com/jackc/pgservicefile` | `v0.0.0-20240606120523-5a60cdf6a761` | MIT | PostgreSQL service-file parsing used by pgx | Explicit connection configuration |
| `golang.org/x/text` | `v0.29.0` | BSD-3-Clause | Text preparation used by pgx authentication | Maintained pgx-selected equivalent |

Transitive Go modules are resolved by `go mod tidy`; their exact versions and
checksums are authoritative in `go.sum`. CI verifies module checksums, scans Go
dependencies for vulnerabilities, and checks dependency licenses. Build and CI
container/action versions are pinned to immutable digests or commit SHAs in their
respective configuration files.
