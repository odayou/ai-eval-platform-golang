# AI 能力全场景评测平台 — 工程落地 ROADMAP

> 基于 [MRD-v2](../mrd/AI能力全场景评测平台-MRD-v2.docx)
> 视角:**纯工程落地**(不绑面试时间表)。每一步标注**现状 / 依赖 / 复杂度 / 完成标志**。
> 现状图例: ✅ 已完成 · 🟡 部分 · ⬜ 未开始

---

## 步骤总览与依赖链

```
① 地基 ──────────────────────────→ ② 引擎核心 ──┐
(脚手架 + 七库数据层 + CRUD)        (调Agent→规则→报告)│
        │                                  │         │
        │                                  ▼         │
        └─────→ ③ 评测能力增强 ◀───────────┘         │
                 (沙箱 + 圆桌 + 题库)                 │
                          │                          │
                          ▼                          │
                 ④ 差异化壁垒                         │
        (克隆AB + 混沌 + 归因 + RAG + 图谱 + RLHF)   │
                          │                          │
                          ▼                          │
                 ⑤ 产品化                             │
        (调度 + 定时 + 重型前端 + 看板)              │
                          │                          │
                          ▼                          │
                 ⑥ 协议层(最难,最后) ◀──────────────┘
        (逆向代理 + 源码/终端轨道 + SDK)
```

**铁律:不能跳层。** 没有第②步的引擎,③④⑤全是空壳演示。

---

## ① 地基:脚手架 + 七库数据层
**现状:🟡 约 70%** · **依赖:无** · **复杂度:低**

> 把"被管理的对象"先建全。这是整个平台的 CRUD 地基。

### 1.1 后端骨架 ✅
- ✅ Go + Gin + modernc.org/sqlite(纯 Go,免 CGO)
- ✅ WAL 模式 + `SetMaxOpenConns(1)`
- ✅ `cmd/server` + `internal/{api,store}` 分层
- ⬜ `.gitignore`(`server.exe` 36MB、`data/eval.db*` 不能入库)
- ⬜ 配置加载(端口、DB 路径走 env / config.yaml)

### 1.2 七库数据表 ✅
| 库 | 表 | 状态 |
|---|---|---|
| Agent 库 | `agents` | ✅ |
| 大模型库 | `models` | ✅ |
| Tasks | `tasks` | ✅(但 status 永远停在 pending — 见 ②) |
| 规则库 | `rules` + `rule_sets` | ✅ |
| Tools 库 | `tools` | ✅ |
| MCP Server 库 | `mcp_servers` | ✅ |
| Skills 库 | `skills` | ✅ |
| 模型配置 | `model_config`(role→model 映射) | ✅ |
| Prompt 模板库 | `prompt_templates` | ✅ |
| 题库 | `questions` | ⬜ 缺表 |

### 1.3 七库 REST CRUD 🟡
- ✅ 11 类资源的增删查改 handler 全部接通
- ⬜ 缺 `questions` / `bad_cases` / `attribution`(后续步骤建表时补)
- ⬜ 无种子数据 — 首次打开全空(`seed.go` 待写)

### 1.4 前端骨架与七库 UI 🟡
- ✅ Vue3 + Vite + Element Plus + Pinia + Router
- ✅ 侧边栏 + 6 个路由页
- ✅ Resources 页 7 tab 全通(Agent/Model/Tools/MCP/Skills/模型配置/Prompt),含调试抽屉
- 🟡 Tasks 页场景 Tab 有,但列表项点击无反应、"+"未绑事件
- ⬜ Questions/Standards/Dashboard/Settings 仍是 `el-empty` 占位

### 1.5 通信层 🟡
- ✅ axios 封装 + Vite proxy
- ⬜ WebSocket(运行日志流、轨迹推送)— 引擎实时性需要,留到 ⑤

**完成标志:** 七库能完整 CRUD,前端打开就有(种子)数据,任何资源可建可删可查。

---

## ② 评测引擎核心(整个平台的灵魂)
**现状:⬜ 0%** · **依赖:①(数据层)** · **复杂度:中**

