@echo off
setlocal
chcp 65001 >nul
set "TEAM_WEB_DATA=%~1"
if not defined TEAM_WEB_DATA set "TEAM_WEB_DATA=%~dp0..\var\team-web"
if not defined TEAM_PYTHON set "TEAM_PYTHON=python"
if not exist "%TEAM_WEB_DATA%\private.json" (
    echo Run the documented prepare command first.
    exit /b 1
)
"%TEAM_PYTHON%" -X utf8 "%~dp0team_web.py" --data-root "%TEAM_WEB_DATA%" startup-paths >nul
if errorlevel 1 exit /b 1
"%TEAM_PYTHON%" -X utf8 "%~dp0team_web.py" --data-root "%TEAM_WEB_DATA%" start-detached
if errorlevel 1 exit /b 1
if /i not "%~2"=="--no-browser" start "" "http://127.0.0.1:8088/#/sessions"
exit /b 0
