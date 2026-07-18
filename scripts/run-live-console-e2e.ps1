[CmdletBinding()]
param(
    [string]$EnvironmentFile = (Join-Path $PSScriptRoot '..\..\.env'),
    [int]$ApiPort = 18080,
    [int]$ConsolePort = 4173
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Read-DotEnv([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Environment file was not found: $Path"
    }
    $values = @{}
    foreach ($rawLine in Get-Content -LiteralPath $Path) {
        $line = $rawLine.Trim()
        if (-not $line -or $line.StartsWith('#') -or -not $line.Contains('=')) {
            continue
        }
        $parts = $line.Split('=', 2)
        $name = $parts[0].Trim()
        $value = $parts[1].Trim()
        if (
            $value.Length -ge 2 -and
            (($value.StartsWith('"') -and $value.EndsWith('"')) -or
            ($value.StartsWith("'") -and $value.EndsWith("'")))
        ) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        $values[$name] = $value
    }
    return $values
}

function Require-Value([hashtable]$Values, [string]$Name) {
    $value = [string]$Values[$Name]
    if ([string]::IsNullOrWhiteSpace($value)) {
        throw "Required environment key is missing: $Name"
    }
    return $value
}

function Invoke-Checked([string]$FilePath, [string[]]$Arguments) {
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Command failed with exit code ${LASTEXITCODE}: $([IO.Path]::GetFileName($FilePath))"
    }
}

function Stop-ExactProcess([AllowNull()][Diagnostics.Process]$Process) {
    if ($null -ne $Process -and -not $Process.HasExited) {
        Stop-Process -Id $Process.Id
        Wait-Process -Id $Process.Id -Timeout 10 -ErrorAction SilentlyContinue
    }
}

$serverRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$workspaceRoot = [IO.Path]::GetFullPath((Join-Path $serverRoot '..'))
$consoleRoot = [IO.Path]::GetFullPath((Join-Path $workspaceRoot 'voice-asset-console'))
$mcpRoot = [IO.Path]::GetFullPath((Join-Path $workspaceRoot 'voice-asset-mcp'))
$environmentPath = [IO.Path]::GetFullPath($EnvironmentFile)
$runId = [Guid]::NewGuid().ToString('N')
$schema = "voiceasset_live_$runId"
$artifactRoot = [IO.Path]::GetFullPath((Join-Path $serverRoot ".build\live-console-e2e-$runId"))
$storageRoot = Join-Path $artifactRoot 'objects'
$apiStdout = Join-Path $artifactRoot 'api.stdout.log'
$apiStderr = Join-Path $artifactRoot 'api.stderr.log'
$workerStdout = Join-Path $artifactRoot 'worker.stdout.log'
$workerStderr = Join-Path $artifactRoot 'worker.stderr.log'
$apiExecutable = Join-Path $artifactRoot 'voiceasset-api.exe'
$workerExecutable = Join-Path $artifactRoot 'voiceasset-worker.exe'
$migrateExecutable = Join-Path $artifactRoot 'voiceasset-migrate.exe'
$adminExecutable = Join-Path $artifactRoot 'voiceasset-adminctl.exe'
$apiProcess = $null
$workerProcess = $null
$schemaCreated = $false

[IO.Directory]::CreateDirectory($artifactRoot) | Out-Null
[IO.Directory]::CreateDirectory($storageRoot) | Out-Null

$values = Read-DotEnv $environmentPath
$postgresHost = Require-Value $values 'POSTGRES_HOST'
$postgresPort = Require-Value $values 'POSTGRES_PORT'
$postgresDatabase = Require-Value $values 'POSTGRES_DB'
$postgresUser = Require-Value $values 'POSTGRES_USER'
$postgresPassword = Require-Value $values 'POSTGRES_PASSWORD'
$postgresSSLMode = Require-Value $values 'POSTGRES_SSLMODE'
if ($postgresHost -notmatch '^[A-Za-z0-9.:-]+$' -or $postgresPort -notmatch '^\d{1,5}$') {
    throw 'PostgreSQL host or port has an unsupported format.'
}
if ($postgresSSLMode -notin @('disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full')) {
    throw 'POSTGRES_SSLMODE is unsupported.'
}

