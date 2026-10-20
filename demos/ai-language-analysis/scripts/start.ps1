param([string]$ListenHost="",[string]$DataDirectory="")
$ErrorActionPreference="Stop"
Set-Location (Split-Path $PSScriptRoot -Parent)
if ($ListenHost) { $env:DEMO_HOST=$ListenHost }
if ($DataDirectory) { $env:DEMO_DATA_DIR=$DataDirectory }
# Leave an unset port unset: the Go process loads .env with its own dotenv
# parser and applies 8090 only when neither environment nor .env provides it.
if ($env:DEMO_PORT) {
 $demoPort=[int]$env:DEMO_PORT
 if ($demoPort -lt 1 -or $demoPort -gt 65535) { throw "端口必须在1到65535之间" }
 $demoListeners=@(Get-NetTCPConnection -LocalPort $demoPort -State Listen -ErrorAction SilentlyContinue)
 if ($demoListeners.Count -gt 0) {
  $demoOwner=Get-Process -Id $demoListeners[0].OwningProcess -ErrorAction SilentlyContinue
  $demoOwnerName=if($demoOwner){$demoOwner.ProcessName}else{'其他程序'}
  throw "端口 $demoPort 正由 $demoOwnerName 使用。请在原后端窗口按 Ctrl+C 停止后，再运行本脚本；刷新网页不会切换后端。"
 }
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "请先安装 Go 1.26 或更高版本" }
New-Item -ItemType Directory -Force -Path bin | Out-Null
go build -o bin/teacher-report-demo.exe .
if ($LASTEXITCODE -ne 0) { throw "Go 编译失败" }
Write-Host "新版已编译，即将启动服务；终端会显示实际监听地址和报告规则版本。"
& ./bin/teacher-report-demo.exe
if ($LASTEXITCODE -ne 0) { throw "服务退出，错误码 $LASTEXITCODE" }
