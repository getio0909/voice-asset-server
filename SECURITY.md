# Security Policy

## Supported Versions

Before the first stable release, security fixes are applied to the latest
development branch only. After `v1.0.0`, this table will identify supported
release lines and their end-of-support dates.

## Reporting a Vulnerability

Do not open a public issue. Use GitHub's **Security > Report a vulnerability**
flow for this repository and include:

- affected version or commit;
- reproducible steps and expected impact;
- logs or fixtures with credentials and personal data removed;
- any known workaround.

Maintainers will acknowledge receipt, assess severity, coordinate a private
fix, and publish an advisory when users can safely upgrade. Do not include real
audio, tokens, provider credentials, database dumps, or encryption keys.

## Security Baseline

Provider secrets belong only on the Server and must be encrypted before
persistence. Original audio and raw provider responses are immutable. Logs must
be structured and redacted. Custom endpoints, uploaded media, signed URLs,
FFmpeg arguments, and every Agent write operation are untrusted security
boundaries. The local Compose password is deliberately non-secret and must be
replaced for any shared or production deployment.
