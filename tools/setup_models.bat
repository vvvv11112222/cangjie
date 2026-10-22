@echo off
setlocal
chcp 65001 >nul
set "TEAM_WEB_DATA=%~1"
if not defined TEAM_WEB_DATA set "TEAM_WEB_DATA=%~dp0..\var\team-web"
set "TEAM_WEB_CONFIG=%TEAM_WEB_DATA%\private.json"
if not exist "%TEAM_WEB_CONFIG%" (
    echo Please prepare and start the team website first.
    pause
    exit /b 1
)
if not defined TEAM_PYTHON for /f "usebackq delims=" %%P in (`powershell -NoProfile -Command "[Console]::OutputEncoding=[Text.Encoding]::UTF8; (ConvertFrom-Json -InputObject (Get-Content -LiteralPath $env:TEAM_WEB_CONFIG -Raw -Encoding UTF8)).python"`) do set "TEAM_PYTHON=%%P"
if not defined TEAM_PYTHON exit /b 1
"%TEAM_PYTHON%" -X utf8 "%~dp0setup_models.py" --data-root "%TEAM_WEB_DATA%"
set "SETUP_EXIT=%ERRORLEVEL%"
pause
exit /b %SETUP_EXIT%
