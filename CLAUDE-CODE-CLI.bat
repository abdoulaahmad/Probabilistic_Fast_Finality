@echo off
chcp 65001 >nul
title Claude Code CLI - Nghimmo
color 0B

cls
echo.
echo ============================================================
echo          CLAUDE CODE CLI - POWERED BY NGHIMMO
echo ============================================================
echo.
echo  Server : https://api.xpiki.com/v1
echo  Check  : https://api.nghimmo.com/check
echo.
echo ============================================================
echo.

:: Enter the customer's API key
set "APIKEY="
set /p "APIKEY=Enter your API Key (sk-...): "

if "%APIKEY%"=="" (
    echo.
    echo [ERROR] You did not enter an API Key. Close the window and reopen.
    echo.
    pause
    exit /b
)

:: Point Claude Code to the Nghimmo server (session-only; closing will clear it)
set "ANTHROPIC_BASE_URL=https://api.xpiki.com/v1"
set "ANTHROPIC_AUTH_TOKEN=%APIKEY%"
set "ANTHROPIC_API_KEY=%APIKEY%"
set "ANTHROPIC_MODEL=kr/claude-sonnet-4.6"
set "ANTHROPIC_SMALL_FAST_MODEL=kr/claude-sonnet-4.6"

echo.
echo [OK] Configuration complete. Opening Claude Code in this folder...
echo      (Folder: %CD%)
echo.

:: Open Claude Code in the folder where this batch file is located
claude

echo.
echo ============================================================
echo  Claude Code has closed. Press any key to exit.
echo ============================================================
pause >nul
