param([string]$PythonExecutable="")
$ErrorActionPreference="Stop"
Set-Location (Split-Path $PSScriptRoot -Parent)
$env:PYTHONDONTWRITEBYTECODE="1"
if (-not $PythonExecutable) {
 if (Test-Path 'D:\anaconda\python.exe') { $PythonExecutable='D:\anaconda\python.exe' }
 else { $PythonExecutable=(Get-Command python -ErrorAction Stop).Source }
}
& $PythonExecutable -m unittest discover -s integration -p 'test_*.py' -v
if ($LASTEXITCODE -ne 0) { throw "Contract and frozen-reference tests failed" }
& $PythonExecutable -m unittest discover -s integration/upstream/tools -p test_quality.py -v
if ($LASTEXITCODE -ne 0) { throw "Upstream quality calculator tests failed" }
