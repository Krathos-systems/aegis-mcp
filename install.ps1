# Install aegis-mcp on Windows, then run `aegis-mcp setup`.
#
#   irm https://github.com/Krathos-systems/aegis-mcp/releases/latest/download/install.ps1 | iex
#
# Options, as environment variables:
#   AEGIS_MCP_VERSION      a release tag to install instead of the latest (e.g. v0.1.0)
#   AEGIS_MCP_INSTALL_DIR  where to put the binary (default %LOCALAPPDATA%\Programs\aegis-mcp)
#   AEGIS_MCP_NO_SETUP=1   install only; run `aegis-mcp setup` yourself later
#   AEGIS_SERVER, AEGIS_TOKEN
#                          set both to run setup without questions (for scripts)
#
# Installs for the current user only; never needs an elevated prompt.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # the progress bar makes downloads far slower

$repoUrl = if ($env:AEGIS_MCP_RELEASES_URL) { $env:AEGIS_MCP_RELEASES_URL } else { 'https://github.com/Krathos-systems/aegis-mcp/releases' }
$version = if ($env:AEGIS_MCP_VERSION) { $env:AEGIS_MCP_VERSION } else { 'latest' }
$installDir = if ($env:AEGIS_MCP_INSTALL_DIR) { $env:AEGIS_MCP_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\aegis-mcp' }

if (-not [Environment]::Is64BitOperatingSystem) { throw 'aegis-mcp needs 64-bit Windows.' }
$asset = 'aegis-mcp-windows-amd64.exe'
$base = if ($version -eq 'latest') { "$repoUrl/latest/download" } else { "$repoUrl/download/$version" }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Downloading $asset ($version)..."
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset)
    Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS')

    $want = $null
    foreach ($line in Get-Content (Join-Path $tmp 'SHA256SUMS')) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -eq 2 -and $parts[1].TrimStart('*') -eq $asset) { $want = $parts[0].ToLower() }
    }
    if (-not $want) { throw "SHA256SUMS has no entry for $asset" }
    $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $asset)).Hash.ToLower()
    if ($got -ne $want) { throw "Checksum mismatch for ${asset}: the download is damaged or was altered." }
    Write-Host 'Checksum verified'

    New-Item -ItemType Directory -Force -Path $installDir | Out-Null
    $dest = Join-Path $installDir 'aegis-mcp.exe'
    Move-Item -Force (Join-Path $tmp $asset) $dest
    # Only matters when this script is tested under PowerShell on Linux or macOS.
    if ($IsLinux -or $IsMacOS) { chmod 755 $dest }
    Write-Host "Installed $(& $dest --version) to $dest"
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# Put the install directory on the user's PATH, for new terminals and this one.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $installDir)) {
    $newPath = if ($userPath) { "$($userPath.TrimEnd(';'));$installDir" } else { $installDir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    Write-Host "Added $installDir to your PATH (open a new terminal to use it there)"
}
if (-not (($env:Path -split ';') -contains $installDir)) { $env:Path = "$env:Path;$installDir" }

if ($env:AEGIS_MCP_NO_SETUP -eq '1') {
    Write-Host "Next: run 'aegis-mcp setup' to connect it to Aegis."
}
elseif ($env:AEGIS_SERVER -and $env:AEGIS_TOKEN) {
    Write-Host ''
    $env:AEGIS_TOKEN | & $dest setup --server $env:AEGIS_SERVER --token-stdin
}
else {
    Write-Host ''
    & $dest setup
}
