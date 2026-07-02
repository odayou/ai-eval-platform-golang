# AGENTS.md — AI Eval Platform

> 本文件是 agent 的上下文入口（harness engineering 第1层）。参照 codex/AGENTS.md 风格：纯指令、无废话、每条可执行。
> 详见 docs/ARCHITECTURE.md 了解架构决策。

## 项目结构

- Go 后端（Gin）：`internal/` — api(路由) / engine(评测引擎) / store(SQLite) / proxy(协议代理) / ws(WebSocket) / scheduler(调度)
- Vue3 前端：`web/src/` — views(页面) / components(组件) / stores(pinia) / router
- 技术栈：Go 1.21+ / SQLite / Vue3 + Element Plus + Vite / WebSocket

## 关键命令

- 后端构建测试：`go build ./... && go test ./internal/...`
- 前端构建测试：`cd web && npm run build && npm test -- --run`
- Go 在 `C:\Program Files\Go\bin\go.exe`（不在 PATH，用全路径）
- npm 在 `C:\Program Files\nodejs`

## 编码约定（Go）

- 包注释用中文，说明文件职责和设计要点
- dataclass 用 frozen dataclass 风格（不可变 + tuple 字段）
- 错误处理：不中断评测链路，失败走 mock 回退（让 Demo 可演示）
- 模型职责分离：被测 Agent 用 `lookupModelEndpoint`（models 表）；平台引擎用 `LookupJudgeEndpoint`（judge_models 表），绝不混用
- 新增表用 `execAll("表名", []string{...})` + 增量迁移用 `migrateAddColumn`
- 权限门控：terminal/sandbox 执行前必须过 `permission_gate.go` 的 CheckCommand/CheckSandboxCode

## 编码约定（前端）

- 组件用 `<script setup lang="ts">`
- API 调用统一走 `utils/api.ts`
- 字典数据走 `dictStore.ts` 的 ensure* 方法（幂等缓存）
- 新增页面：路由加 `router/index.ts` + 导航加 `App.vue` navItems

## 禁止事项

- 不要把 api_key 明文输出到日志（用 MaskKeyForLog）
- 不要在 engine 里直接 import api 包（避免循环依赖）
- 不要新增第三方依赖除非必要（优先标准库）
- 不要破坏向后兼容：新列可空、新函数保留旧签名

## harness 六层能力（agent 运行时）

1. **AGENTS.md**（本文件）：项目约定入口
2. **Tools & MCP**：agent_loop.go 的 ReAct 循环 + builtin_tools.go（Bash/FileRead/FileWrite/WebSearch）+ mcp_client.go
3. **Skills**：skills_loader.go 按需加载知识包注入 system prompt
4. **Subagents**：subagent.go 上下文防火墙 + Task 工具
5. **Hooks**：hooks.go 生命周期脚本（PreToolUse/PostToolUse/Stop）
6. **Back-Pressure**：backpressure.go Stop 前自我验证（测试/lint）
