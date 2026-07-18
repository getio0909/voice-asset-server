import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const dockerfile = readFileSync(new URL('../Dockerfile', import.meta.url), 'utf8')
const release = readFileSync(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')
const ci = readFileSync(new URL('../.github/workflows/ci.yaml', import.meta.url), 'utf8')
const checksums = readFileSync(new URL('./write-checksums.sh', import.meta.url), 'utf8')
const verifier = readFileSync(new URL('./verify-release.sh', import.meta.url), 'utf8')

test('pins multi-platform base images and declares immutable OCI labels', () => {
  assert.match(dockerfile, /FROM golang:1\.26\.5-alpine@sha256:[0-9a-f]{64} AS build/)
  assert.match(dockerfile, /FROM alpine:3\.22@sha256:[0-9a-f]{64} AS runtime/)
  for (const label of ['licenses', 'revision', 'source', 'title', 'version']) {
    assert.match(dockerfile, new RegExp(`org\\.opencontainers\\.image\\.${label}=`))
  }
})

test('builds and verifies one amd64 and arm64 OCI archive on release tags', () => {
  assert.match(release, /docker\/setup-qemu-action@[0-9a-f]{40}/)
  assert.match(release, /docker\/setup-buildx-action@[0-9a-f]{40}/)
  assert.match(release, /--platform linux\/amd64,linux\/arm64/)
  assert.match(release, /type=oci,dest=/)
  assert.match(release, /verify-oci-image\.mjs/)
  assert.match(release, /--require-sbom --require-container/)
})

test('checksums and release verification cover the OCI archive', () => {
  assert.match(checksums, /\*\.oci\.tar/)
  assert.match(verifier, /--require-container/)
  assert.match(verifier, /verify-oci-image\.mjs/)
  assert.match(ci, /node --test scripts\/verify-oci-image\.test\.mjs scripts\/container-release\.test\.mjs/)
})
