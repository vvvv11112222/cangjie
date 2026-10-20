param([switch]$Live,[ValidateSet('management','speaker','future-check','later-context','example-scope','joint-sources','teacher-reasoning','future-verification','definition-standard','wording-not-status','lecture-not-task','absence-summary','absence-title','absence-fact','absence-highlight','relation-direction','relation-condition','biology-cooking-meaning','biology-cross-block','biology-preview','biology-question-intent')][string]$CasePrefix)
$ErrorActionPreference='Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
$savedLive=$env:BAILIAN_LIVE_AUDIT_TEST
$savedReading=$env:BAILIAN_LIVE_READING_TEST
$savedPrefix=$env:BAILIAN_AUDIT_CASE_PREFIX
$savedTemp=$env:TEMP
$savedTmp=$env:TMP
try {
 $taskTemp=Join-Path (Get-Location) 'evaluation/2026-10-17-audit-calibration/tmp'
 New-Item -ItemType Directory -Path $taskTemp -Force | Out-Null
 $env:TEMP=$taskTemp
 $env:TMP=$taskTemp
 $env:BAILIAN_LIVE_READING_TEST='0'
 $env:BAILIAN_LIVE_AUDIT_TEST='0'
 $env:BAILIAN_AUDIT_CASE_PREFIX=$CasePrefix
 go test -run '^TestAuditCalibrationFixturesAndVerdict$' -count=1 .
 if ($LASTEXITCODE -ne 0) { throw 'Offline calibration checks failed.' }
 if ($Live) {
  $requestCount=if($CasePrefix){2}else{42}
  Write-Host "Live mode: $requestCount billable text-model requests; no retries and no ASR. Existing reports will not be changed."
  $env:BAILIAN_LIVE_AUDIT_TEST='1'
  go test -run '^TestLiveAuditCalibration$' -count=1 -timeout 56m -v .
  if ($LASTEXITCODE -ne 0) { throw 'Live expectations did not all match. Inspect the new evaluation run; do not retry blindly.' }
 } else {
  Write-Host 'Offline checks only. Use -Live explicitly to run the billable model evaluation.'
 }
} finally {
 $env:BAILIAN_LIVE_AUDIT_TEST=$savedLive
 $env:BAILIAN_LIVE_READING_TEST=$savedReading
 $env:BAILIAN_AUDIT_CASE_PREFIX=$savedPrefix
 $env:TEMP=$savedTemp
 $env:TMP=$savedTmp
}