$encodedUser = [Uri]::EscapeDataString($postgresUser)
$encodedPassword = [Uri]::EscapeDataString($postgresPassword)
$encodedDatabase = [Uri]::EscapeDataString($postgresDatabase)
$databaseURL = "postgres://${encodedUser}:${encodedPassword}@${postgresHost}:${postgresPort}/${encodedDatabase}?sslmode=${postgresSSLMode}&search_path=${schema}"
$passwordBytes = [byte[]]::new(32)
[Security.Cryptography.RandomNumberGenerator]::Fill($passwordBytes)
$ownerPassword = [Convert]::ToBase64String($passwordBytes).TrimEnd('=').Replace('+', 'A').Replace('/', 'B')
$profileKeyBytes = [byte[]]::new(32)
[Security.Cryptography.RandomNumberGenerator]::Fill($profileKeyBytes)
$ownerEmail = "live-$runId@example.test"
$publicOrigin = "http://127.0.0.1:$ConsolePort"
$apiURL = "http://127.0.0.1:$ApiPort"

$env:PGPASSWORD = $postgresPassword
$env:PGSSLMODE = $postgresSSLMode
$env:DATABASE_URL = $databaseURL
$env:VOICEASSET_HTTP_ADDR = "127.0.0.1:$ApiPort"
$env:VOICEASSET_PUBLIC_ORIGIN = $publicOrigin
$env:VOICEASSET_COOKIE_SECURE = 'false'
$env:VOICEASSET_STORAGE_PATH = $storageRoot
$env:VOICEASSET_PROFILE_MASTER_KEY = [Convert]::ToBase64String($profileKeyBytes)
$env:VOICEASSET_API_PROXY_TARGET = $apiURL
$env:VOICEASSET_CONSOLE_PORT = [string]$ConsolePort
$env:VOICEASSET_LIVE_E2E = '1'
$env:VOICEASSET_LIVE_OWNER_EMAIL = $ownerEmail
$env:VOICEASSET_LIVE_OWNER_PASSWORD = $ownerPassword

