@echo off
setlocal
chcp 65001 >nul
set "TEAM_WEB_DATA=%~1"
if not defined TEAM_WEB_DATA set "TEAM_WEB_DATA=%~dp0..\var\team-web"
set "TEAM_WEB_CONFIG=%TEAM_WEB_DATA%\private.json"
if not exist "%TEAM_WEB_CONFIG%" (
    echo Team website configuration was not found. Start the team website first.
    set "SETUP_EXIT=1"
    goto finish
)
if not defined TEAM_PYTHON for /f "usebackq delims=" %%P in (`powershell.exe -NoProfile -Command "try { [Console]::OutputEncoding=[Text.Encoding]::UTF8; (ConvertFrom-Json -InputObject (Get-Content -LiteralPath $env:TEAM_WEB_CONFIG -Raw -Encoding UTF8 -ErrorAction Stop)).python } catch { exit 1 }"`) do set "TEAM_PYTHON=%%P"
if not defined TEAM_PYTHON (
    echo Could not read the configured Python path. Check the local website configuration.
    set "SETUP_EXIT=1"
    goto finish
)
if not exist "%TEAM_PYTHON%" (
    echo The configured Python runtime was not found. Please repair the website runtime.
    set "SETUP_EXIT=1"
    goto finish
)
"%TEAM_PYTHON%" -X utf8 "%~dp0setup_models.py" --data-root "%TEAM_WEB_DATA%"
set "SETUP_EXIT=%ERRORLEVEL%"
:finish
if not defined TEAM_MODEL_SETUP_NO_PAUSE pause
exit /b %SETUP_EXIT%