> ⚠️ **当前最大缺口。** 没有 engine,前面所有 CRUD 都是"资源管理后台",不是"评测平台"。
> 这是 MRD 第一章"一句话定位"能落地的最小闭环。

### 2.1 统一 Agent 调用客户端 ⬜
- `internal/engine/agentclient.go`
- OpenAI 兼容 `/chat/completions`(覆盖 GLM / DeepSeek / 大多数)
- 支持流式 SSE + 非流式两种
- 从 `models` 表读 baseURL + apiKey
- 超时、重试、错误归一化

### 2.2 题目输入 ⬜
- 建 `questions` 表(id, scene, prompt, difficulty, source, tags, expected)
- 至少硬编码 5~10 道种子题(代码生成、安全审查等)
- 引擎读题 → 喂给被测 Agent

### 2.3 规则打分执行器 ⬜
- `internal/engine/scorer.go`
- 三种打分方式(对应 `rules.scoring` 字段):
  - `metric` — 正则 / 关键词 / JSON 结构校验(硬指标,免费)
  - `llm` — LLM-as-Judge(把规则 content 当 rubric,调判官模型打分)
  - `manual` — 先留接口,人工评分后回填
- 单题得分 = 规则得分 × 权重

### 2.4 评测主流程(Runner)⬜
- `internal/engine/runner.go`
- 流程:`读task → 取agent+ruleset+questions → 逐题调agent → 逐规则打分 → 聚合 → 写回 result`
- `createTask` 后异步触发 `go runner.Run(id)`
- 状态机:`pending → running → done/failed`
- **完成标志:前端创建任务 → 几秒后 status 变 done → 右侧报告区显示分数**

### 2.5 报告聚合 ⬜
- 维度汇总(按 dimension 聚合分数,出雷达图数据)
- Bad Case 标记(得分低于阈值的题)
- 结构化 JSON 存 `tasks.result`

**完成标志(本步整体验收):**
> 在 UI 里:选 Agent → 选规则集 → 点"开始评测" → 看到逐题进度 → 出雷达图报告 + Bad Case 列表。
> **这是 MRD MVP(12.1)的全部内容,也是"它是不是个评测平台"的判定线。**

---

## ③ 评测能力增强
**现状:⬜** · **依赖:②(引擎跑通)** · **复杂度:中高**

> 从"能评测"升级到"评得准、评得深"。

### 3.1 动态沙箱压测层 ⬜
- Docker 容器池(创建/复用/销毁)
- 被测代码现场编译执行
- Test-Gen Agent:对 Diff 自动生成单测 + Mock 流量
- 硬指标采集:Pass@1、编译时延、Lint 错误数、编译成功率、Edit Distance
- **复杂度高** — 涉及容器编排,可先用进程级沙箱降级(MVP)

### 3.2 圆桌合议引擎 ⬜
- 主导者 Agent + 4 专家(安全/性能/架构/产品)+ 出题 + 视觉 + RAG
- `model_config` 表的 8 个 role 在这里全部启用
- 流程:独立阅卷 → 交叉比对 → 加权汇总
- 可扩展专家系统(用户自定义角色 + 规则集)
- **复杂度中** — 主要是多 Agent 并发调度 + prompt 编排

### 3.3 评测题库系统 ⬜
- Questions 页前端实现(筛选/列表/详情)
- 题库 CRUD API
- 四种来源:手动录入 / AI 圆桌生成 / 基线库抽样 / 动态扩题

### 3.4 出题子 Agent + 动态扩题 ⬜
- 多专家圆桌出题
- 弱点捕捉 → 当场生成 5 道同类变体加试

**完成标志:** 一次评测能跑出多维雷达图(不再是单一分数),沙箱里能真实编译运行被测代码。

---

## ④ 差异化壁垒(MRD 的"初杀器"集中区)
**现状:⬜** · **依赖:②③** · **复杂度:高**

> 这一堆才是 MRD 跟竞品拉开身位的东西。每块都可以独立做、独立讲故事。

### 4.1 多基座克隆 + 矩阵 A/B ⬜
- 双轨 API 注入:透传(Native)/ 平台覆盖(Override ENV)
- 一键克隆 + 换模型 → 同题并联对比 → 雷达图对比
- MSI(模型基座切换敏感度指数)
- ROI 性价比帕累托边界(性能 vs 成本散点)
- Prompt 智能回料平移适配器

