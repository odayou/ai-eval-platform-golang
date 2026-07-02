# 架构说明 — claw-code 借鉴关系校准与设计决策

> 文档目的：如实记录本项目对 [claw-code](https://github.com/claw-code/claw-code) 的借鉴关系，纠正 MRD §5.6 的简化叙事，避免后续维护者被误导。
>
> 最后更新：2026-06-22

---

## 一、重要澄清：claw-code 的真实形态

MRD §5.6 将 claw-code 描述为"Claude Code 的 Rust clean-room 重写（MIT 协议），是目前业内最成熟的生产级 AI Coding Agent 架构参照"。**这个描述是简化叙事，与 claw-code 的实际情况有偏差。**

通过通读 claw-code 源码与 `PHILOSOPHY.md`，真实情况是：

| MRD 描述 | 实际情况 |
|---|---|
| "Rust clean-room 重写" | `src/` 下是 **Python** 实现（不是 Rust）；存在 `rust/` 目录但也是产物 |
| "生产级 AI Coding Agent 架构" | claw-code 本身是 **"自治软件开发"的演示项目**，其代码是协调系统的产物，不是产品核心 |
| "经过真实流量场景打磨" | 它的核心价值不在单文件代码，而在协调方法论 |

### claw-code 的真正核心（PHILOSOPHY.md 原文）

claw-code 明确表示：**"If you only look at the generated files in this repository, you are looking at the wrong layer."**（只盯着生成文件看，你看错了层次。）

它真正展示的是 **三层协调系统**：

```
┌─────────────────────────────────────────────┐
│  OmX (oh-my-codex) — 工作流层               │
│  把一句话指令 → 结构化执行协议               │
│  （规划关键词 / 执行模式 / 验证循环）         │
└─────────────────────────────────────────────┘
┌─────────────────────────────────────────────┐
│  clawhip — 事件与通知路由层                  │
│  监控 git/tmux/issue/agent 生命周期，        │
│  把通知推出 Agent 上下文窗口                 │
└─────────────────────────────────────────────┘
┌─────────────────────────────────────────────┐
│  OmO (oh-my-openagent) — 多 Agent 协调层     │
│  规划 / 交接 / 分歧解决 / 验证循环           │
│  （Architect / Executor / Reviewer 协调）    │
└─────────────────────────────────────────────┘
```

**关键启示**：claw-code 的哲学是 **"人类给方向，claws 干活"** —— 瓶颈不再是打字速度，而是架构清晰度、任务分解、判断力。这与本评测平台"自动化喂题→执行→打分→归因"的思路是一致的。

---

## 二、MRD §5.6 六个借鉴点的真实落地情况

MRD 列举了六个"核心借鉴点"，下面逐一对照实际代码：

| claw-code 机制 | MRD 声称用途 | 实际借鉴落地 | 状态 |
|---|---|---|---|
| ConversationRuntime 主循环 | 评测主 Agent 的 agent loop 骨架 | `engine.go:Run` 实现了"读题→调Agent→打分→报告→归因"管线 | ✅ 已落地（自研实现，非直接移植） |
| 可注入 ToolExecutor trait | 不同场景挂不同执行策略 | `InvokeAgent`(agentclient.go) 按 adapter 分发 api/terminal/source | ✅ 已落地 |
| Context Compaction 上下文压缩 | 评测长会话不爆 token | `compaction.go` 多轮递进压缩 + 滑动窗口 + LLM 摘要 | ✅ 已落地 |
| 三级权限门控 (Permission-Gated) | 安全执行被测 Agent 代码 | ~~permissions 表建了但门控逻辑缺失~~ → **本次新增 `permission_gate.go` 落地** | ✅ 本次补齐 |
| JSONL 会话持久化 + 脱敏 | 评测记录可复现、不泄密 | `RedactText` 函数 + conversation 表写入前脱敏 | ✅ 已落地 |
| autoDream 后台异步归因 | Bad Case 根因回溯 + 反哺 | `attribution.go` + `autoFixPromptTemplate` 自动闭环 | ✅ 已落地 |

### 真正值得学且已移植的核心：权限门控

claw-code `src/permissions.py` 的 `ToolPermissionContext` 是本项目最值得借鉴的设计：

```python
# claw-code 的设计思想（已迁移到本项目的 Go 实现）
class ToolPermissionContext:
    deny_names: frozenset[str]        # 禁止的工具名
    deny_prefixes: tuple[str, ...]    # 禁止的工具名前缀
    workspace_scope: WorkspacePathScope  # 工作区路径范围
    cwd: Path                          # 当前工作目录

    def blocks(self, tool_name) -> bool          # 黑名单拦截
    def validate_payload_scope(tool, payload)    # 路径越界检测
```

本项目 `internal/engine/permission_gate.go` 将其迁移为 Go 的三级权限模型（read-only / workspace-write / full），详见该文件。

---

## 三、本项目的真实架构（非 claw-code 复刻）

本项目是 **面向字节抖音服务架构团队的 AI 评测引擎**，架构自研，claw-code 只是参考之一：

```
┌─────────────────────────────────────────────────────┐
│  Vue 3 + Element Plus + ECharts（前端 12 页面）       │
└────────────────────────┬────────────────────────────┘
                         │ HTTP REST + WebSocket
┌────────────────────────▼────────────────────────────┐
│  Gin API Layer（80+ 端点）                            │
└────────────────────────┬────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────┐
│  Scheduler（Worker Pool + 优先级队列 + Cron）         │
└────────────────────────┬────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────┐
│  Engine Core（评测引擎，24 模块）                     │
│                                                      │
│  Run(taskID) →                                       │
│    1. loadTaskContext（scene 联动规则集+题集）         │
│    2. Intent Routing（router.go）                     │
│    3. Breaker Init（breaker.go）                      │
│    4. Permission Gate（permission_gate.go）★本次新增  │
│    5. Per Question Loop:                             │
│       a. RAG Retrieve（rag.go，含关系边）             │
│       b. Invoke Agent（agentclient.go，双模型查找）   │
│       c. Scoring: Roundtable / Single Judge          │
│       d. Sandbox Execute（sandbox.go）                │
│       e. Anti-Leak Scan（antileak.go）                │
│       f. Chaos Inject（chaos.go）                     │
│    6. Report Build（report.go）                       │
│    7. Async Attribution（attribution.go）             │
└────────────────────────┬────────────────────────────┘
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
   ┌────────────┐ ┌────────────┐ ┌────────────┐
   │  SQLite    │ │  Docker    │ │  Redirector│
   │  (store/)  │ │  Sandbox   │ │  + MITM    │
   │            │ │            │ │  (proxy/)  │
   └────────────┘ └────────────┘ └────────────┘
```

---

## 四、协议逆向轨道的诚实说明（MRD §5.1 校准）

### MRD 原文的描述

MRD §5.1 和 §13 声称要"逸向解密客户端本地的配置文件、提取 Auth Token、伪造客户端的 Header（含特定 User-Agent、加密签名）、劫持本地 IPC 通信、伪造 TLS 指纹"，并称"参考 claude-code-router 已经用同样的 MITM 代理思路"。

### 实际情况校准

**对 claude-code-router 的误读**：实测 [claude-code-router](https://github.com/musistudio/claude-code-router) **根本不是 MITM**。它只是本地起一个 OpenAI/Anthropic 兼容 HTTP 服务，通过**环境变量重定向**（`ANTHROPIC_BASE_URL`）把 Claude Code 的请求拐到本地，凭证靠用户主动设 `ANTHROPIC_API_KEY`。**没有 TLS 指纹伪造、没有 IPC 劫持、没有加密签名、没有解密配置文件。** MRD 对参考项目的描述是误读。

### 本项目的落地决策

经过权衡，本项目协议逆向轨道采用 **对标 claude-code-router 的实用方向**，而非 MRD 原文的激进路线：

| 能力 | 是否实现 | 说明 |
|---|---|---|
| 本地协议适配服务 + 环境变量重定向 | ✅ | `internal/proxy/redirector.go`，自动注入 OPENAI_BASE_URL 等 |
| HTTP MITM 流量捕获 + 转评测任务 | ✅ | `proxy.go` 全套 |
| HTTPS MITM 解密（需装 CA） | ✅ | `proxy_tls.go` + `ca.go`，已修复 acceptLoop 调用 bug |
| 从请求头提取凭证（脱敏记录） | ✅ | `proxy.go:extractAuth` |
| TLS 指纹伪造（uTLS/JA3/JA4） | ❌ 预留接口 | 法律灰色 + 对抗闭源更新维护成本高，不实现 |
| 解密本地配置文件提取 Auth Token | ❌ 预留接口 | 同上 |
| 劫持本地 IPC 通信 | ❌ 预留接口 | 同上 |
| 伪造加密签名 | ❌ 预留接口 | 同上 |

**降级理由**：
1. **合规风险**：伪造 TLS 指纹/劫持 IPC/解密闭源客户端配置，在不同司法管辖区可能违反计算机滥用法规和 ToS。
2. **维护成本**：闭源客户端（zCode/Codex）的协议和指纹会随版本更新变化，逆向需要持续投入。
3. **实用性**：claude-code-router 的环境变量重定向方案已能覆盖绝大多数"把闭源客户端接入评测"的需求，且稳定可维护。
4. **预留接口**：相关函数签名在 `redirector.go` 中预留，未来确有需要时可填充具体实现。

---

## 五、模型管理的职责分离（本次重构）

MRD 原本只有一张 `models` 表，被测对象（考生）和平台评测引擎（考官）共用，存在自评自审风险。本次重构拆为两张表：

| 表 | 用途 | 谁读 |
|---|---|---|
| `models`（被测模型库） | 存放被测 Agent 使用的模型端点（考生） | `LookupExamineeEndpoint` → `ChatAgent` |
| `judge_models`（考官模型库） | 存放平台评测引擎各角色用的模型（考官） | `LookupJudgeEndpoint` → 圆桌/判官/出题/路由 |

`judge_models.role` 唯一，对应 `main`（主导者）/`expert_security`/`expert_perf`/`expert_arch`/`expert_product`/`question_gen`。圆桌各专家现在真正使用各自配置的模型，而非写死用 main。

---

## 六、总结

本项目是 **高度对齐 MRD 的工程化实践**，整体架构自研，claw-code 是参考之一而非蓝本。对于 MRD 中过度简化或与实际参考项目不符的描述（§5.1 协议逆向、§5.6 claw-code 借鉴），本文件给出了诚实的校准，并在代码层面做了务实落地。

**核心判断标准**：能让系统真正可用、可维护、可演示的，做；法律灰色、维护成本高、实用性低的，降级为预留接口 + 文档说明。
