# Test install.ps1 against a fake release served from a local directory.
#
# Usage: pwsh scripts/test-install.ps1 -Binary <path>
#   <path> is an aegis-mcp binary for this machine. It is published in the fake
#   release under the Windows asset name, so on Linux or macOS the same script
#   tests everything but the Windows-only PATH handling.
# Needs python3 (or python) for the local web servers (scripts/test-server.py).
param([Parameter(Mandatory)] [string] $Binary)
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
$asset = 'aegis-mcp-windows-amd64.exe'
# On Windows, python3 may be the Store's install prompt rather than Python.
$python = if ($IsWindows) { 'python' } else { 'python3' }
$procs = @()

function Fail($msg) { throw "FAIL: $msg" }
function Pass($msg) { Write-Host "ok: $msg" }

# Start scripts/test-server.py and return its URL once it has answered.
function Start-Server($name, $mode, $dir) {
    $portFile = Join-Path $work "$name.port"
    $opts = @{ FilePath = $python; ArgumentList = @((Join-Path $PSScriptRoot 'test-server.py'), $mode, $dir, $portFile); PassThru = $true }
    if ($IsWindows) { $opts.WindowStyle = 'Hidden' }   # -WindowStyle exists only on Windows
    $script:procs += Start-Process @opts
    for ($i = 0; -not (Test-Path $portFile); $i++) {
        if ($i -ge 100) { Fail "$name server did not start" }
        Start-Sleep -Milliseconds 100
    }
    $url = "http://127.0.0.1:$((Get-Content -Raw $portFile).Trim())"
    # Any HTTP answer (a 401 from the fake Aegis included) means it is serving.
    try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 10 "$url/" | Out-Null }
    catch { if (-not $_.Exception.Response) { Fail "$name server does not answer: $_" } }
    return $url
}

# Run install.ps1 in a fresh PowerShell with only the given variables set.
function Install($vars) {
    $env_ = @{
        AEGIS_MCP_INSTALL_DIR = (Join-Path $work 'bin'); AEGIS_MCP_CONFIG = (Join-Path $work 'config.json')
        AEGIS_MCP_RELEASES_URL = ''; AEGIS_MCP_NO_SETUP = ''; AEGIS_MCP_VERSION = ''; AEGIS_SERVER = ''; AEGIS_TOKEN = ''
    }
    foreach ($k in $vars.Keys) { $env_[$k] = $vars[$k] }
    $saved = @{}
    foreach ($k in $env_.Keys) { $saved[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, $env_[$k]) }
    try {
        $out = & pwsh -NoProfile -NonInteractive -File (Join-Path $root 'install.ps1') 2>&1 | Out-String
        return @{ Code = $LASTEXITCODE; Out = $out }
    }
    finally {
        foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
    }
}

try {
    $good = Join-Path $work 'good/latest/download'
    $bad = Join-Path $work 'bad/latest/download'
    New-Item -ItemType Directory -Force $good, $bad | Out-Null
    Copy-Item $Binary (Join-Path $good $asset)
    Copy-Item $Binary (Join-Path $bad $asset)
    $hash = (Get-FileHash -Algorithm SHA256 (Join-Path $good $asset)).Hash.ToLower()
    Set-Content -NoNewline -Path (Join-Path $good 'SHA256SUMS') -Value "$hash  $asset`n"
    Set-Content -NoNewline -Path (Join-Path $bad 'SHA256SUMS') -Value ("0" + $hash.Substring(1) + "  $asset`n")
    $goodUrl = Start-Server 'good' 'files' (Join-Path $work 'good')
    $badUrl = Start-Server 'bad' 'files' (Join-Path $work 'bad')
    $aegisUrl = Start-Server 'aegis' 'aegis' '-'

    # 1. Install only.
    $r = Install @{ AEGIS_MCP_RELEASES_URL = $goodUrl; AEGIS_MCP_NO_SETUP = '1' }
    if ($r.Code -ne 0 -or $r.Out -notmatch 'Checksum verified' -or $r.Out -notmatch "aegis-mcp setup") { Fail "install: $($r.Out)" }
    $installed = Join-Path $work 'bin/aegis-mcp.exe'
    if (-not (Test-Path $installed)) { Fail 'binary not installed' }
    Pass 'installs and explains the next step'

    if ($IsWindows) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        if (-not (($userPath -split ';') -contains (Join-Path $work 'bin'))) { Fail 'install dir not added to the user PATH' }
        Pass 'adds the install directory to the user PATH'
        # Leave the runner's PATH as it was.
        [Environment]::SetEnvironmentVariable('Path', (($userPath -split ';') -ne (Join-Path $work 'bin')) -join ';', 'User')
    }

    # 2. A damaged download is refused.
    Remove-Item $installed
    $r = Install @{ AEGIS_MCP_RELEASES_URL = $badUrl; AEGIS_MCP_NO_SETUP = '1' }
    if ($r.Code -eq 0 -or $r.Out -notmatch 'Checksum mismatch') { Fail "bad checksum accepted: $($r.Out)" }
    if (Test-Path $installed) { Fail 'binary installed despite a bad checksum' }
    Pass 'refuses a download that does not match SHA256SUMS'

    # 3. Scripted setup.
    $r = Install @{ AEGIS_MCP_RELEASES_URL = $goodUrl; AEGIS_SERVER = $aegisUrl; AEGIS_TOKEN = 'good' }
    if ($r.Code -ne 0 -or $r.Out -notmatch "Signed in to $([regex]::Escape($aegisUrl)) as ada") { Fail "scripted setup: $($r.Out)" }
    if ((Get-Content -Raw (Join-Path $work 'config.json')) -notmatch '"token": "good"') { Fail 'config file not written' }
    Pass 'runs setup without questions when AEGIS_SERVER and AEGIS_TOKEN are set'

    Write-Host 'install.ps1: all tests passed'
}
finally {
    $procs | ForEach-Object { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue }
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