try {
    Write-Host 'Creating isolated PostgreSQL schema.'
    Invoke-Checked 'psql' @(
        '-X', '-v', 'ON_ERROR_STOP=1', '-h', $postgresHost, '-p', $postgresPort,
        '-U', $postgresUser, '-d', $postgresDatabase, '-c', "CREATE SCHEMA `"$schema`""
    )
    $schemaCreated = $true

    Write-Host 'Building isolated Server commands.'
    Push-Location $serverRoot
    try {
        Invoke-Checked 'go' @('build', '-o', $apiExecutable, './cmd/api')
        Invoke-Checked 'go' @('build', '-o', $workerExecutable, './cmd/worker')
        Invoke-Checked 'go' @('build', '-o', $migrateExecutable, './cmd/migrate')
        Invoke-Checked 'go' @('build', '-o', $adminExecutable, './cmd/adminctl')

        Write-Host 'Applying migrations to the isolated schema.'
        Invoke-Checked $migrateExecutable @()

        Write-Host 'Creating an ephemeral owner through password stdin.'
        $startInfo = [Diagnostics.ProcessStartInfo]::new()
        $startInfo.FileName = $adminExecutable
        $startInfo.UseShellExecute = $false
        $startInfo.RedirectStandardInput = $true
        $startInfo.RedirectStandardOutput = $true
        $startInfo.RedirectStandardError = $true
        foreach ($argument in @(
            'create-admin', '--email', $ownerEmail, '--workspace', 'Live E2E Workspace', '--password-stdin'
        )) {
            $startInfo.ArgumentList.Add($argument)
        }
        $adminProcess = [Diagnostics.Process]::new()
        $adminProcess.StartInfo = $startInfo
        if (-not $adminProcess.Start()) {
            throw 'Could not start adminctl.'
        }
        $adminProcess.StandardInput.WriteLine($ownerPassword)
        $adminProcess.StandardInput.Close()
        $adminOutput = $adminProcess.StandardOutput.ReadToEnd()
        $adminError = $adminProcess.StandardError.ReadToEnd()
        $adminProcess.WaitForExit()
        if ($adminProcess.ExitCode -ne 0) {
            throw "adminctl failed: $adminError"
        }
        $null = $adminOutput

        Write-Host 'Starting real API and worker processes.'
        $apiProcess = Start-Process -FilePath $apiExecutable -WorkingDirectory $serverRoot `
            -RedirectStandardOutput $apiStdout -RedirectStandardError $apiStderr `
            -WindowStyle Hidden -PassThru
        $workerProcess = Start-Process -FilePath $workerExecutable -ArgumentList @('--heartbeat', '250ms') `
            -WorkingDirectory $serverRoot -RedirectStandardOutput $workerStdout `
            -RedirectStandardError $workerStderr -WindowStyle Hidden -PassThru
    }
    finally {
        Pop-Location
    }

    $ready = $false
    for ($attempt = 0; $attempt -lt 80; $attempt += 1) {
        if ($apiProcess.HasExited) {
            throw 'API exited before becoming ready.'
        }
        try {
            $response = Invoke-WebRequest -Uri "$apiURL/readyz" -TimeoutSec 2 -UseBasicParsing
            if ($response.StatusCode -eq 200) {
                $ready = $true
                break
            }
        }
        catch {
            Start-Sleep -Milliseconds 250
        }
    }
    if (-not $ready) {
        throw 'API did not become ready within 20 seconds.'
    }

    Write-Host 'Running the live Console browser workflow.'
    Push-Location $consoleRoot
    try {
        Invoke-Checked 'pnpm' @('run', 'build')
        Invoke-Checked 'pnpm' @(
            'exec', 'playwright', 'test',
            'e2e/live-phase1.spec.ts', 'e2e/live-providers.spec.ts',
            'e2e/live-llm-providers.spec.ts', '--workers=1'
        )
    }
    finally {
        Pop-Location
    }
    Write-Host 'Live Console E2E passed.'

    Write-Host 'Creating an ephemeral Server session and scoped API key for the live MCP workflow.'
    $loginBody = @{ email = $ownerEmail; password = $ownerPassword } | ConvertTo-Json -Compress
    $mcpWebSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
    $login = Invoke-RestMethod -Method Post -Uri "$apiURL/api/v1/auth/sessions" `
        -ContentType 'application/json' -Headers @{ Origin = $publicOrigin } -Body $loginBody `
        -WebSession $mcpWebSession
    $mcpSessionCookie = $mcpWebSession.Cookies.GetCookies([Uri]"$apiURL/api/v1/") |
        Where-Object { $_.Name -eq 'voiceasset_session' } | Select-Object -First 1
    if ($null -eq $mcpSessionCookie) {
        throw 'Server login did not set the MCP E2E session cookie.'
    }
    $mcpSessionToken = [string]$mcpSessionCookie.Value
    if ([string]::IsNullOrWhiteSpace($mcpSessionToken)) {
        throw 'Server login did not return a session token for MCP E2E.'
    }
    $apiKeyBody = @{
        name = 'Live MCP reader'
        scopes = @('assets:read', 'transcripts:read')
        expires_at = [DateTime]::UtcNow.AddMinutes(30).ToString('o')
    } | ConvertTo-Json -Compress
    $createdAPIKey = Invoke-RestMethod -Method Post -Uri "$apiURL/api/v1/api-keys" `
        -ContentType 'application/json' -Headers @{ Origin = $publicOrigin } -Body $apiKeyBody `
        -WebSession $mcpWebSession
    $mcpAPIKeyID = [string]$createdAPIKey.api_key.id
    $mcpServerToken = [string]$createdAPIKey.token
    $parsedAPIKeyID = [Guid]::Empty
    if (
        -not [Guid]::TryParse($mcpAPIKeyID, [ref]$parsedAPIKeyID) -or
        [string]::IsNullOrWhiteSpace($mcpServerToken) -or
        -not $mcpServerToken.StartsWith('va_pat_')
    ) {
        throw 'Server did not return a valid scoped API key for MCP E2E.'
    }
    $env:VOICE_ASSET_MCP_LIVE_E2E = '1'
    $env:VOICE_ASSET_SERVER_URL = $apiURL
    $env:VOICE_ASSET_SERVER_TOKEN = $mcpServerToken
    Write-Host 'Running the live MCP search, revision, and exact-range workflow.'
    Push-Location $mcpRoot
    try {
        Invoke-Checked 'go' @('test', './internal/mcpserver', '-run', '^TestLiveMCPReadWorkflow$', '-count=1', '-v')
    }
    finally {
        Pop-Location
    }
    $auditVerified = & psql -X -tA -v ON_ERROR_STOP=1 -h $postgresHost -p $postgresPort `
        -U $postgresUser -d $postgresDatabase -c @"
SELECT
    EXISTS (
        SELECT 1 FROM "$schema".audit_logs
        WHERE action = 'asset.listed' AND actor_type = 'agent'
          AND metadata->>'api_key_id' = '$mcpAPIKeyID'
    )
    AND EXISTS (
        SELECT 1 FROM "$schema".audit_logs
        WHERE action = 'transcript.listed' AND actor_type = 'agent'
          AND metadata->>'api_key_id' = '$mcpAPIKeyID'
    )
    AND EXISTS (
        SELECT 1 FROM "$schema".audit_logs
        WHERE action = 'transcript_revision.read' AND actor_type = 'agent'
          AND metadata->>'api_key_id' = '$mcpAPIKeyID'
    );
"@
    if (($auditVerified | Select-Object -Last 1).Trim() -ne 't') {
        throw 'Live MCP reads were not all attributed to the scoped API key in audit_logs.'
    }
    Invoke-RestMethod -Method Delete -Uri "$apiURL/api/v1/api-keys/$mcpAPIKeyID" `
        -Headers @{ Authorization = "Bearer $mcpSessionToken" } | Out-Null
    $revokedCredentialRejected = $false
    try {
        Invoke-WebRequest -Method Get -Uri "$apiURL/api/v1/assets?limit=1" `
            -Headers @{ Authorization = "Bearer $mcpServerToken" } -UseBasicParsing | Out-Null
    }
    catch {
        if ($null -ne $_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 401) {
            $revokedCredentialRejected = $true
        }
        else {
            throw
        }
    }
    if (-not $revokedCredentialRejected) {
        throw 'Revoked MCP API key was not rejected with HTTP 401.'
    }
    Invoke-RestMethod -Method Delete -Uri "$apiURL/api/v1/auth/session" `
        -Headers @{ Authorization = "Bearer $mcpSessionToken" } | Out-Null
    $mcpServerToken = $null
    $mcpSessionToken = $null
    $createdAPIKey = $null
    $login = $null
    Write-Host 'Live MCP E2E and read-audit verification passed.'
}
catch {
    foreach ($logPath in @($apiStderr, $workerStderr)) {
        if (Test-Path -LiteralPath $logPath) {
            Get-Content -LiteralPath $logPath -Tail 20
        }
    }
    throw
}
finally {
    Stop-ExactProcess $workerProcess
    Stop-ExactProcess $apiProcess
    if ($schemaCreated) {
        & psql -X -v ON_ERROR_STOP=1 -h $postgresHost -p $postgresPort -U $postgresUser `
            -d $postgresDatabase -c "DROP SCHEMA IF EXISTS `"$schema`" CASCADE" | Out-Null
    }

    $resolvedServerRoot = $serverRoot.TrimEnd([IO.Path]::DirectorySeparatorChar)
    $resolvedArtifactRoot = [IO.Path]::GetFullPath($artifactRoot)
    $expectedPrefix = $resolvedServerRoot + [IO.Path]::DirectorySeparatorChar
    if (
        $resolvedArtifactRoot.StartsWith($expectedPrefix, [StringComparison]::OrdinalIgnoreCase) -and
        [IO.Path]::GetFileName($resolvedArtifactRoot).StartsWith('live-console-e2e-')
    ) {
        if (Test-Path -LiteralPath $resolvedArtifactRoot) {
            [IO.Directory]::Delete($resolvedArtifactRoot, $true)
        }
    }
    else {
        throw 'Refusing to clean an unexpected live E2E artifact path.'
    }

    Remove-Item Env:PGPASSWORD, Env:PGSSLMODE, Env:DATABASE_URL,
        Env:VOICEASSET_HTTP_ADDR, Env:VOICEASSET_PUBLIC_ORIGIN,
        Env:VOICEASSET_COOKIE_SECURE, Env:VOICEASSET_STORAGE_PATH,
        Env:VOICEASSET_PROFILE_MASTER_KEY,
        Env:VOICEASSET_API_PROXY_TARGET, Env:VOICEASSET_CONSOLE_PORT,
        Env:VOICEASSET_LIVE_E2E,
        Env:VOICEASSET_LIVE_OWNER_EMAIL, Env:VOICEASSET_LIVE_OWNER_PASSWORD,
        Env:VOICE_ASSET_MCP_LIVE_E2E, Env:VOICE_ASSET_SERVER_URL,
        Env:VOICE_ASSET_SERVER_TOKEN `
        -ErrorAction SilentlyContinue
}
