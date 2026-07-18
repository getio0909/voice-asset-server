#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { closeSync, fstatSync, lstatSync, openSync, readSync } from 'node:fs'

const blockSize = 512
const maxMetadataBytes = 2 * 1024 * 1024
const indexMediaType = 'application/vnd.oci.image.index.v1+json'
const manifestMediaType = 'application/vnd.oci.image.manifest.v1+json'
const configMediaType = 'application/vnd.oci.image.config.v1+json'

function fail(message) {
  console.error(`verify-oci-image: ${message}`)
  process.exit(1)
}

function readText(buffer, start, length) {
  const end = buffer.indexOf(0, start)
  const limit = end >= start && end < start + length ? end : start + length
  return buffer.subarray(start, limit).toString('utf8')
}

function readOctal(buffer, start, length, field) {
  const value = buffer.subarray(start, start + length).toString('ascii').replaceAll('\0', '').trim()
  if (value === '') return 0
  if (!/^[0-7]+$/.test(value)) fail(`tar ${field} is not octal`)
  const parsed = Number.parseInt(value, 8)
  if (!Number.isSafeInteger(parsed) || parsed < 0) fail(`tar ${field} is outside the safe range`)
  return parsed
}

function readAt(fd, length, position, field) {
  const buffer = Buffer.alloc(length)
  let offset = 0
  while (offset < length) {
    const count = readSync(fd, buffer, offset, length - offset, position + offset)
    if (count === 0) fail(`unexpected end of tar while reading ${field}`)
    offset += count
  }
  return buffer
}

function normalizeEntry(name) {
  let value = name
  while (value.startsWith('./')) value = value.slice(2)
  while (value.endsWith('/')) value = value.slice(0, -1)
  if (value === '.' || value === '') return ''
  if (value.includes('\\') || value.startsWith('/') || /^[A-Za-z]:/.test(value)) {
    fail(`unsafe tar path: ${name}`)
  }
  const parts = value.split('/')
  if (parts.some((part) => part === '' || part === '.' || part === '..')) {
    fail(`unsafe tar path: ${name}`)
  }
  return value
}

function validateHeaderChecksum(header, name) {
  const expected = readOctal(header, 148, 8, `checksum for ${name}`)
  let actual = 0
  for (let index = 0; index < header.length; index += 1) {
    actual += index >= 148 && index < 156 ? 0x20 : header[index]
  }
  if (actual !== expected) fail(`tar header checksum mismatch: ${name}`)
}

function parseTar(path) {
  const stat = lstatSync(path)
  if (!stat.isFile() || stat.isSymbolicLink()) fail('archive must be a regular, non-symlink file')
  const fd = openSync(path, 'r')
  const metadata = new Map()
  const blobSizes = new Map()
  const entries = new Set()
  let position = 0
  try {
    while (position + blockSize <= stat.size) {
      const header = readAt(fd, blockSize, position, 'tar header')
      position += blockSize
      if (header.every((byte) => byte === 0)) break
      const namePart = readText(header, 0, 100)
      const prefix = readText(header, 345, 155)
      const rawName = prefix === '' ? namePart : `${prefix}/${namePart}`
      const name = normalizeEntry(rawName)
      validateHeaderChecksum(header, rawName)
      const type = header[156] === 0 ? '0' : String.fromCharCode(header[156])
      const size = readOctal(header, 124, 12, `size for ${rawName}`)
      if (type !== '0' && type !== '5') fail(`unsupported tar entry type ${type}: ${rawName}`)
      if (entries.has(name)) fail(`duplicate tar entry: ${rawName}`)
      entries.add(name)
      if (type === '5') {
        if (size !== 0) fail(`tar directory has non-zero size: ${rawName}`)
      } else {
        const allowed = name === 'oci-layout' || name === 'index.json' || /^blobs\/sha256\/[0-9a-f]{64}$/.test(name)
        if (!allowed) fail(`unexpected OCI layout file: ${rawName}`)
        const hash = createHash('sha256')
        const chunks = []
        let remaining = size
        let offset = 0
        while (remaining > 0) {
          const length = Math.min(remaining, 1024 * 1024)
          const chunk = readAt(fd, length, position + offset, rawName)
          hash.update(chunk)
          if (size <= maxMetadataBytes) chunks.push(chunk)
          remaining -= length
          offset += length
        }
        const digest = hash.digest('hex')
        if (name.startsWith('blobs/sha256/')) {
          const expected = name.slice('blobs/sha256/'.length)
          if (digest !== expected) fail(`blob digest mismatch: ${rawName}`)
          blobSizes.set(`sha256:${expected}`, size)
        }
        if (size <= maxMetadataBytes) metadata.set(name, Buffer.concat(chunks))
      }
      position += Math.ceil(size / blockSize) * blockSize
      if (position > stat.size) fail(`tar entry exceeds archive: ${rawName}`)
    }
  } finally {
    closeSync(fd)
  }
  return { metadata, blobSizes }
}

function parseJSON(buffer, field) {
  if (!buffer) fail(`${field} is missing or too large`)
  try {
    return JSON.parse(buffer.toString('utf8'))
  } catch {
    fail(`${field} is not valid JSON`)
  }
}

