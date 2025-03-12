param (
    [switch]$Clean,
    [switch]$Test,
    [switch]$BuildLinux,
    [switch]$BuildWindows,
    [switch]$Build,
    [switch]$Release,
    [string]$Version = ""
)

$BinaryName = "siebel_exporter"

# Get version from git if not specified
if (-not $Version) {
    try {
        $Version = (git describe --tags --always 2>$null)
        if (-not $Version) { $Version = "dev" }
    } catch {
        $Version = "dev"
    }
}

$BuildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd_HH:mm:ss")

function Clean {
    Write-Host "Cleaning..." -ForegroundColor Cyan
    if (Test-Path -Path "dist") {
        Remove-Item -Path "dist" -Recurse -Force
    }
    New-Item -Path "dist" -ItemType Directory -Force | Out-Null
}

function RunTests {
    Write-Host "Running tests..." -ForegroundColor Cyan
    go test ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Tests failed" -ForegroundColor Red
        exit 1
    }
}

function BuildLinux {
    Write-Host "Building for Linux AMD64..." -ForegroundColor Cyan
    $env:CGO_ENABLED = 0
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"

    go build  -o "dist/$BinaryName`_linux_amd64" ./cli
}

function BuildWindows {
    Write-Host "Building for Windows AMD64..." -ForegroundColor Cyan
    $env:CGO_ENABLED = 0
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"

    go build  -o "dist/$BinaryName`_windows_amd64.exe" ./cli
}

function CreateChecksums {
    Write-Host "Creating checksums file..." -ForegroundColor Cyan
    Push-Location dist
    try {
        $files = Get-ChildItem -Filter "$BinaryName`_*" -Exclude "*.sha256" | Select-Object -ExpandProperty Name
        $checksums = @()

        foreach ($file in $files) {
            $hash = (Get-FileHash -Algorithm SHA256 $file).Hash.ToLower()
            $checksums += "$hash  $file"
        }

        $checksums | Out-File -FilePath "checksums.txt" -Encoding ascii
    } finally {
        Pop-Location
    }
}

# Process commands
if ($Clean -or $Release) {
    Clean
}

if ($Test -or $Release) {
    RunTests
}

if ($BuildLinux -or $Build -or $Release) {
    BuildLinux
}

if ($BuildWindows -or $Build -or $Release) {
    BuildWindows
}

if ($Release) {
    CreateChecksums
    Write-Host "Done! Release artifacts are in the dist/ directory" -ForegroundColor Green
}

# Default action if no args specified
if (-not ($Clean -or $Test -or $BuildLinux -or $BuildWindows -or $Build -or $Release)) {
    Clean
    BuildLinux
    BuildWindows
    Write-Host "Done! Build artifacts are in the dist/ directory" -ForegroundColor Green
}