param(
    [string]$Distribution,
    [string]$ArtifactDirectory,
    [string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
$repoDirectory = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$wslArguments = @()
if ($Distribution) { $wslArguments += @('--distribution', $Distribution) }

if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) {
    throw 'WSL 2 with a Linux distribution is required. See PK_MAINTENANCE.md; no tests ran.'
}
& wsl.exe @wslArguments --exec /bin/true
if ($LASTEXITCODE -ne 0) { throw 'WSL Linux is not ready. Install/start a distribution; no tests ran.' }

function Convert-ToWslPath([string]$Path) {
    $absolutePath = [System.IO.Path]::GetFullPath($Path)
    $converted = & wsl.exe @wslArguments --exec wslpath -a -u $absolutePath
    if ($LASTEXITCODE -ne 0 -or -not $converted) { throw "Cannot map path into WSL: $absolutePath" }
    return ($converted | Select-Object -Last 1).Trim()
}

$runner = (Convert-ToWslPath $repoDirectory) + '/scripts/test-local.py'
$testArguments = @($runner)
if ($ArtifactDirectory) { $testArguments += @('--artifact-dir', (Convert-ToWslPath $ArtifactDirectory)) }
if ($OutputDirectory) { $testArguments += @('--output-dir', (Convert-ToWslPath $OutputDirectory)) }

# Execute argument arrays directly: paths never become shell command text.
& wsl.exe @wslArguments --exec python3 @testArguments
exit $LASTEXITCODE