function validateDescriptor(descriptor, blobSizes, field) {
  if (!descriptor || typeof descriptor !== 'object' || Array.isArray(descriptor)) fail(`${field} is invalid`)
  if (!/^sha256:[0-9a-f]{64}$/.test(descriptor.digest ?? '')) fail(`${field} digest is invalid`)
  const size = blobSizes.get(descriptor.digest)
  if (size === undefined) fail(`${field} blob is missing`)
  if (descriptor.size !== size) fail(`${field} size does not match its blob`)
  return `blobs/sha256/${descriptor.digest.slice('sha256:'.length)}`
}

function validateImage(metadata, blobSizes, descriptor, expected) {
  if (descriptor.mediaType !== manifestMediaType) fail('index contains a non-image manifest')
  const platform = descriptor.platform
  const platformName = `${platform?.os ?? ''}/${platform?.architecture ?? ''}`
  if (!expected.platforms.has(platformName)) fail(`unexpected OCI platform: ${platformName}`)
  if (expected.seen.has(platformName)) fail(`duplicate OCI platform: ${platformName}`)
  expected.seen.add(platformName)

  const manifestPath = validateDescriptor(descriptor, blobSizes, `manifest for ${platformName}`)
  const manifest = parseJSON(metadata.get(manifestPath), `manifest for ${platformName}`)
  if (manifest.schemaVersion !== 2 || manifest.mediaType !== manifestMediaType) {
    fail(`manifest for ${platformName} is not OCI image manifest v1`)
  }
  if (manifest.config?.mediaType !== configMediaType) fail(`config media type is invalid for ${platformName}`)
  const configPath = validateDescriptor(manifest.config, blobSizes, `config for ${platformName}`)
  if (!Array.isArray(manifest.layers) || manifest.layers.length === 0) fail(`layers are missing for ${platformName}`)
  for (const [index, layer] of manifest.layers.entries()) {
    validateDescriptor(layer, blobSizes, `layer ${index} for ${platformName}`)
  }

  const config = parseJSON(metadata.get(configPath), `config for ${platformName}`)
  if (config.os !== platform.os || config.architecture !== platform.architecture) {
    fail(`config platform differs from index for ${platformName}`)
  }
  if (config.config?.User !== '65532:65532') fail(`image must run as non-root user 65532:65532 for ${platformName}`)
  if (!config.config?.ExposedPorts || !Object.hasOwn(config.config.ExposedPorts, '8080/tcp')) {
    fail(`image must expose 8080/tcp for ${platformName}`)
  }
  const labels = config.config?.Labels ?? {}
  const requiredLabels = {
    'org.opencontainers.image.licenses': 'AGPL-3.0-or-later',
    'org.opencontainers.image.revision': expected.commit,
    'org.opencontainers.image.source': `https://github.com/getio0909/${expected.image.replace('voiceasset-', 'voice-asset-')}`,
    'org.opencontainers.image.title': expected.image,
    'org.opencontainers.image.version': expected.version,
  }
  for (const [name, value] of Object.entries(requiredLabels)) {
    if (labels[name] !== value) fail(`label ${name} is invalid for ${platformName}`)
  }
}

function validateImageIndex(metadata, blobSizes, index, expected, field) {
  if (
    !index ||
    typeof index !== 'object' ||
    index.schemaVersion !== 2 ||
    index.mediaType !== indexMediaType ||
    !Array.isArray(index.manifests)
  ) {
    fail(`${field} is not an OCI image index`)
  }
  for (const descriptor of index.manifests) validateImage(metadata, blobSizes, descriptor, expected)
}

const [archive, image, version, commit] = process.argv.slice(2)
if (!archive || !image || !version || !commit || process.argv.length !== 6) {
  fail('usage: verify-oci-image.mjs <archive> <image-name> <version> <commit>')
}
if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(image)) fail('image name is invalid')
if (!/^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(version)) {
  fail('version must be a semantic version tag')
}
if (!/^[0-9A-Fa-f]{7,64}$/.test(commit)) fail('commit must be a hexadecimal revision')

const { metadata, blobSizes } = parseTar(archive)
const layout = parseJSON(metadata.get('oci-layout'), 'oci-layout')
if (layout.imageLayoutVersion !== '1.0.0') fail('unsupported OCI layout version')
const index = parseJSON(metadata.get('index.json'), 'index.json')
if (index.schemaVersion !== 2 || !Array.isArray(index.manifests)) fail('index.json is not an OCI image index')
const expected = {
  image,
  version,
  commit,
  platforms: new Set(['linux/amd64', 'linux/arm64']),
  seen: new Set(),
}
if (index.manifests.length === 1 && index.manifests[0].mediaType === indexMediaType) {
  const nestedDescriptor = index.manifests[0]
  if (nestedDescriptor.platform) fail('nested OCI index descriptor must not declare a platform')
  const nestedPath = validateDescriptor(nestedDescriptor, blobSizes, 'nested OCI index')
  const nestedIndex = parseJSON(metadata.get(nestedPath), 'nested OCI index')
  validateImageIndex(metadata, blobSizes, nestedIndex, expected, 'nested OCI index')
} else {
  validateImageIndex(metadata, blobSizes, index, expected, 'index.json')
}
if (expected.seen.size !== 2 || [...expected.platforms].some((platform) => !expected.seen.has(platform))) {
  fail('OCI image must contain exactly linux/amd64 and linux/arm64')
}

console.log(`verified OCI image ${image}:${version} for linux/amd64 and linux/arm64`)
