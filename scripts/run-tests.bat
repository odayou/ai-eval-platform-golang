@echo off
REM AI Eval Platform — 测试运行脚本
REM 用法: scripts\run-tests.bat [package]
REM 示例: scripts\run-tests.bat ./internal/engine/
REM       scripts\run-tests.bat ./internal/api/

echo ═══════════════════════════════════════
echo  AI Eval Platform — Test Runner
echo ═══════════════════════════════════════

if "%1"=="" (
    echo 运行全部测试...
    go test -v -count=1 ./internal/... 2>&1
) else (
    echo 运行 %1 测试...
    go test -v -count=1 %1 2>&1
)

if %ERRORLEVEL% EQU 0 (
    echo ✅ 测试通过
) else (
    echo ❌ 测试失败
)
