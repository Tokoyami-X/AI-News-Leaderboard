@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo AI RADAR - AI 最新新闻聚合
where go >nul 2>nul
if errorlevel 1 (echo [错误] 未找到 Go，请安装 Go 1.23 或更高版本并加入 PATH。 & pause & exit /b 1)
echo 浏览器访问: http://localhost:8080
echo 按 Ctrl+C 停止服务，请保持此窗口打开。
go run .
if errorlevel 1 pause
