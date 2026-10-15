# 一键开发运行：打包前端 -> 编译后端（含前端产物）-> 启动服务
# 用法：.\dev.ps1              # 默认 token=admin, addr=:8000
#       .\dev.ps1 -Token xxx -Addr :9000
#       .\dev.ps1 -SkipWeb    # 前端没改时跳过 npm build
param(
    [string]$Token = 'admin',
    [string]$Addr = ':8000',
    [switch]$SkipWeb
)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot

# 0. 端口被占用时提示先停旧进程（替换运行中的 exe 会损坏文件）
$conn = Get-NetTCPConnection -LocalPort $Addr.TrimStart(':') -State Listen -ErrorAction SilentlyContinue
if ($conn) {
    $p = Get-Process -Id $conn[0].OwningProcess -ErrorAction SilentlyContinue
    Write-Host "端口 $Addr 已被占用（PID $($conn[0].OwningProcess)，进程 $($p.ProcessName)）。请先停止旧进程再运行。" -ForegroundColor Red
    exit 1
}

# 1. 前端构建
if (-not $SkipWeb) {
    Write-Host '==> npm run build (web)' -ForegroundColor Cyan
    Push-Location "$root\web"
    npm run build
    if ($LASTEXITCODE -ne 0) { Pop-Location; throw '前端构建失败' }
    Pop-Location
} else {
    Write-Host '==> 跳过前端构建 (-SkipWeb)' -ForegroundColor DarkGray
}

# 2. 后端编译（go:embed 嵌入 web/dist）
Write-Host '==> go build (embed web/dist)' -ForegroundColor Cyan
go build -ldflags "-s -w -X main.version=dev" -o "$root\bin\adbreeze.exe" "$root\cmd\adbreeze"
if ($LASTEXITCODE -ne 0) { throw '后端编译失败' }

# 3. 启动（前台运行，Ctrl+C 退出）
Write-Host "==> 启动 http://localhost:$($Addr.TrimStart(':'))  (Token: $Token)" -ForegroundColor Green
& "$root\bin\adbreeze.exe" -addr $Addr -token $Token
