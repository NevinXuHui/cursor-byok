# Windows dev/build environment bootstrap for cursor-byok.
# Usage (from repo root):
#   powershell -ExecutionPolicy Bypass -File .\scripts\setup-windows.ps1
#   powershell -ExecutionPolicy Bypass -File .\scripts\setup-windows.ps1 -BuildOnly

param(
    [switch]$BuildOnly,
    [switch]$SkipFrontend,
    [string]$Output = "bin/windows-64.exe",
    [string]$Version = "0.0.39"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root

$ProtocDir = Join-Path $env:USERPROFILE ".local\tools\protoc"
$ProtocBin = Join-Path $ProtocDir "bin"
$GoBin = Join-Path $env:USERPROFILE "go\bin"
$env:PATH = "$ProtocBin;$GoBin;$env:PATH"
$env:GOPROXY = "https://goproxy.cn,direct"

function Ensure-Protoc {
    if (Get-Command protoc -ErrorAction SilentlyContinue) {
        Write-Host "protoc: $(protoc --version)"
        return
    }
    Write-Host "Installing protoc 29.3 to $ProtocDir ..."
    New-Item -ItemType Directory -Force -Path $ProtocDir | Out-Null
    $zip = Join-Path $env:TEMP "protoc-29.3-win64.zip"
    Invoke-WebRequest -Uri "https://github.com/protocolbuffers/protobuf/releases/download/v29.3/protoc-29.3-win64.zip" -OutFile $zip -UseBasicParsing
    Expand-Archive -Path $zip -DestinationPath $ProtocDir -Force
    if (-not (Get-Command protoc -ErrorAction SilentlyContinue)) {
        throw "protoc install failed"
    }
    Write-Host "protoc: $(protoc --version)"
}

function Ensure-GoTools {
    Write-Host "Installing Go toolchain plugins ..."
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
    go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
    go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.74
    $env:PATH = "$GoBin;$env:PATH"
    if (-not (Get-Command wails3 -ErrorAction SilentlyContinue)) {
        throw "wails3 install failed; check $GoBin is on PATH"
    }
    Write-Host "wails3: $(wails3 version 2>&1 | Select-Object -First 1)"
}

function Invoke-ProtoGenerate {
    Write-Host "Generating proto ..."
    Remove-Item -Recurse -Force gen -ErrorAction SilentlyContinue
    protoc -I ./proto --go_out=. --go_opt=module=cursor --connect-go_out=. --connect-go_opt=module=cursor ./proto/agent_v1.proto ./proto/aiserver_v1.proto
    Get-ChildItem -Recurse gen -Filter *.go | ForEach-Object { gofmt -w $_.FullName }
}

function Invoke-GoModTidy {
    Write-Host "go mod tidy ..."
    go mod tidy
}

function Invoke-BindingsGenerate {
    Write-Host "Generating Wails bindings ..."
    wails3 generate bindings -f "-tags production" -clean=true
}

function Invoke-FrontendBuild {
    if ($SkipFrontend) { return }
    Write-Host "Building frontend ..."
    Push-Location frontend
    try {
        if (-not (Test-Path node_modules)) {
            npm install
        }
        $env:PRODUCTION = "true"
        npm run build
    } finally {
        Pop-Location
    }
    if (-not (Test-Path "frontend/dist/index.html")) {
        throw "frontend/dist missing after build"
    }
}

function Invoke-WindowsResources {
    Write-Host "Generating Windows icon/syso ..."
    Push-Location build
    try {
        wails3 generate icons -input appicon.png -macfilename darwin/icons.icns -windowsfilename windows/icon.ico
        wails3 generate syso -arch amd64 -icon windows/icon.ico -manifest windows/wails.exe.manifest -info windows/info.json -out ../wails_windows_amd64.syso
    } finally {
        Pop-Location
    }
}

function Invoke-GoBuild {
    Write-Host "Building $Output ..."
    New-Item -ItemType Directory -Force -Path (Split-Path $Output) | Out-Null
    go build -tags production -trimpath -buildvcs=false `
        -ldflags="-w -s -H windowsgui -X cursor/internal/buildinfo.Version=$Version" `
        -o $Output .
    Write-Host "OK: $(Resolve-Path $Output)"
}

if (-not $BuildOnly) {
    Ensure-Protoc
    Ensure-GoTools
    Invoke-ProtoGenerate
    Invoke-GoModTidy
    Invoke-BindingsGenerate
}

Invoke-FrontendBuild
Invoke-WindowsResources
Invoke-GoBuild

Write-Host ""
Write-Host "Done. Run: .\$Output"
