import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const script = resolve(dirname(fileURLToPath(import.meta.url)), 'verify-workspace-compatibility.mjs')
const repositories = [
  'voice-asset-android',
  'voice-asset-console',
  'voice-asset-mcp',
  'voice-asset-server',
  'voice-asset-site',
]
const version = '1.2.3'

async function put(root, relative, content = '') {
  const destination = join(root, relative)
  await mkdir(dirname(destination), { recursive: true })
  await writeFile(destination, content, 'utf8')
}

async function fixture() {
  const root = await mkdtemp(join(tmpdir(), 'voiceasset-compatibility-'))
  for (const repository of repositories) {
    await mkdir(join(root, repository, '.git'), { recursive: true })
    await put(root, `${repository}/CONTRACT_VERSION`, `${version}\n`)
  }

  const openapi = `openapi: 3.1.0
info:
  title: VoiceAsset API
  version: ${version}
paths:
  /api/v1/system/capabilities:
    get: {}
  /api/v1/admin/system-settings:
    parameters:
      - name: X-Request-ID
    get: {}
  /api/v1/events:
    get:
      security:
        - SessionCookie: []
components:
  schemas:
    Capabilities:
      properties:
        contract_version:
          const: ${version}
`
  await put(root, 'voice-asset-server/contracts/openapi.yaml', openapi)
  await put(root, 'voice-asset-site/public/openapi.yaml', openapi)
  await put(
    root,
    'voice-asset-console/src/config/contract.ts',
    `export const API_VERSION = 'v1'
export const CONTRACT_VERSION = '${version}'
export const REQUIRED_SERVER_FEATURES = Object.freeze([
  'alpha',
  'beta',
  'deployment_settings_read',
] as const)
`,
  )
  await put(
    root,
    'voice-asset-android/core/api/src/main/kotlin/com/voiceasset/core/api/Compatibility.kt',
    `const val SUPPORTED_API_VERSION = "v1"
const val SUPPORTED_CONTRACT_VERSION = "${version}"
private val compatibleContractVersions = setOf("1.2.2", SUPPORTED_CONTRACT_VERSION)
private val requiredAndroidSyncFeatures =
    setOf(
        "beta",
    )
`,
  )
  await put(
    root,
    'voice-asset-mcp/internal/backend/client.go',
    `package backend
const (
    SupportedAPIVersion = "v1"
    SupportedContractVersion = "${version}"
)
`,
  )
  const capabilities = join(root, 'capabilities.json')
  await put(
    root,
    'capabilities.json',
    `${JSON.stringify({
      server_version: '1.0.0-test',
      api_version: 'v1',
      contract_version: version,
      features: ['alpha', 'beta', 'deployment_settings_read', 'personal_notifications'],
    })}\n`,
  )
  return { root, capabilities }
}

function verify(root, capabilities) {
  return spawnSync(process.execPath, [script], {
    encoding: 'utf8',
    env: {
      ...process.env,
      VOICEASSET_WORKSPACE: root,
      VOICEASSET_CAPABILITIES_FILE: capabilities,
    },
  })
}

test('accepts one synchronized five-repository workspace', async (t) => {
  const { root, capabilities } = await fixture()
  t.after(() => rm(root, { recursive: true, force: true }))

  const result = verify(root, capabilities)

  assert.equal(result.status, 0, result.stderr)
  assert.match(result.stdout, /Workspace compatibility verified/)
})

test('rejects a sixth VoiceAsset repository', async (t) => {
  const { root, capabilities } = await fixture()
  t.after(() => rm(root, { recursive: true, force: true }))
  await mkdir(join(root, 'voice-asset-extra', '.git'), { recursive: true })

  const result = verify(root, capabilities)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /expected exactly five VoiceAsset repositories/)
})

test('rejects a client capability absent from Server runtime output', async (t) => {
  const { root, capabilities } = await fixture()
  t.after(() => rm(root, { recursive: true, force: true }))
  await put(
    root,
    'voice-asset-console/src/config/contract.ts',
    `export const API_VERSION = 'v1'
export const CONTRACT_VERSION = '${version}'
export const REQUIRED_SERVER_FEATURES = Object.freeze([
  'alpha',
  'gamma',
] as const)
`,
  )

  const result = verify(root, capabilities)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /Console requires features not advertised by Server: gamma/)
})

test('rejects a Site OpenAPI copy that drifted from Server', async (t) => {
  const { root, capabilities } = await fixture()
  t.after(() => rm(root, { recursive: true, force: true }))
  await put(root, 'voice-asset-site/public/openapi.yaml', 'openapi: 3.1.0\n')

  const result = verify(root, capabilities)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /Site public\/openapi.yaml is not byte-identical/)
})

test('rejects deployment settings capability without its OpenAPI read route', async (t) => {
  const { root, capabilities } = await fixture()
  t.after(() => rm(root, { recursive: true, force: true }))
  const contractPath = join(root, 'voice-asset-server', 'contracts', 'openapi.yaml')
  const contract = await readFile(contractPath, 'utf8')
  const missingRoute = contract.replace(
    '  /api/v1/admin/system-settings:\n    parameters:\n      - name: X-Request-ID\n    get: {}\n',
    '',
  )
  await put(root, 'voice-asset-server/contracts/openapi.yaml', missingRoute)
  await put(root, 'voice-asset-site/public/openapi.yaml', missingRoute)

  const result = verify(root, capabilities)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /deployment_settings_read requires the OpenAPI system settings read route/)
})

test('rejects a Console that does not require deployment settings support', async (t) => {
  const { root, capabilities } = await fixture()
  t.after(() => rm(root, { recursive: true, force: true }))
  await put(
    root,
    'voice-asset-console/src/config/contract.ts',
    `export const API_VERSION = 'v1'
export const CONTRACT_VERSION = '${version}'
export const REQUIRED_SERVER_FEATURES = Object.freeze([
  'alpha',
  'beta',
] as const)
`,
  )

  const result = verify(root, capabilities)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /Console must require deployment_settings_read/)
})

test('rejects personal notifications without the Session-only OpenAPI route', async (t) => {
  const { root, capabilities } = await fixture()
  t.after(() => rm(root, { recursive: true, force: true }))
  const contractPath = join(root, 'voice-asset-server', 'contracts', 'openapi.yaml')
  const contract = await readFile(contractPath, 'utf8')
  const missingRoute = contract.replace(
    '  /api/v1/events:\n    get:\n      security:\n        - SessionCookie: []\n',
    '',
  )
  await put(root, 'voice-asset-server/contracts/openapi.yaml', missingRoute)
  await put(root, 'voice-asset-site/public/openapi.yaml', missingRoute)

  const result = verify(root, capabilities)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /personal_notifications requires the OpenAPI personal events read route/)
})

test('rejects a stale Capabilities contract const despite a matching decoy', async (t) => {
  const { root, capabilities } = await fixture()
  t.after(() => rm(root, { recursive: true, force: true }))
  const stale = `openapi: 3.1.0
info:
  title: VoiceAsset API
  version: ${version}
paths:
  /api/v1/system/capabilities:
    get: {}
components:
  schemas:
    Capabilities:
      properties:
        contract_version:
          const: 9.9.9
    Decoy:
      properties:
        version:
          const: ${version}
`
  await put(root, 'voice-asset-server/contracts/openapi.yaml', stale)
  await put(root, 'voice-asset-site/public/openapi.yaml', stale)

  const result = verify(root, capabilities)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /OpenAPI Capabilities contract_version const is stale/)
})
