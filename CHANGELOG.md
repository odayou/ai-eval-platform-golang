# Changelog

## v0.2.0 (2026-06-22)

### ✨ 新功能

- **排行榜** — 新 Leaderboard 页面 + API，支持 Top-5 雷达图对比、场景筛选
- **AI 圆桌出题** — 前端出题对话框(场景/规则集/题数配置→预览→确认入库)
- **Dashboard 增强** — 时间范围选择器、场景筛选器、帕累托散点图
- **SDK 接入** — Go SDK 客户端 (`internal/sdk/client.go`) + CLI 工具 (`eval-cli`) + MCP 协议支持
- **SDK 示例** — mock Agent 服务器，演示完整 SDK 接入流程
- **规则反哺闭环** — Standards 页新增"建议"Tab，支持一键确认落库
- **System Prompt 自动修正** — 归因引擎自动写回 `prompt_templates` 表
- **Docker 部署** — 多阶段构建 Dockerfile + docker-compose.yml 一键部署

### ✅ 修复

- AI 圆桌出题按钮解除 disabled（此前标注"待③引擎接入"）
- Dashboard 缺时间筛选、场景筛选、帕累托散点图
- Standards 页缺规则建议管理（确认→落库 UX）
- 归因引擎缺 System Prompt 闭环自动回写
- 缺排行榜功能（MRD 多次提及但代码不存在）
- 全部 19 项 MRD vs 代码缺口修复完成

### 🧪 测试

- **66 个单元/集成测试** 覆盖 engine/api/sdk/proxy/store/e2e
- engine: 意图路由/Metric判官/Breaker/圆桌加权/报告聚合
- api: CRUD 全链路/排行榜/SDK 文档/看板统计
- sdk: 客户端注册/提任务/EvalHandler/MCP Server
- proxy: HTTP MITM 代理/会话捕获/回放/凭证提取/脱敏
- store: 初始化/种子数据/WAL 模式/并发读
- e2e: 完整引擎管线/状态机/Bad Case/归因闭环/并发任务

### 📚 文档

- 项目根 README.md（完整中英文）
- web/README.md 重写
- CONTRIBUTING.md 贡献指南
- SDK 接入文档 (`internal/sdk/README.md`)
- .env.example 环境变量模板

### 🔧 DevOps

- GitHub Actions CI（Go 构建+测试+Vue 构建+golangci-lint+Docker）
- 多阶段 Dockerfile（最终镜像<20MB）
- docker-compose.yml 一键部署

---

## v0.1.0 (2026-06-21)

### 初始版本

- Go + Gin + SQLite 后端骨架
- Vue 3 + Element Plus 前端骨架
- 七库 CRUD（Agent/Model/Tools/MCP/Skills/模型配置/Prompt 模板）
- 评测引擎核心管线（engine.Run）
- 圆桌合议引擎（4 专家并发）
- 双轨沙箱（Docker + 进程级）
- 断路器钱包保护
- 混沌工程 4 种注入
- 克隆 A/B 测试 + MSI + ROI
- RAG 向量检索
- 反泄漏扫描
- HTTP/HTTPS MITM 代理
- 任务调度 + Cron
- 种子数据
