<#
.SYNOPSIS
  Installs (or upgrades) RepoMan as a Windows service.

.DESCRIPTION
  Run from the extracted release archive, in an elevated PowerShell ("Run as administrator"):

      Set-ExecutionPolicy -Scope Process Bypass -Force
      .\install.ps1                  # install or upgrade
      .\install.ps1 -OpenFirewall    # ...and allow inbound connections to the listen port
      .\install.ps1 -Uninstall       # remove the service and program files; data is kept

  Layout:
      C:\Program Files\RepoMan\repoman.exe     program
      C:\Program Files\RepoMan\repoman.env     settings (never overwritten on upgrade)
      C:\ProgramData\RepoMan                   data directory (database + artifacts), administrators only
      C:\ProgramData\RepoMan\logs\repoman.log  log

.PARAMETER InstallDir
  Program directory. Default: C:\Program Files\RepoMan

.PARAMETER DataDir
  Data directory. Default: C:\ProgramData\RepoMan

.PARAMETER Port
  Listen port written to a new repoman.env. Default: 8081
#>
[CmdletBinding()]
param(
  [string]$InstallDir = (Join-Path $env:ProgramFiles 'RepoMan'),
  [string]$DataDir = (Join-Path $env:ProgramData 'RepoMan'),
  [int]$Port = 8081,
  [switch]$OpenFirewall,
  [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
$ServiceName = 'RepoMan'
$FirewallRule = 'RepoMan (TCP-In)'

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not ([Security.Principal.WindowsPrincipal]$identity).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw 'Run this script from an elevated PowerShell window (right-click PowerShell, "Run as administrator").'
}

$exe = Join-Path $InstallDir 'repoman.exe'
$service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue

if ($Uninstall) {
  if ($service) {
    if ($service.Status -ne 'Stopped') { Stop-Service -Name $ServiceName -Force }
    if (Test-Path $exe) { & $exe service uninstall } else { sc.exe delete $ServiceName | Out-Null }
  }
  Get-NetFirewallRule -DisplayName $FirewallRule -ErrorAction SilentlyContinue | Remove-NetFirewallRule
  if (Test-Path $InstallDir) { Remove-Item -Recurse -Force $InstallDir }
  Write-Host "RepoMan removed. Data in $DataDir was kept; delete it yourself if you no longer need it."
  return
}

$source = Join-Path $PSScriptRoot 'repoman.exe'
if (-not (Test-Path $source)) { throw "repoman.exe was not found next to this script ($PSScriptRoot)." }
& $source version | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'repoman.exe does not run on this machine: download the archive for your CPU (amd64 for Intel/AMD, arm64 for ARM).' }

$upgrade = $false
if ($service) {
  $upgrade = $true
  if ($service.Status -ne 'Stopped') {
    Write-Host 'Stopping the running service for the upgrade'
    Stop-Service -Name $ServiceName -Force
    (Get-Service -Name $ServiceName).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(60))
  }
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item -Force $source $exe
foreach ($extra in 'README.md', 'LICENSE', 'CHANGELOG.md', 'SECURITY.md', 'repoman.env.example') {
  $p = Join-Path $PSScriptRoot $extra
  if (Test-Path $p) { Copy-Item -Force $p $InstallDir }
}
$docs = Join-Path $PSScriptRoot 'docs'
if (Test-Path $docs) { Copy-Item -Recurse -Force $docs $InstallDir }

# Data directory: the database holds password hashes, the signing key and upstream credentials,
# so only SYSTEM (the service account) and administrators may read it.
if (-not (Test-Path $DataDir)) {
  New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
  icacls $DataDir /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
}

$envFile = Join-Path $InstallDir 'repoman.env'
if (-not (Test-Path $envFile)) {
  $lines = @(
    '# RepoMan settings. See repoman.env.example and docs\configuration.md for every option.',
    '# Restart the service after editing:  Restart-Service RepoMan',
    "RM_ADDR=:$Port",
    "RM_DATA_DIR=$DataDir"
  )
  # ASCII: Windows PowerShell 5.1 would otherwise add a byte-order mark
  Set-Content -Path $envFile -Value $lines -Encoding Ascii
  Write-Host "Wrote $envFile"
} else {
  Write-Host "Kept existing $envFile"
}
# the settings file may hold RM_ADMIN_PASSWORD: administrators and SYSTEM only
icacls $envFile /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null

if (-not $upgrade) {
  & $exe service install
  if ($LASTEXITCODE -ne 0) { throw 'Registering the service failed.' }
}

# the port actually configured (an existing repoman.env wins over -Port)
$configured = (Get-Content $envFile | Where-Object { $_ -match '^\s*RM_ADDR\s*=' } | Select-Object -Last 1)
$listenPort = $Port
if ($configured -match ':(\d+)\s*$') { $listenPort = [int]$Matches[1] }

if ($OpenFirewall) {
  Get-NetFirewallRule -DisplayName $FirewallRule -ErrorAction SilentlyContinue | Remove-NetFirewallRule
  New-NetFirewallRule -DisplayName $FirewallRule -Direction Inbound -Action Allow -Protocol TCP -LocalPort $listenPort -Program $exe | Out-Null
  Write-Host "Windows Firewall: inbound TCP $listenPort allowed for repoman.exe"
}

Start-Service -Name $ServiceName

# Start-Process keeps the probe's error output away from PowerShell's error handling
$healthy = $false
foreach ($i in 1..20) {
  $probe = Start-Process -FilePath $exe -ArgumentList 'healthcheck', '--config', ('"{0}"' -f $envFile) -Wait -PassThru -WindowStyle Hidden
  if ($probe.ExitCode -eq 0) { $healthy = $true; break }
  Start-Sleep -Seconds 1
}

Write-Host ''
& $exe version
if ($healthy) { Write-Host 'RepoMan is running.' }
else { Write-Host "RepoMan was installed but is not answering yet. Check the log: $DataDir\logs\repoman.log" }
Write-Host "  Open:      http://localhost:$listenPort"
Write-Host "  Settings:  $envFile   (then: Restart-Service RepoMan)"
Write-Host "  Logs:      $DataDir\logs\repoman.log"
$initial = Join-Path $DataDir 'initial-admin-password'
if ((-not $upgrade) -and (Test-Path $initial)) {
  Write-Host "  Sign in as ""admin"" with the password in $initial"
  Write-Host "             Get-Content '$initial'"
}
if (-not $OpenFirewall) {
  Write-Host '  Other machines cannot connect until Windows Firewall allows the port: re-run with -OpenFirewall.'
}
