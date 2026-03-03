$ErrorActionPreference = "Stop"

$repo = "linanwx/nagobot"
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$version = $release.tag_name
$url = "https://github.com/$repo/releases/download/$version/nagobot-windows-amd64.exe"

$dir = "$env:LOCALAPPDATA\nagobot"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$bin = "$dir\nagobot.exe"

Write-Host "Downloading nagobot $version..."
Invoke-WebRequest -Uri $url -OutFile $bin

# Add to user PATH if not present
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$dir*") {
    [Environment]::SetEnvironmentVariable("Path", "$dir;$userPath", "User")
    Write-Host "Added $dir to PATH (restart terminal to take effect)"
}

Write-Host "Binary installed. Registering system service..."
& $bin install
