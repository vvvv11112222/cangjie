param(
    [string]$DataRoot = (Join-Path $PSScriptRoot '..\var\team-web'),
    [switch]$NoBrowser
)
$ErrorActionPreference = 'Stop'
$DataRoot = [IO.Path]::GetFullPath($DataRoot)
$configFile = Join-Path $DataRoot 'private.json'
if (!(Test-Path -LiteralPath $configFile)) { throw 'Run the documented team_web.py prepare command first.' }
$config = Get-Content -LiteralPath $configFile -Raw -Encoding UTF8 | ConvertFrom-Json

function Ensure-AsciiAlias([string]$Target) {
    if ($Target -notmatch '[^\x00-\x7F]') { return }
    # Reuse only our marked directory; never replace existing drive mappings.
    $marker = Join-Path $Target '.team-web-alias-owner'
    if (!(Test-Path -LiteralPath $marker)) { [IO.File]::WriteAllText($marker, [guid]::NewGuid().ToString()) }
    $owner = [IO.File]::ReadAllText($marker)
    foreach ($letter in @('V','W','Y','Z')) {
        $drive = $letter + ':\'
        if (Test-Path -LiteralPath $drive) {
            $candidate = Join-Path $drive '.team-web-alias-owner'
            if ((Test-Path -LiteralPath $candidate) -and [IO.File]::ReadAllText($candidate) -eq $owner) { return }
        }
    }
    foreach ($letter in @('V','W','Y','Z')) {
        $drive = $letter + ':\'
        if (Test-Path -LiteralPath $drive) { continue }
        & subst.exe ($letter + ':') $Target
        if ($LASTEXITCODE -eq 0) { return }
    }
    throw 'No free project drive alias; existing mappings were preserved.'
}
$runtimeRoot = [IO.Path]::GetDirectoryName([IO.Path]::GetDirectoryName([IO.Path]::GetDirectoryName($config.go)))
Ensure-AsciiAlias $runtimeRoot
Ensure-AsciiAlias $DataRoot
& $config.python -X utf8 (Join-Path $PSScriptRoot 'team_web.py') --data-root $DataRoot start-detached
if ($LASTEXITCODE -ne 0) { throw 'Website startup failed; inspect local deployment logs.' }
if (!$NoBrowser) { Start-Process 'http://127.0.0.1:8088/#/sessions' }
Write-Output 'Team website: http://127.0.0.1:8088/#/sessions'
