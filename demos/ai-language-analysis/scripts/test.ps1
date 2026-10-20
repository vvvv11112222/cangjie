param([string]$NodeExecutable="")
$ErrorActionPreference="Stop"
Set-Location (Split-Path $PSScriptRoot -Parent)
$env:BAILIAN_LIVE_READING_TEST="0"
$env:BAILIAN_LIVE_AUDIT_TEST="0"
$env:BAILIAN_LIVE_PUBLIC_COURSE_TEST="0"
go test ./...
if ($LASTEXITCODE -ne 0) { throw "Go 测试失败" }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw "Go 静态检查失败" }
if (-not $NodeExecutable) {
 if (Test-Path 'D:\Program Files\nodejs\node.exe') { $NodeExecutable='D:\Program Files\nodejs\node.exe' }
 else { $NodeExecutable=(Get-Command node -ErrorAction Stop).Source }
}
& $NodeExecutable --test tests/transcript.test.cjs tests/recovery.test.cjs tests/analysis.test.cjs tests/delivery.test.cjs
if ($LASTEXITCODE -ne 0) { throw "前端测试失败" }
& $NodeExecutable --check web/jobs.js
if ($LASTEXITCODE -ne 0) { throw "任务页面语法检查失败" }
& $NodeExecutable --check web/recovery.js
if ($LASTEXITCODE -ne 0) { throw "恢复逻辑语法检查失败" }
& $NodeExecutable --check web/analysis.js
if ($LASTEXITCODE -ne 0) { throw "六维报告页面语法检查失败" }
& $NodeExecutable --check web/events.js
if ($LASTEXITCODE -ne 0) { throw "课堂依据页面语法检查失败" }
