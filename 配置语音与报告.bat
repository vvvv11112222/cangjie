@echo off
setlocal
chcp 65001 >nul
set "TEAM_MODEL_SETUP_NO_PAUSE=1"
if not exist "%~dp0tools\setup_models.bat" (
    echo Model setup script is missing. Please restore the complete project folder.
    set "MODEL_SETUP_EXIT=1"
    goto finish
)
call "%~dp0tools\setup_models.bat" "%~dp0var\team-web"
set "MODEL_SETUP_EXIT=%ERRORLEVEL%"
:finish
echo.
echo You can close this window after reading the setup result.
pause
exit /b %MODEL_SETUP_EXIT%
