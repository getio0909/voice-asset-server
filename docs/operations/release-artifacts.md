# Release Artifact Validation

Server, MCP, Console, Android, and Site release workflows delegate to repository-local
Bash scripts so build, checksum, and verification steps can be reproduced before
a Tag run. Builders require an empty output directory and use a guarded temporary
staging directory. Verifiers reject unsafe paths, links, unexpected contents,
wrong contract pins or Go targets, missing embedded versions, and incomplete
hashes.

## Local Validation Record

### Current merged-tree repeatability (2026-07-19)

The current default branches were validated again after the coordinated merges.
Server `19e1db8` and MCP `8d34906` each produced all six cross-platform archives
twice with `v0.0.0-local.20260719`; every paired SHA-256 value matched, and
both repository `verify-release.sh` checks passed. Console `ed5b7f8` and Site
`7c0a665` rebuilt their static bundles twice at `v0.1.0`; the normalized
archives matched byte-for-byte, and both license/checksum verifiers passed.
Android `main` rebuilt the unsigned `v0.1.0` APK and AAB twice with
`clean assembleRelease bundleRelease`; both hashes matched. The Android
candidate remains explicitly unsigned because no local release keystore is
available, so external signing and physical-device acceptance remain open.
Temporary comparison outputs were removed after verification.

On 2026-07-17, each repository was built twice with synthetic version
`v0.0.0-local.20260717`. Both runs produced identical SHA-256 values for every
archive. Server embedded baseline revision
`93d24228976f1bdd7ec0a8ae981cd25c549a091a`; MCP's baseline was
`5df2f92dd0b828c8383ad6fc1288d892993954aa`.

The source trees contained the uncommitted coordinated slices, so this proves
local script determinism and package validation, not Tag provenance or a
publishable release. All transient archives were removed after verification.

| Server target | SHA-256                                                            |
| ------------- | ------------------------------------------------------------------ |
| darwin/amd64  | `6a2587bfc9655fd27e7acca15dd87bca3cfaa506f4ad954da0eb1f5ed7ca3182` |
| darwin/arm64  | `0c57291875be5676ade3a2f7f47c81370e4815eb3b1339c2bb531ddd86c92af9` |
| linux/amd64   | `eaeb98abe01df72324e3bab76c7239274c03b472950b8d38b9d45bee75e2708b` |
| linux/arm64   | `0a968371ac340f9e6040cc3a9aab266e5a042ffc4b993aee91af8f17f45f7eb4` |
| windows/amd64 | `567967e5bbda6a40e8973a237b24e084ea3e9848e52388d0b7aa34a97307bc5f` |
| windows/arm64 | `c4537522373df88bf4a119a9a6eb5ca0713ee9970bfc0a30db4781ee8f83cbb9` |

| MCP target    | SHA-256                                                            |
| ------------- | ------------------------------------------------------------------ |
| darwin/amd64  | `5fcbde49f3cd37e6547c934e8ea19eedb21766697541fead625777dbab30a781` |
| darwin/arm64  | `8530777e216413acb2f79718376d00a75c33128916592e885cb09526067f2d89` |
| linux/amd64   | `ced223f1bb622c63edd1b3e2f7a591f9249c2694d2b1a7ff837e285faddd1b19` |
| linux/arm64   | `a4d359f15b5b75a9be0ad8afbb88dd727fc3d99999e8826b2f039ac2700ec178` |
| windows/amd64 | `f765939a54e91035af7304a5d50eae615818f151c59ff050d2850a095f91e7ca` |
| windows/arm64 | `db6819d24ce777663a3fabdeb0be6e0edc4cb7380b7ad9844606325cf8f55df9` |

Console and Site were also built twice from their current `v0.1.0` package
versions. Each run rebuilt `dist`, generated a production-license inventory,
created a normalized archive, verified every checksum, safely extracted the
archive, and compared the extracted tree byte-for-byte with `dist`. The two runs
were identical:

| Static bundle | SHA-256                                                            |
| ------------- | ------------------------------------------------------------------ |
| Console       | `41fe4b600d8bbe9f00f7291915c609f92b22cb8014193a225898ce4b6f72613f` |
| Site          | `df631d2cc7e8f09d98d55ae6f738d7d896906362cd7224c6e6e3740fcfb6244d` |

Android `v0.1.0` also completed the all-module tests, Release lint, unsigned
APK/AAB build, and repository-local package verifier. These artifacts are not
claimed as reproducible or installable until externally signed and device-tested:

| Android candidate | SHA-256                                                            |
| ----------------- | ------------------------------------------------------------------ |
| Unsigned APK      | `a4bcfd2f70b3d344a80df278807ffca0557893d7e0808ff9dede571d9fe55c36` |
| Unsigned AAB      | `5a8e5c78a67dff8c2ecb61a58f972e90d4010e3a81e26ac0a33c2d7c47416fca` |
| CycloneDX SBOM    | `21ecb89b660e42687c35639f3a3a15724c7595a5c81d2f000e24746a4a79ca37` |

## Container Artifact Contract

On 2026-07-18, the Server and Console Tag workflows were extended to produce
one OCI-layout tar archive containing exactly Linux AMD64 and ARM64 images.
Build/runtime bases, QEMU, and BuildKit are digest-pinned. A dependency-free
streaming verifier checks tar safety, every OCI blob digest and descriptor size,
both platforms, the exact version/revision/source/license/title labels, exposed
port `8080`, and runtime user `65532:65532`. Each repository's seven Node tests,
workflow lint, and Bash syntax checks pass; Console also passed its complete
107-test `pnpm verify` gate and a real static-archive verification.

No Docker engine is available on this workstation, so this is implementation
and verifier evidence only. No real OCI archive or image digest is claimed until
the immutable hosted Tag workflow builds and validates both architectures.

## Tag Gate

For a real release, create and push the reviewed Tag. GitHub Actions reruns all
tests, builds from that immutable revision, generates each repository's declared
SPDX or CycloneDX source SBOM, rewrites `SHA256SUMS`, invokes verification with
`--require-sbom` and, for Server/Console, `--require-container`, then uploads
only a draft prerelease. Review the workflow logs, SBOM contents, hashes, OCI
manifests, and generated notes before publication.

The local workstation had no `syft` or Docker engine, and no Tag was created or
pushed during this validation. Hosted SBOM generation, actual container images,
signing, and final publication therefore remain open release gates.
