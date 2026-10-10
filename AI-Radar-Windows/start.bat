@echo off
chcp 65001 >nul
setlocal

cd /d "%~dp0"

REM AI Radar Launcher
REM Start local AI news + leaderboard server

if exist "AI-Radar.exe" (
    echo Starting AI Radar...
    start "AI Radar" "AI-Radar.exe"
    exit /b 0
)

if exist "..\AI-Radar.exe" (
    echo Starting AI Radar from parent folder...
    start "AI Radar" "..\AI-Radar.exe"
    exit /b 0
)

echo Cannot find AI-Radar.exe
pause
