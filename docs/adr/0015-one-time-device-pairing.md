# ADR 0015: One-Time Device Pairing

- Status: Accepted
- Date: 2026-07-18

## Context

Android must pair with a self-hosted VoiceAsset Server without placing a user
password, reusable access token, refresh token, Provider secret, or TLS bypass
in a QR code. The existing browser and Android authentication boundary already
uses revocable, named device sessions. Pairing therefore needs to create the
same session type while remaining safe if a payload is photographed or scanned
by the wrong device.

Three options were considered:

| Option | Benefit | Cost or risk |
| --- | --- | --- |
| Encode a password or existing session | Minimal Server work | Exposes a reusable credential and cannot be safely audited or bounded |
| OAuth-style device polling | Familiar authorization model | Adds polling state and UI complexity not required for a same-user, nearby-device flow |
| One-time claim secret | Small synchronous protocol; reuses device sessions | The visible payload is a five-minute bearer capability and must be protected until consumed |

## Decision

Use a versioned `voiceasset://pair` payload containing only the canonical public
Server origin, API/contract identity, pairing-session UUID, expiry, and a random
32-byte one-time claim secret.

- Only an authenticated personal session may create a pairing session. API keys
  cannot create one. Creating a new pairing session revokes older unclaimed
  sessions for the same user and workspace.
- The claim secret is returned once, stored only as SHA-256, expires after five
  minutes, and is never written to logs or audit metadata.
- Claiming is rate-limited and requires the exact public `Origin`. The Server
  atomically verifies active user and membership state, consumes the pairing
  session, creates a named access/refresh device session, and writes safe
  creation/claim audits. Expired, revoked, invalid, and replayed claims share one
  failure response.
- Android strictly rejects unknown fields, duplicate query keys, fragments,
  user information, incompatible API/contract versions, non-canonical origins,
  malformed UUIDs/secrets/timestamps, and expired payloads before network I/O.
- Pairing never weakens TLS. Public certificates use system trust; private-CA
  deployments still require an installed/configured CA and any explicit
  fingerprint policy before the claim request.

## Trade-offs and Consequences

The synchronous claim is simpler than polling and fits the modular monolith,
but possession of an unexpired payload authorizes one device session. Short
expiry, single use, latest-only issuance, rate limiting, and user-visible device
revocation bound that risk.

The protocol and a paste/copy fallback can be completed without a new runtime
dependency. Camera decoding and visual QR rendering remain presentation layers
over the same payload and require separately reviewed, license-pinned production
dependencies. They must not change the authentication or TLS rules above.

Revisit device polling only if pairing must cross devices that are not nearby,
requires administrator approval after scanning, or needs an explicit deny flow.