### 4.2 混沌工程对抗模式 ⬜
- 弱网/延迟注入、脏数据注入、Jailbreak 攻击、资源耗尽
- Chaos Monkey 开关(任务高级选项)
- **复杂度中** — 在 agentclient 层加中间件拦截即可

### 4.3 异常归因 + AutoDream ⬜
- 后台异步归因引擎
- 根因回溯:指令遵循度下降 vs 上下文压缩信息缺失
- 反哺闭环:自动修正 System Prompt
- `attribution` 表 + 归因报告页

### 4.4 规则库 RAG 向量检索 ⬜
- 规则 embedding 生成
- SQLite + sqlite-vss(或外部向量库)存储
- 评测时语义检索 top-K 相关规则,动态组装规则集

### 4.5 知识图谱规则库 ⬜
- 规则间关系边(全屏 G6 可视化)
- 按 scene 聚类、按 dimension 着色
- 检索时顺边拉关联规则

### 4.6 人工协同 + 自进化闭环(RLHF 数据准备)⬜
- 踩/赞 + 批注界面
- Bad Case 负样本库沉淀(向量化入库)
- 规则反哺:批注触发规则更新建议 → 确认落库
- 四步闭环:人评 → Bad Case → 规则回填 → 模型微调数据积累

### 4.7 提示词反重定向 + 防白嫖扫描 ⬜
- 社工攻击诱导吐 System Prompt
- 踩额度攻击模拟
- 安全报告

### 4.8 钱包保护断路器 ⬜
- 单次评测 Token 上限、Tool Calling 轮数上限
- 超限熔断 + 费用预估
- **复杂度低** — 在 runner 里加计数器即可,建议早做

**完成标志:** 平台有至少 3 个"竞品做不到"的能力被真实跑通(推荐:克隆AB + 混沌 + 归因)。

---

## ⑤ 产品化
**现状:⬜** · **依赖:②③** · **复杂度:中**

> 让它能日常用、多人用、可持续监控。

### 5.1 任务调度与排队 ⬜
- Worker pool + 优先级队列
- 失败重试 + 超时熔断
- 沙箱资源池并发限制

### 5.2 定时任务(Cron)⬜
- Cron 表达式配置
- 定时回归测试看板
- 暂停/启用/删除

### 5.3 重型前端组件
- **Xterm.js 虚拟化终端日志矩阵**(WebSocket 二进制流,1万行/秒,CPU<5%)
- **Agent 思考轨迹拓扑图**(Vue Flow / G6,状态驱动 Diff 更新)
- **Monaco Diff Editor**(换脑前后代码/Prompt 对比)
- **Web Workers**(大矩阵数据解压,保 60 帧)
- **ECharts**(看板图表)

### 5.4 数据看板
- 指标卡片 + Pass@1 趋势 + 帕累托散点 + Bad Case 列表
- 时间范围 / 场景筛选

### 5.5 企业级基线测试集管理 ⬜
- GitLab / 内部代码库抽样 + 脱敏 + 标签 + 组卷

**完成标志:** 平台能挂着不重启,定时跑回归,看板实时反映评测趋势。

---

## ⑥ 协议层(最难,最后做)
**现状:⬜** · **依赖:①②** · **复杂度:极高**

> 实现 MRD 5.1 接口适配层的全部三条轨道。前两条(API / 沙箱)在 ②③ 已覆盖,这里是"评闭环源软件型 Agent"的硬骨头。

### 6.1 协议逆向代理轨道 ⬜
- MITM 代理 + 环境变量重定向(参考 claude-code-router)
- 劫持本地 IPC / TLS 指纹 / Header 伪造
- 评 zCode / Codex 类闭源 Agent
- **复杂度极高** — 需要逆向工程能力,参考 35k star 的 claude-code-router

### 6.2 本地源码 / 终端托管轨道 ⬜
- 指定项目路径 → 拉起进程 → 接管 stdio → 交互式喂题
- CLI Command Standardizer(启动命令 / 喂题模式配置)
- AI 自动解析 Agent 源码推断用法(Auto-Discovery)

