@echo off
setlocal
chcp 65001 >nul
call "%~dp0tools\start_team_web.bat" "%~dp0var\team-web"
if errorlevel 1 pause
