# mdviewer installer for Windows PowerShell
# Usage: iex (iwr 'https://raw.githubusercontent.com/roraja/markdown-go/master/install.ps1').Content
$ErrorActionPreference = 'Stop'

$repo = 'roraja/markdown-go'
$binary = 'mdviewer'

$arch = if ([Environment]::Is64BitOperatingSystem) { 'amd64' } else { 'unsupported' }
if ($arch -eq 'unsupported') { Write-Error 'Only 64-bit Windows is supported'; exit 1 }

$latest = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
if (-not $latest) { Write-Error 'Failed to fetch latest release'; exit 1 }

$asset = "$binary-windows-$arch.exe"
$url = "https://github.com/$repo/releases/download/$latest/$asset"
$installDir = "$env:LOCALAPPDATA\mdviewer"
$installPath = "$installDir\$binary.exe"

Write-Host "Installing mdviewer $latest (windows/$arch)..."
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
Invoke-WebRequest -Uri $url -OutFile $installPath

# Add to PATH if not already
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$installDir", 'User')
    Write-Host "Added $installDir to PATH (restart terminal to take effect)"
}

Write-Host "✅ mdviewer $latest installed to $installPath"
Write-Host "   Run: mdviewer -root C:\your-notes -port 8080"
