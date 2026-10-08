param(
    [string]$Distribution,
    [string]$ArtifactDirectory,
    [string]$OutputDirectory,
    [string]$BindCommit,
    [string]$Report,
    [switch]$PublishEvidence
)

$ErrorActionPreference = 'Stop'
if ($BindCommit) {
    if (-not $Report -or $ArtifactDirectory -or $OutputDirectory) {
        throw '-BindCommit requires -Report and cannot be combined with -ArtifactDirectory or -OutputDirectory.'
    }
} elseif ($Report -or $PublishEvidence) {
    throw '-Report and -PublishEvidence require -BindCommit.'
}
$repoDirectory = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$wslArguments = @()
if ($Distribution) { $wslArguments += @('--distribution', $Distribution) }

if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) {
    throw 'WSL 2 with a Linux distribution is required. See PK_MAINTENANCE.md; no tests ran.'
}
& wsl.exe @wslArguments --exec /bin/true
if ($LASTEXITCODE -ne 0) { throw 'WSL Linux is not ready. Install/start a distribution; no tests ran.' }

function Convert-ToWslPath([string]$Path) {
    $absolutePath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
    $converted = & wsl.exe @wslArguments --exec wslpath -a -u $absolutePath
    if ($LASTEXITCODE -ne 0 -or -not $converted) { throw "Cannot map path into WSL: $absolutePath" }
    return ($converted | Select-Object -Last 1).Trim()
}

$runner = (Convert-ToWslPath $repoDirectory) + '/scripts/test-local.py'
$testArguments = @($runner)
if ($ArtifactDirectory) { $testArguments += @('--artifact-dir', (Convert-ToWslPath $ArtifactDirectory)) }
if ($OutputDirectory) { $testArguments += @('--output-dir', (Convert-ToWslPath $OutputDirectory)) }
if ($BindCommit) { $testArguments += @('--bind-commit', $BindCommit, '--report', (Convert-ToWslPath $Report)) }
if ($PublishEvidence) { $testArguments += '--publish-evidence' }

# Execute argument arrays directly: paths never become shell command text.
& wsl.exe @wslArguments --exec python3 @testArguments
exit $LASTEXITCODE
