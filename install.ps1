# SPDX-License-Identifier: MIT
# Installs µPi (upi.exe) from a GitHub release of Code000-211/microPi on Windows x64:
#
#   powershell -c "irm https://github.com/Code000-211/microPi/releases/latest/download/install.ps1 | iex"
#
# It downloads upi-windows-amd64.exe and the release's SHA256SUMS, checks the executable against it, installs it as
# $env:MICROPI_BIN_DIR\upi.exe (default %USERPROFILE%\.micropi\bin) and adds that folder to the user PATH.
#
#   MICROPI_VERSION   a release tag such as upi-v0.2.1 (default: the latest release)
#   MICROPI_BIN_DIR   where upi.exe goes
$ErrorActionPreference = 'Stop'

$repo = 'https://github.com/Code000-211/microPi'
$version = if ($env:MICROPI_VERSION) { $env:MICROPI_VERSION } else { 'latest' }
$binDir = if ($env:MICROPI_BIN_DIR) { $env:MICROPI_BIN_DIR } else { Join-Path $env:USERPROFILE '.micropi\bin' }

if ($version -eq 'latest') {
    $base = "$repo/releases/latest/download"
} elseif ($version -match '^upi-v\d+\.\d+\.\d+$') {
    $base = "$repo/releases/download/$version"
} else {
    throw 'MICROPI_VERSION must be a release tag such as upi-v0.2.1'
}
if (-not [Environment]::Is64BitOperatingSystem) { throw 'µPi needs 64-bit Windows' }

$name = 'upi-windows-amd64.exe'
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("micropi-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Force $tmp | Out-Null
try {
    Write-Host "Downloading $name ($version) ..."
    $ProgressPreference = 'SilentlyContinue'
    Invoke-WebRequest "$base/SHA256SUMS" -OutFile (Join-Path $tmp 'SHA256SUMS') -UseBasicParsing
    Invoke-WebRequest "$base/$name" -OutFile (Join-Path $tmp 'upi.exe') -UseBasicParsing

    $line = Get-Content (Join-Path $tmp 'SHA256SUMS') | Where-Object { ($_ -split '\s+')[1] -in @($name, "*$name") } | Select-Object -First 1
    if (-not $line) { throw "SHA256SUMS has no line for $name" }
    $want = ($line -split '\s+')[0].ToLowerInvariant()
    $got = (Get-FileHash (Join-Path $tmp 'upi.exe') -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $want) { throw "checksum mismatch for $name (expected $want, got $got): nothing was installed" }

    New-Item -ItemType Directory -Force $binDir | Out-Null
    Move-Item -Force (Join-Path $tmp 'upi.exe') (Join-Path $binDir 'upi.exe')
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $binDir)) {
    [Environment]::SetEnvironmentVariable('Path', (($userPath, $binDir) -join ';').Trim(';'), 'User')
    Write-Host "Added $binDir to your user PATH (open a new terminal)."
}
Write-Host "Installed $(& (Join-Path $binDir 'upi.exe') --version) as $(Join-Path $binDir 'upi.exe')"
