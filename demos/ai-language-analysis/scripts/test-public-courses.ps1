param([switch]$Live,[ValidateSet('physics','biology')][string]$Sample)
$ErrorActionPreference='Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
$names=@('BAILIAN_LIVE_PUBLIC_COURSE_TEST','BAILIAN_LIVE_READING_TEST','BAILIAN_LIVE_AUDIT_TEST','BAILIAN_PUBLIC_COURSE_SAMPLE','TEMP','TMP','GOTMPDIR','GOCACHE')
$saved=@{}
foreach($name in $names){$saved[$name]=[Environment]::GetEnvironmentVariable($name,'Process')}
try {
 $taskTemp=Join-Path (Get-Location) 'evaluation/2026-10-17-public-regression/tmp'
 New-Item -ItemType Directory -Path $taskTemp -Force | Out-Null
 $env:TEMP=$taskTemp
 $env:TMP=$taskTemp
 $env:GOTMPDIR=$taskTemp
 $env:GOCACHE=Join-Path $taskTemp 'go-cache'
 $env:BAILIAN_LIVE_PUBLIC_COURSE_TEST='0'
 $env:BAILIAN_LIVE_READING_TEST='0'
 $env:BAILIAN_LIVE_AUDIT_TEST='0'
 $env:BAILIAN_PUBLIC_COURSE_SAMPLE=$Sample
 go test -run '^TestPublicCourse' -count=1 .
 if ($LASTEXITCODE -ne 0) { throw 'Offline public-course checks failed; live run not started.' }
 if ($Live) {
  $requestCount=if($Sample){4}else{8}
  Write-Host "Live comparison: at most $requestCount billable text requests, no ASR or automatic retries. Separate artifacts only; running services and manual reviews are untouched."
  $env:BAILIAN_LIVE_PUBLIC_COURSE_TEST='1'
  go test -run '^TestLivePublicCourseComparison$' -count=1 -timeout 26m -v .
  if ($LASTEXITCODE -ne 0) { throw 'Live comparison was incomplete. Inspect saved artifacts; do not retry blindly.' }
 } else {
  Write-Host 'Offline checks only. -Live explicitly enables the two-sample model comparison.'
 }
} finally {
 foreach($name in $names){[Environment]::SetEnvironmentVariable($name,$saved[$name],'Process')}
}
