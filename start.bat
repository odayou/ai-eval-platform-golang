@echo off
chcp 65001 >nul
title AI Eval Platform

echo ═══════════════════════════════════════
echo  AI Eval Platform — 一键启动
echo ═══════════════════════════════════════
echo.

:: 检查 Go
go version >nul 2>&1
if %errorlevel% neq 0 (
    echo [错误] 未检测到 Go,请先安装 Go 1.25+
    pause
    exit /b 1
)

:: 构建前端
echo [1/4] 构建前端...
cd web
call npm install >nul 2>&1
call npm run build >nul 2>&1
if %errorlevel% neq 0 (
    echo [警告] 前端构建异常,将使用开发模式
)
cd ..

:: 构建后端
echo [2/4] 编译后端...
go build -ldflags="-s -w" -o server.exe ./cmd/server/
if %errorlevel% neq 0 (
    echo [错误] 后端编译失败
    pause
    exit /b 1
)

:: 启动后端
echo [3/4] 启动服务...
start "AI Eval Backend" /B server.exe > server.log 2>&1
echo   后端口: 8080
echo   日志: server.log

:: 可选: 启动前端开发模式
echo [4/4] 环境就绪
echo.
echo ═══════════════════════════════════════
echo  后端: http://localhost:8080
echo  前端: http://localhost:5173 (开发模式)
echo  健康: http://localhost:8080/api/health
echo ═══════════════════════════════════════
echo.
echo 按任意键停止服务...
pause >nul

:: 停止服务
echo 正在停止...
taskkill /f /im server.exe >nul 2>&1
echo 服务已停止
