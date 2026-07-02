@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

:: AI Eval Platform — Windows 快速演示
:: 用法: scripts\demo.bat
:: 前置条件: 服务器已在 :8080 运行

set BASE=http://localhost:8080/api
set PASS=0
set FAIL=0

echo ══════════════════════════════════════
echo  AI Eval Platform — 功能演示
echo ══════════════════════════════════════
echo.

:: 1. 健康检查
echo 【1/8】基础设施
curl -s "%BASE%/health" | findstr "ok" >nul
if !errorlevel! equ 0 ( echo   ✅ 健康检查 & set /a PASS+=1 ) else ( echo   ❌ 健康检查 & set /a FAIL+=1 )

:: 2. 资源管理
echo 【2/8】资源管理
curl -s "%BASE%/agents" | findstr "CodeGen" >nul
if !errorlevel! equ 0 ( echo   ✅ Agents 列表 & set /a PASS+=1 ) else ( echo   ❌ Agents 列表 & set /a FAIL+=1 )

curl -s "%BASE%/models" | findstr "glm" >nul
if !errorlevel! equ 0 ( echo   ✅ 模型列表 & set /a PASS+=1 ) else ( echo   ❌ 模型列表 & set /a FAIL+=1 )

curl -s "%BASE%/scenes" | findstr "Agent" >nul
if !errorlevel! equ 0 ( echo   ✅ 场景列表 & set /a PASS+=1 ) else ( echo   ❌ 场景列表 & set /a FAIL+=1 )

:: 3. 题库
echo 【3/8】题库管理
curl -s "%BASE%/questions" | findstr "计数器" >nul
if !errorlevel! equ 0 ( echo   ✅ 题目列表 & set /a PASS+=1 ) else ( echo   ❌ 题目列表 & set /a FAIL+=1 )

:: 4. 创建评测任务
echo 【4/8】评测任务
curl -s -X POST "%BASE%/tasks" ^
  -H "Content-Type: application/json" ^
  -d "{\"name\":\"演示-代码评测\",\"agent_id\":\"a-codegen\",\"scene\":\"agent\",\"rule_set\":\"{\\\"rule_set_id\\\":\\\"rs-fullstack\\\"}\"}" ^
  | findstr "pending" >nul
if !errorlevel! equ 0 ( echo   ✅ 创建任务 & set /a PASS+=1 ) else ( echo   ❌ 创建任务 & set /a FAIL+=1 )

:: 5. 排行榜
echo 【5/8】排行榜
curl -s "%BASE%/stats/leaderboard" | findstr "entries" >nul
if !errorlevel! equ 0 ( echo   ✅ 排行榜 & set /a PASS+=1 ) else ( echo   ❌ 排行榜 & set /a FAIL+=1 )

:: 6. SDK
echo 【6/8】SDK
curl -s "%BASE%/sdk/docs" | findstr "sdk_version" >nul
if !errorlevel! equ 0 ( echo   ✅ SDK 文档 & set /a PASS+=1 ) else ( echo   ❌ SDK 文档 & set /a FAIL+=1 )

:: 7. 看板
echo 【7/8】数据看板
curl -s "%BASE%/stats/dashboard" | findstr "total_tasks" >nul
if !errorlevel! equ 0 ( echo   ✅ 看板统计 & set /a PASS+=1 ) else ( echo   ❌ 看板统计 & set /a FAIL+=1 )

:: 8. 高级功能
echo 【8/8】高级功能
curl -s "%BASE%/rules/graph" | findstr "nodes" >nul
if !errorlevel! equ 0 ( echo   ✅ 规则图谱 & set /a PASS+=1 ) else ( echo   ❌ 规则图谱 & set /a FAIL+=1 )

:: 结果
echo.
echo ══════════════════════════════════════
echo  演示结果: 通过=%PASS% 失败=%FAIL%
echo ══════════════════════════════════════