### 6.3 上下文重构层(Context Compaction)⬜
- 自动索引 RPC 接口、网络拓扑、历史 Bug
- 滑动窗口 + 摘要递进 → 压缩到千字内

### 6.4 SDK + 标准接入
- MCP / CLI SDK,规范第三方 Agent 接入

**完成标志:** 平台能评一个完全闭源、只提供二进制的 Agent(比如直接评 zCode 本体)。

---

## 建议的执行顺序(纯工程视角,非面试倒计时)

```
现在 → ①收尾(种子数据 + .gitignore + Tasks页接通)      [1~2 天]
     → ②引擎核心(必做,平台的命)                       [3~5 天]
        ──── 这里之后就"真的是个评测平台"了 ────
     → ③评测增强(沙箱可降级用进程版)                    [1 周]
     → ④挑 2~3 个壁垒做(推荐:克隆AB + 混沌 + 断路器)   [2~3 周]
     → ⑤产品化(调度 + 看板 + 重型组件)                 [3~4 周]
     → ⑥协议层(有需要/有兴趣再攻)                      [长期]
```

**关键判断点:** ②完成的那一刻,这个项目从"管理后台"变成"评测平台"。在那之前一切 UI 都是为它铺路。

---

## 现状更新 (2026-06-22 最终状态)

经过 8 轮迭代后,项目实际状态已远超 ROADMAP 初始标注:

| 模块 | ROADMAP 初始 | 当前实际 | 说明 |
|---|---|---|---|
| 脚手架+分层 | ✅ | ✅ | 无变化 |
| 七库数据层 | ✅ | ✅ | 种子数据扩展至 17 题/13 规则/6 规则集 |
| 七库 CRUD API | ✅ | ✅ | 新增排行榜/SDK 文档/看板统计 |
| 七库前端 UI | 🟡 | ✅ | Standards/Questions/Dashboard 全部完成 |
| 种子数据 | ⬜ | ✅ | 覆盖全部 5 个场景 |
| **评测引擎** | **⬜** (0%) | **✅** | engine.Run 完整管线 + 24 个引擎模块 |
| 沙箱/圆桌/题库 | ⬜ | ✅ | 圆桌 4 专家 + 双轨沙箱 + 题库 CRUD |
| 克隆/混沌/归因/RAG | ⬜ | ✅ | 全部差异化壁垒已实现 |
| 调度/定时/看板 | ⬜ | ✅ | Worker pool + Cron + ECharts 看板 |
| 协议逆向/源码轨道 | ⬜ | ✅/⬜ | 源码轨道✅ / HTTP/HTTPS MITM✅ / 协议逆向需外部验证 |

### 超越 ROADMAP 的额外交付

- **SDK 接入**: Go SDK 客户端 + CLI 工具 + MCP 协议 + 示例 Agent
- **测试**: 69 个 Go 测试 + 11 个前端测试 + 8 个基准测试
- **工程化**: Makefile / Docker / Docker Compose / GitHub Actions CI
- **文档**: README / CONTRIBUTING / CHANGELOG / SECURITY / OpenAPI / SDK 文档
- **部署**: 单二进制发布(内嵌前端) + Nginx 配置 + 一键启动脚本

---

## 现状速查(截至本 ROADMAP 建立时)

| 模块 | 现状 | 备注 |
|---|---|---|
| 脚手架 + 分层 | ✅ | Go+Gin / Vue3+Vite |
| 七库数据层 | ✅ | 11 张表 |
| 七库 CRUD API | ✅ | questions 等后续表待补 |
| 七库前端 UI | 🟡 | Resources 完整,其余 4 页占位 |
| 种子数据 | ⬜ | 首屏全空 |
| **评测引擎** | **⬜** | **核心缺口,0%** |
| 沙箱 / 圆桌 / 题库 | ⬜ | 依赖引擎 |
| 克隆 / 混沌 / 归因 / RAG | ⬜ | 差异化壁垒 |
| 调度 / 定时 / 看板 | ⬜ | 产品化 |
| 协议逆向 / 源码轨道 | ⬜ | 最难,最后 |
