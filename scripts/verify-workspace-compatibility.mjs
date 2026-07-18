import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { access, readFile, readdir } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const serverRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const workspaceRoot = resolve(process.env.VOICEASSET_WORKSPACE ?? dirname(serverRoot))
const repositoryNames = [
  'voice-asset-android',
  'voice-asset-console',
  'voice-asset-mcp',
  'voice-asset-server',
  'voice-asset-site',
]

function invariant(condition, message) {
  if (!condition) throw new Error(message)
}

async function text(path) {
  return readFile(resolve(workspaceRoot, path), 'utf8')
}

async function pin(repository) {
  return (await text(`${repository}/CONTRACT_VERSION`)).trim()
}

function quotedConstant(source, name, quote = "'") {
  const escapedQuote = quote === "'" ? "'" : '"'
  const expression = new RegExp(`\\b${name}\\s*=\\s*${escapedQuote}([^${escapedQuote}]+)${escapedQuote}`)
  const match = source.match(expression)
  invariant(match, `${name} is missing`)
  return match[1]
}

function quotedValues(source) {
  return [...source.matchAll(/['"]([a-z][a-z0-9_.-]*)['"]/g)].map((match) => match[1])
}

function block(source, expression, name) {
  const match = source.match(expression)
  invariant(match, `${name} declaration is missing`)
  return match[1]
}

function openAPIPath(source, path) {
  const marker = `  ${path}:`
  const start = source.indexOf(marker)
  if (start < 0) return undefined
  const nextPath = source.indexOf('\n  /api/', start + marker.length)
  return source.slice(start, nextPath < 0 ? undefined : nextPath)
}

function verifyFeatureSet(features, name) {
  invariant(Array.isArray(features), `${name} must be an array`)
  invariant(features.length > 0, `${name} must not be empty`)
  invariant(features.every((feature) => /^[a-z][a-z0-9_]*$/.test(feature)), `${name} has an invalid feature name`)
  invariant(new Set(features).size === features.length, `${name} contains duplicate features`)
  invariant([...features].sort().join('\n') === features.join('\n'), `${name} must be sorted`)
}

function verifyRequiredFeatures(required, advertised, client) {
  verifyFeatureSet(required, `${client} required features`)
  const available = new Set(advertised)
  const missing = required.filter((feature) => !available.has(feature))
  invariant(missing.length === 0, `${client} requires features not advertised by Server: ${missing.join(', ')}`)
}

async function verifyRepositoryLayout() {
  const entries = await readdir(workspaceRoot, { withFileTypes: true })
  const actual = []
  for (const entry of entries) {
    if (!entry.isDirectory() || !entry.name.startsWith('voice-asset-')) continue
    try {
      await access(resolve(workspaceRoot, entry.name, '.git'))
      actual.push(entry.name)
    } catch {
      // A similarly named non-repository directory is outside the five-repository invariant.
    }
  }
  actual.sort()
  invariant(
    actual.join('\n') === repositoryNames.join('\n'),
    `expected exactly five VoiceAsset repositories (${repositoryNames.join(', ')}); found ${actual.join(', ') || 'none'}`,
  )
}

function readRuntimeCapabilities() {
  const output = process.env.VOICEASSET_CAPABILITIES_FILE
    ? readFileSync(resolve(process.env.VOICEASSET_CAPABILITIES_FILE), 'utf8')
    : execFileSync(process.env.GO ?? 'go', ['run', './cmd/adminctl', 'capabilities'], {
        cwd: serverRoot,
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'inherit'],
      })
  const capabilities = JSON.parse(output)
  invariant(typeof capabilities.server_version === 'string' && capabilities.server_version.length > 0, 'Server runtime server_version is missing')
  invariant(capabilities.api_version === 'v1', `Server runtime API is ${JSON.stringify(capabilities.api_version)}, expected "v1"`)
  invariant(/^\d+\.\d+\.\d+$/.test(capabilities.contract_version), 'Server runtime contract_version is invalid')
  verifyFeatureSet(capabilities.features, 'Server advertised features')
  return capabilities
}

async function main() {
  await verifyRepositoryLayout()
  const capabilities = readRuntimeCapabilities()

  const pins = Object.fromEntries(
    await Promise.all(repositoryNames.map(async (repository) => [repository, await pin(repository)])),
  )
  for (const [repository, version] of Object.entries(pins)) {
    invariant(version === capabilities.contract_version, `${repository} pins ${version}, Server reports ${capabilities.contract_version}`)
  }

  const openapi = await text('voice-asset-server/contracts/openapi.yaml')
  const openapiVersion = openapi.match(/^  version: ([0-9]+\.[0-9]+\.[0-9]+)$/m)?.[1]
  invariant(openapiVersion === capabilities.contract_version, `OpenAPI info.version is ${openapiVersion ?? 'missing'}`)
  invariant(openapi.includes('/api/v1/system/capabilities:'), 'OpenAPI capabilities operation is missing')
  const capabilitiesSchema = block(
    openapi,
    /^    Capabilities:\r?\n([\s\S]*?)(?=^    [A-Za-z][A-Za-z0-9]*:\r?$|(?![\s\S]))/m,
    'OpenAPI Capabilities schema',
  )
  const contractVersionSchema = block(
    capabilitiesSchema,
    /^        contract_version:\r?\n([\s\S]*?)(?=^        [a-z][a-z0-9_]*:\r?$|(?![\s\S]))/m,
    'OpenAPI Capabilities contract_version property',
  )
  invariant(
    contractVersionSchema.includes(`          const: ${capabilities.contract_version}`),
    'OpenAPI Capabilities contract_version const is stale',
  )
  if (capabilities.features.includes('deployment_settings_read')) {
    const systemSettingsPath = openAPIPath(openapi, '/api/v1/admin/system-settings')
    invariant(
      systemSettingsPath && /^    get:/m.test(systemSettingsPath),
      'deployment_settings_read requires the OpenAPI system settings read route',
    )
  }
  if (capabilities.features.includes('personal_notifications')) {
    const eventsPath = openAPIPath(openapi, '/api/v1/events')
    invariant(
      eventsPath && /^    get:/m.test(eventsPath),
      'personal_notifications requires the OpenAPI personal events read route',
    )
    invariant(
      eventsPath.includes('- SessionCookie: []') && !eventsPath.includes('- BearerAuth: []'),
      'personal_notifications must remain Session-only in OpenAPI',
    )
  }

  const consoleContract = await text('voice-asset-console/src/config/contract.ts')
  invariant(quotedConstant(consoleContract, 'API_VERSION') === capabilities.api_version, 'Console API version is incompatible')
  invariant(quotedConstant(consoleContract, 'CONTRACT_VERSION') === capabilities.contract_version, 'Console contract constant is incompatible')
  const consoleFeatures = quotedValues(
    block(
      consoleContract,
      /REQUIRED_SERVER_FEATURES\s*=\s*Object\.freeze\(\[([\s\S]*?)\]\s*as const\)/,
      'Console REQUIRED_SERVER_FEATURES',
    ),
  )
  verifyRequiredFeatures(consoleFeatures, capabilities.features, 'Console')
  if (capabilities.features.includes('deployment_settings_read')) {
    invariant(
      consoleFeatures.includes('deployment_settings_read'),
      'Console must require deployment_settings_read',
    )
  }

  const androidContract = await text('voice-asset-android/core/api/src/main/kotlin/com/voiceasset/core/api/Compatibility.kt')
  invariant(quotedConstant(androidContract, 'SUPPORTED_API_VERSION', '"') === capabilities.api_version, 'Android API version is incompatible')
  invariant(
    quotedConstant(androidContract, 'SUPPORTED_CONTRACT_VERSION', '"') === capabilities.contract_version,
    'Android contract constant is incompatible',
  )
  const compatibleAndroidVersions = block(
    androidContract,
    /compatibleContractVersions\s*=\s*setOf\(([^)]*)\)/,
    'Android compatibleContractVersions',
  )
  invariant(
    compatibleAndroidVersions.includes('SUPPORTED_CONTRACT_VERSION'),
    'Android current contract is missing from compatibleContractVersions',
  )
  const androidFeatures = quotedValues(
    block(
      androidContract,
      /requiredAndroidSyncFeatures\s*=\s*setOf\(([\s\S]*?)\n\s*\)/,
      'Android requiredAndroidSyncFeatures',
    ),
  )
  verifyRequiredFeatures(androidFeatures, capabilities.features, 'Android')

  const mcpContract = await text('voice-asset-mcp/internal/backend/client.go')
  invariant(quotedConstant(mcpContract, 'SupportedAPIVersion', '"') === capabilities.api_version, 'MCP API version is incompatible')
  invariant(
    quotedConstant(mcpContract, 'SupportedContractVersion', '"') === capabilities.contract_version,
    'MCP contract constant is incompatible',
  )

  const siteOpenAPI = await text('voice-asset-site/public/openapi.yaml')
  invariant(siteOpenAPI === openapi, 'Site public/openapi.yaml is not byte-identical to the Server contract')

  console.log(
    `Workspace compatibility verified: five repositories, API ${capabilities.api_version}, contract ${capabilities.contract_version}, ` +
      `${capabilities.features.length} sorted Server features, Console ${consoleFeatures.length}, Android ${androidFeatures.length}, and exact Site OpenAPI.`,
  )
}

main().catch((error) => {
  console.error(`Workspace compatibility failed: ${error.message}`)
  process.exitCode = 1
})
