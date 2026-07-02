# AI Eval Platform — AI 能力全场景评测平台

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev)
[![Vue Version](https://img.shields.io/badge/Vue-3.5-4FC08D?logo=vue.js)](https://vuejs.org)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

> **一个测一切的 AI 评测引擎** — 被测对象任意、评测规则用户定义。

---

## 📋 目录

- [项目简介](#项目简介)
- [核心特性](#核心特性)
- [快速开始](#快速开始)
- [技术架构](#技术架构)
- [项目结构](#项目结构)
- [测试](#测试)
- [SDK 接入](#sdk-接入)
- [文档](#文档)

---

## 项目简介

AI Eval Platform 定位不是"又一个测某类 AI 的工具"，而是"一个测一切的评测引擎"。

被测对象不限于 Agent——大模型、AIGC 生成物、AI 工具、甚至实验报告都能测。  
平台本身不预设"测什么"，只提供 **喂题 → 执行 → 按规则打分 → 归因** 的通用引擎。

### 一句话定位

> "一个测一切的 AI 能力评测引擎——被测对象任意、评测规则用户定义。"

### 核心价值

- 🎯 **被测对象无差别接入** — API / 终端托管 / 源码 / 协议逆向，四种方式统一接入
- 📐 **评测规则用户定义** — 规则库独立维护，自定义新增、导入导出、多场景复用
- ⚡ **动静结合评测** — 多 Agent "圆桌合议"主观打分 + Docker 沙箱真实执行（编译/测试/压测）
- 🔄 **异常归因闭环** — Bad Case 自动回溯根因，反哺 Prompt 迭代

---

## 核心特性

### 评测引擎

| 特性 | 说明 |
|------|------|
| 🧠 **意图识别路由** | 单主 Agent 统管所有场景，自动判断评测类型 |
| 🏛️ **圆桌合议** | 4 领域专家并发评审 + 主导者加权汇总 |
| 🏗️ **沙箱编译** | Docker 容器池 / 进程级沙箱双轨，Go/Python/JS 编译+测试+Lint |
| 🛡️ **反泄漏扫描** | System Prompt 泄露 / 注入攻击 / 凭据泄露检测 |
| 🌪️ **混沌工程** | 弱网延迟 / 脏数据 / Jailbreak / 资源耗尽注入 |
| 🔌 **断路器** | Token/轮数/费用上限熔断，保护钱包 |
| 📊 **A/B 测试** | 克隆 Agent 换基座，MSI 敏感度 + ROI 性价比分析 |
| 🔎 **RAG 规则检索** | 向量化规则库，语义检索 top-K 规则 |
| 🤖 **AI 出题** | 多专家圆桌出题 + 动态扩题（弱点捕捉生成变体题） |
| 🔄 **异常归因** | 根因回溯 + System Prompt 自动闭环修正 |

### 管理能力

| 模块 | 说明 |
|------|------|
| 📦 **七库管理** | Agent / 大模型 / Tools / MCP Server / Skills / 模型配置 / Prompt 模板 |
| 📋 **评测任务** | 创建/调度/执行/报告全流程 |
| 📝 **题库管理** | 四种来源（手动/AI/基线/动态）+ 标签+难度+组卷 |
| ⚙️ **评测标准** | 规则集管理 + 导入导出 + 知识图谱可视化 |
| 📈 **数据看板** | 综合分趋势、帕累托散点、Bad Case 追踪 |
| 🕐 **定时任务** | Cron 表达式调度，自动回归测试 |
| 🏆 **排行榜** | 多 Agent/模型综合分排名，Top-5 雷达对比 |
| 🔄 **回归看板** | 版本演进性能 Diff，标杆回归漏洞 |
| 🔌 **协议代理** | HTTP/HTTPS MITM 代理，捕获闭源 Agent 流量 |
| 💻 **终端轨道** | 拉起本地 Agent 进程，交互式喂题评测 |

---

## 快速开始

### 前置要求

- Go 1.25+
- Node.js 20+
- SQLite（embedded via modernc.org/sqlite, 无需安装）

### 1. 启动后端

```bash
cd ai-eval-platform

# 编译后端
go build -o server.exe ./cmd/server/

# 运行(默认端口 8080)
./server.exe

# 或自定义配置
set PORT=9090 && ./server.exe
```

### 2. 启动前端

```bash
cd web

# 安装依赖
npm install

# 开发模式(热重载, 端口 5173)
npm run dev

# 生产构建
npm run build
```

### 3. 打开浏览器

访问 `http://localhost:5173`

### 4. 配置 API Key

在 **设置页** 填入至少一个 LLM 模型的真实 API Key（如 GLM-4-Flash / DeepSeek / GPT-4o），否则全系统运行在 mock 模式。

### 5. 创建首个评测任务

1. 进入 **评测任务** 页
2. 点击 **"+" 创建评测任务**
3. 选择 Agent（如 "CodeGen Agent"）、规则集（如 "全栈代码评测集"）
4. 提交，等待评测完成
5. 查看 **雷达图 + 圆桌合议 + 沙箱指标**

---

## 技术架构

```
┌─────────────────────────────────────────────────────────┐
│                    Vue 3 + Element Plus                  │
│   Tasks / Resources / Questions / Standards / Dashboard  │
└────────────────────────┬────────────────────────────────┘
                         │ HTTP REST + WebSocket
                         ▼
┌─────────────────────────────────────────────────────────┐
│  Gin API Layer (internal/api/)                          │
│   Router → CRUD Handlers → Middleware (CORS/WS)          │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  Scheduler (internal/scheduler/)                        │
│  Worker Pool → Priority Queue → Cron → Retry            │
└────────────────────────┬────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────┐
│  Engine Core (internal/engine/)                         │
│                                                         │
│  Run(taskID) →                                          │
│    1. Load Context (agent + rules + questions)          │
│    2. Intent Routing (router.go)                        │
│    3. Breaker Init (breaker.go)                         │
│    4. Per Question Loop:                                │
│       a. RAG Retrieve (rag.go)                          │
│       b. Invoke Agent (agentclient.go)                  │
│       c. Scoring: Roundtable / Single Judge             │
│       d. Sandbox Execute (sandbox.go)                   │
│       e. Anti-Leak Scan (antileak.go)                   │
│       f. Chaos Inject (chaos.go)                        │
│    5. Report Build (report.go)                          │
│    6. Async Attribution (attribution.go)                │
└────────────────────────┬────────────────────────────────┘
                         │
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│  SQLite      │ │  Docker      │ │  MITM Proxy  │
│  (store/)    │ │  Sandbox     │ │  (proxy/)    │
└──────────────┘ └──────────────┘ └──────────────┘
```

### 核心依赖

| 层 | 技术 | 用途 |
|----|------|------|
| 后端框架 | Gin v1.12 | HTTP 路由 + CORS + 中间件 |
| 数据库 | SQLite (modernc.org/sqlite) | 纯 Go 免 CGO, WAL 模式 |
| AI 调用 | OpenAI 兼容协议 | 支持 GLM/DeepSeek/GPT/Claude 等 |
| WebSocket | gorilla/websocket | 实时评测进度推送 |
| 沙箱 | Docker / 进程级 | 隔离编译执行被测代码 |
| 前端 | Vue 3 + Element Plus + ECharts | 响应式 SPA |
| 图表 | ECharts | 雷达图/折线图/散点图/力导向图 |
| 终端 | xterm.js | 虚拟化日志渲染 |
| Diff | Monaco Editor | 代码/Prompt 对比 |

---

## 项目结构

```
ai-eval-platform/
├── cmd/
│   └── server/main.go          # 应用入口
├── internal/
│   ├── api/                    # HTTP API 层
│   │   ├── router.go           # 路由定义(80+ 端点)
│   │   ├── task.go             # 评测任务 CRUD
│   │   ├── question.go         # 题库管理
│   │   ├── stats.go            # 看板统计 + 排行榜
│   │   ├── feedback.go         # 人工反馈 + RLHF
│   │   └── proxy_api.go        # MITM 代理管理
│   ├── engine/                 # 评测引擎(24 文件)
│   │   ├── engine.go           # 主 Runner
│   │   ├── agentclient.go      # Agent 调用客户端
│   │   ├── roundtable.go       # 圆桌合议
│   │   ├── sandbox.go          # 沙箱执行
│   │   ├── judge.go            # LLM-as-Judge
│   │   ├── breaker.go          # 断路器
│   │   ├── chaos.go            # 混沌工程
│   │   ├── rag.go              # RAG 规则检索
│   │   ├── clone.go            # 克隆 A/B 测试
│   │   └── ...                 # 更多引擎模块
│   ├── proxy/                  # MITM 代理
│   │   ├── proxy.go            # HTTP MITM
│   │   └── proxy_tls.go        # HTTPS MITM (+CA)
│   ├── scheduler/              # 任务调度
│   │   ├── scheduler.go        # Worker Pool
│   │   └── cron.go             # 定时任务
│   ├── sdk/                    # Agent 接入 SDK
│   │   ├── client.go           # Go SDK 客户端
│   │   ├── example/mock-agent/ # 示例 Agent
│   │   └── cmd/eval-cli/       # CLI 工具
│   ├── store/                  # 数据持久化
│   │   ├── sqlite.go           # 连接管理
│   │   ├── schema_core.go      # 七库表结构
│   │   ├── schema_eval.go      # 评测域表
│   │   ├── schema_agent.go     # Agent 运行时表
│   │   └── seed.go             # 种子数据
│   ├── ws/                     # WebSocket
│   │   └── hub.go              # 事件广播
│   └── config/                 # 配置
├── web/                        # Vue 前端
│   └── src/
│       ├── views/              # 11 个页面
│       ├── components/         # 子组件
│       ├── stores/             # Pinia 状态管理
│       ├── router/             # 路由
│       └── workers/            # Web Worker
├── docker/                     # Dockerfile
├── scripts/                    # 工具脚本
└── data/                       # SQLite 数据目录
```

---

## 测试

```bash
# 运行全部测试(66 个)
cd ai-eval-platform
go test -v -count=1 ./internal/...

# 运行指定包
go test -v -count=1 ./internal/engine/
go test -v -count=1 ./internal/api/
go test -v -count=1 ./internal/sdk/
go test -v -count=1 ./internal/proxy/
go test -v -count=1 ./internal/store/

# Windows 批处理
scripts\run-tests.bat
```

### 测试覆盖

| 包 | 测试数 | 覆盖内容 |
|----|--------|---------|
| `internal/engine` | 21 | 意图路由/Metric判官/Breaker/圆桌/报告/沙箱 |
| `internal/api` | 12 | CRUD 全链路/排行榜/SDK 文档/看板 |
| `internal/sdk` | 13 | 客户端/注册/提任务/EvalHandler/MCP |
| `internal/proxy` | 9 | 代理/捕获/回放/凭证提取/脱敏 |
| `internal/store` | 11 | 初始化/种子数据/并发读 |
| **合计** | **66** | |

---

## SDK 接入

第三方 Agent 可通过 SDK 快速接入平台：

### Go SDK

```go
import "ai-eval-platform/internal/sdk"

client := sdk.NewPlatformClient("http://localhost:8080/api", "")

// 1. 注册 Agent
agent, _ := client.RegisterAgent(sdk.AgentRegistration{
    Name:    "My Agent",
    Model:   "gpt-4o",
    Adapter: "api",
})

// 2. 提交评测
task, _ := client.SubmitTask(sdk.TaskSubmission{
    Name:    "首次评测",
    AgentID: agent.ID,
    Scene:   "agent",
})

// 3. 等待结果
result, _ := client.WaitForTask(task.ID)
fmt.Printf("综合分: %.1f\n", result.Overall)
```

### CLI 工具

```bash
eval-cli register --name "My Agent" --model glm-4
eval-cli submit --agent <id> --scene agent
eval-cli list agents
eval-cli list rulesets
```

### REST API

```bash
# 创建评测任务
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"name":"评测","agent_id":"<id>","scene":"agent"}'

# 查看结果
curl http://localhost:8080/api/tasks/<task-id>
```

详见 `internal/sdk/README.md`

---

## 文档

| 文档 | 说明 |
|------|------|
| `MRD-v2.docx` | 市场需求文档(完整产品定义) |
| `mrd/对比分析报告.md` | MRD vs 代码实现对比分析 |
| `mrd/TODO-修复清单.md` | 19 项缺口修复追踪 |
| `ROADMAP.md` | 工程落地路线图 |
| `internal/sdk/README.md` | SDK 接入指南 |
| `web/README.md` | 前端开发说明 |

---

## 许可证

MIT License

---

## 项目背景

产品架构高度借鉴 [claw-code](https://github.com/claw-code/claw-code) 与 opencode harness-engineering 理念。
