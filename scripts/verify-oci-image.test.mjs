import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const verifier = fileURLToPath(new URL('./verify-oci-image.mjs', import.meta.url))
const image = 'voiceasset-server'
const version = 'v1.2.3'
const commit = '0123456789abcdef0123456789abcdef01234567'

function jsonBytes(value) {
  return Buffer.from(`${JSON.stringify(value)}\n`)
}

function addBlob(layout, bytes) {
  const digest = createHash('sha256').update(bytes).digest('hex')
  const path = join(layout, 'blobs', 'sha256', digest)
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, bytes)
  return { digest: `sha256:${digest}`, size: bytes.length }
}

function createFixture(t, { architectures = ['amd64', 'arm64'], user = '65532:65532', tamper = false } = {}) {
  const root = mkdtempSync(join(tmpdir(), 'voiceasset-oci-test-'))
  t.after(() => rmSync(root, { force: true, recursive: true }))
  const layout = join(root, 'layout')
  mkdirSync(layout, { recursive: true })
  writeFileSync(join(layout, 'oci-layout'), jsonBytes({ imageLayoutVersion: '1.0.0' }))
  const layer = addBlob(layout, Buffer.from('fixture-layer'))
  const descriptors = []
  let tamperPath = ''
  for (const architecture of architectures) {
    const configBytes = jsonBytes({
      architecture,
      os: 'linux',
      config: {
        User: user,
        ExposedPorts: { '8080/tcp': {} },
        Labels: {
          'org.opencontainers.image.licenses': 'AGPL-3.0-or-later',
          'org.opencontainers.image.revision': commit,
          'org.opencontainers.image.source': `https://github.com/getio0909/${image.replace('voiceasset-', 'voice-asset-')}`,
          'org.opencontainers.image.title': image,
          'org.opencontainers.image.version': version,
        },
      },
    })
    const config = addBlob(layout, configBytes)
    const manifestBytes = jsonBytes({
      schemaVersion: 2,
      mediaType: 'application/vnd.oci.image.manifest.v1+json',
      config: { mediaType: 'application/vnd.oci.image.config.v1+json', ...config },
      layers: [{ mediaType: 'application/vnd.oci.image.layer.v1.tar+gzip', ...layer }],
    })
    const manifest = addBlob(layout, manifestBytes)
    tamperPath ||= join(layout, 'blobs', 'sha256', config.digest.slice('sha256:'.length))
    descriptors.push({
      mediaType: 'application/vnd.oci.image.manifest.v1+json',
      ...manifest,
      platform: { architecture, os: 'linux' },
    })
  }
  writeFileSync(
    join(layout, 'index.json'),
    jsonBytes({
      schemaVersion: 2,
      mediaType: 'application/vnd.oci.image.index.v1+json',
      manifests: descriptors,
    }),
  )
  if (tamper) {
    writeFileSync(tamperPath, Buffer.from('tampered'))
  }
  const archive = join(root, 'image.oci.tar')
  const packed = spawnSync('tar', ['-cf', archive, '-C', layout, '.'], { encoding: 'utf8' })
  assert.equal(packed.status, 0, packed.stderr)
  return archive
}

function verify(archive) {
  return spawnSync(process.execPath, [verifier, archive, image, version, commit], { encoding: 'utf8' })
}

test('accepts a pinned non-root amd64 and arm64 OCI image', (t) => {
  const result = verify(createFixture(t))
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.stdout, /verified OCI image/)
})

test('rejects an OCI image missing arm64', (t) => {
  const result = verify(createFixture(t, { architectures: ['amd64'] }))
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /linux\/amd64 and linux\/arm64/)
})

test('rejects a root runtime user', (t) => {
  const result = verify(createFixture(t, { user: '0' }))
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /non-root user/)
})

test('rejects a blob whose digest does not match its path', (t) => {
  const result = verify(createFixture(t, { tamper: true }))
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /digest mismatch/)
})
