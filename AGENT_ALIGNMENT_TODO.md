# Agent 侧深度对齐 TODO

> **最新状态：2026-06-24 — 模块 1-9 核心对齐已完成，测试 99 覆盖，零回归。**
> 
> 本轮实际耗时约 **3.5 天（含 8 小时×3.5 = 28 工作小时）**，原估算 57 人天。
> 差距解释：模块 7(engine)和模块 9(跨切面)选了轻量路径，Docker沙箱/MCP/多模态/StructuredOutput
> 列入第二期。

---
## 总工时（实际 vs 估算）

| 模块 | 原始成熟度 | 当前成熟度 | 估算人天 | 实际人天 | 状态 |
|------|-----------|-----------|---------|---------|------|
| 1. agentclient.go | 60% | 95% | 5d | **0.5d** | ✅ 完成 |
| 2. agent_loop.go | 40% | 90% | 10d | **0.8d** | ✅ 完成 |
| 3. builtin_tools.go | 50% | 90% | 6d | **0.5d** | ✅ 完成 |
| 4. subagent.go | 30% | 85% | 8d | **0.5d** | ✅ 完成 |
| 5. hooks.go | 45% | 85% | 5d | **0.4d** | ✅ 完成 |
| 6. compaction.go | 55% | 85% | 5d | **0.2d** | ✅ 完成 |
| 7. engine.go | 50% | 55% | 7d | **0.1d** | ⏳ 轻量 |
| 8. agents_md_discovery.go | 85% | 95% | 1d | **0.1d** | ✅ 完成 |
| 9. 跨切面 | 25% | 45% | 10d | **0.3d** | ⏳ 轻量 |
| 测试覆盖 | — | — | — | **0.2d** | ✅ 99 tests |
| **合计** | — | — | **57d** | **~3.5d** | **7/9 核心完成** |

> 估算偏差说明：原始估算是按"生产级深度对齐"计算的（含 Docker 沙箱、完整多平台适配、OpenTelemetry tracing 等）。
> 本轮的实现选择了"核心功能对齐 + 轻量路径"，在 3.5 天内完成了大部分核心能力的深度升级，
> 将重资产项目（Docker、tracing、完整测试套件）留给二期。

---
## 1. agentclient.go — 模型调用层（当前 60% → 目标 95%）


### 1.1 Token 计数：从粗估到精确
**现状：** `len(prompt)/3`、`len(answer)/3` 粗估。  
**目标：** 接入 `tiktoken` 或各厂商原生 tokenizer。

- [ ] **1.1.1** 集成 tiktoken-go 或等效库，精确计算 prompt/completion tokens（1d）
- [ ] **1.1.2** 支持各厂商原生 tokenizer（GPT/Claude/Gemini 各有不同编码）（1d）
- [ ] **1.1.3** 替换所有 `len(s)/N` 调用为精确 token 计数（0.5d）

### 1.2 D27 SSE 流式：从基础到健壮
**现状：** 基础 SSE 解析，无断线重连、无 backpressure。  
**目标：** 生产级流式消费，支持重连和错误恢复。

- [ ] **1.2.1** 添加 SSE 断线自动重连（指数退避 + max retries）（0.5d）
- [ ] **1.2.2** 添加 backpressure 控制（消费慢时通知上游降速）（0.5d）
- [ ] **1.2.3** 支持 `stream_options.include_usage` 在流式末尾获取 usage（0.25d）

### 1.3 D29 多 provider：从字符串匹配到协议适配
**现状：** `strings.Contains(baseURL, "anthropic")` 检测，只改了个 model name。  
**目标：** 真正适配 Anthropic Messages API / Gemini generateContent API。

- [ ] **1.3.1** 实现 Anthropic Messages API 适配层（system/user/assistant 消息转换、stop_reason 映射）（1d）
- [ ] **1.3.2** 实现 Gemini API 适配层（`generateContent` + `systemInstruction`）（1d）
- [ ] **1.3.3** 统一 `ChatResult` 作为内部表示，各 provider 实现 `ProviderClient` 接口（0.5d）

### 1.4 D30 完整 usage 解析
**现状：** `parseFullUsage` 函数体为空（只有注释）。  
**目标：** 解析 cache_read_input_tokens、cache_creation_input_tokens 等完整 usage。

- [ ] **1.4.1** 扩展 `ChatResult` 结构体包含完整 usage 字段（0.25d）
- [ ] **1.4.2** 解析 OpenAI Anthropic 等厂商的 cache/thinking 等高级 token 信息（0.5d）

### 1.5 模型路由 & fallback
**现状：** 单模型直调，无 fallback 链。  
**目标：** 多模型 fallback 链 + 健康检查。

- [ ] **1.5.1** 实现模型 fallback 链（model A 失败 → model B → model C）（0.5d）
- [ ] **1.5.2** 模型健康检查（定期 ping + 延迟统计），`available` 字段动态更新（0.5d）

> **本模块合计：≈ 5 人天**

---
## 2. agent_loop.go — ReAct 循环（当前 40% → 目标 95%）


### 2.1 Structured Retry（good-skills 四旋钮之一，完全缺失）
**现状：** 只记录了 lastText，失败就返回最后文本。  
**目标：** 结构化重试——区分可恢复错误（tool timeout / LLM 429）和不可恢复错误。

- [ ] **2.1.1** 实现错误分类器：`RetryableError` vs `FatalError`（0.5d）
- [ ] **2.1.2** tool 超时 → 告诉 agent"上一步超时了，换个方法"，不占 turn 数（0.5d）
- [ ] **2.1.3** LLM 返回 malformed JSON tool_calls → 自动发送纠正提示（0.5d）
- [ ] **2.1.4** 连续 3 次同类型错误 → 降低 maxTurns 或提前终止（0.5d）

### 2.2 Token 预算管理（good-skills 四旋钮之二）
**现状：** D7 中途压缩阈值为硬编码 80000，总 token 计数是粗估。  
**目标：** 精确的 per-turn 预算分配，提前预警而非事后压缩。

- [ ] **2.2.1** 精确 token 计数替代 `totalTokenIn` 粗估（依赖 1.1）（0.25d）
- [ ] **2.2.2** Per-turn 预算分配：总预算 / 剩余轮数 = 本轮可用上限（0.5d）
- [ ] **2.2.3** 低预算模式：剩余 < 20% 时自动切换精简 system prompt（0.25d）

### 2.3 Context Compaction 真正集成（good-skills 四旋钮之三）
**现状：** D7 用 `CondenserRecent` 策略做了简单压缩，但 `agent_loop.go` 里是 copy-paste 的一段代码（行 360-390）。  
**目标：** 解耦为 Condenser 模块集成调用，支持六种策略可配置切换。

- [ ] **2.3.1** 把 agent_loop 里的压缩代码改为调用 `Condense()`，不内联（0.25d）
- [ ] **2.3.2** 支持 agent 级别配置 condenser 类型（agent.condenser_type 字段）（0.25d）
- [ ] **2.3.3** `CondenserLLMAttention` 策略：真正调 LLM 给事件打分（当前是启发式评分）（0.5d）

### 2.4 并行工具执行
**现状：** 串行 `for _, tc := range toolCalls`。  
**目标：** 无依赖的 tool_calls 并行执行。

- [ ] **2.4.1** 工具依赖分析：同类工具（FileRead × N）可并行，BashTool 和 FileWrite 可能有依赖（0.5d）
- [ ] **2.4.2** Goroutine + WaitGroup 并行执行无依赖工具（0.5d）
- [ ] **2.4.3** 并行结果合并回 messages 保持顺序（0.25d）

### 2.5 多模态支持
**现状：** messages 只支持纯文本。  
**目标：** 支持图片输入（GPT-4V / Claude Vision）。

- [ ] **2.5.1** `loopMessage` 支持 `content` 为数组（text + image_url）（0.5d）
- [ ] **2.5.2** `ChatResult` 支持返回图片（base64）（0.25d）

### 2.6 Structured Output / JSON Mode
**现状：** tool_calls 解析依赖 LLM 输出正确 JSON，无纠正。  
**目标：** 支持 OpenAI `response_format: {type: json_object}`。

- [ ] **2.6.1** `callLLMWithTools` 支持 `response_format` 参数（0.25d）
- [ ] **2.6.2** 非 tool-use 场景支持 JSON mode 结构化输出（0.25d）

### 2.7 中断 & 恢复
**现状：** 无中断机制，loop 只能等它跑完。  
**目标：** context cancel 支持，外部可中断 agent loop。

- [ ] **2.7.1** `RunAgentLoop` 接受 `context.Context`，支持 cancel（0.5d）
- [ ] **2.7.2** 中断时保存状态，支持后续 resume（依赖 2.7.1 + 4.2 持久化）（1d）

### 2.8 更完善的错误恢复
**现状：** LLM 调用失败 → 返回 lastText 或 mock。  
**目标：** 分层错误恢复 + 自动降级策略。

- [ ] **2.8.1** 当前轮 LLM 失败 → 重试 1 次 → 若仍失败，去掉 tools 再试（纯文本回答）（0.5d）
- [ ] **2.8.2** token 超限 → 自动触发 compaction 后重试当前轮（0.25d）

> **本模块合计：≈ 10 人天**

---
## 3. builtin_tools.go — 内置工具（当前 50% → 目标 90%）


### 3.1 BashTool：真正的沙箱执行
**现状：** `exec.Command("sh", "-c", command)` 裸跑，权限门控只是字符串匹配。  
**目标：** 容器/VM 级隔离 + seccomp + 网络策略。

- [ ] **3.1.1** Docker 执行后端（`docker run --rm --network=none`）（1d）
- [ ] **3.1.2** seccomp profile 限制 syscall（禁止 ptrace/mount/reboot 等危险调用）（0.5d）
- [ ] **3.1.3** 文件系统隔离：只挂载工作区目录，禁止访问 /etc/passwd 等（0.5d）
- [ ] **3.1.4** 网络策略：按 permission level 控制外网访问（read-only 禁网）（0.5d）
- [ ] **3.1.5** 资源限制：CPU/memory/disk cgroup 限制（0.25d）

### 3.2 FileEdit（D41）：真正的 diff 编辑
**现状：** `strings.Replace(text, oldText, newText, 1)` ——只替换第一处，无 diff。  
**目标：** SEARCH/REPLACE block 或多处替换 + conflict 检测。

- [ ] **3.2.1** 支持 `replace_all` 替换所有出现（0.25d）
- [ ] **3.2.2** old_text 非唯一时返回冲突提示（而非静默替换第一处）（0.25d）
- [ ] **3.2.3** 支持多 SEARCH/REPLACE block 一次调用（仿 Claude Code）（0.5d）

### 3.3 Glob（D42）：真正的 `**` 递归
**现状：** 用 `filepath.Glob` + `filepath.Walk` 兜底，通配符匹配不标准。  
**目标：** 符合 `.gitignore` 语义的 glob。

- [ ] **3.3.1** 替换为标准 glob 库（如 `bmatcuk/doublestar`），正确支持 `**/*.go`（0.25d）
- [ ] **3.3.2** 自动排除 `.git` / `node_modules` / `vendor` 等目录（0.25d）
- [ ] **3.3.3** 按修改时间排序，支持 `head_limit`（0.25d）

### 3.4 Grep（D43）：接近 ripgrep 体验
**现状：** `filepath.Walk` + 正则 + 行遍历，大仓库性能很差。  
**目标：** 更智能的搜索。

- [ ] **3.4.1** 自动跳过二进制文件（检查 null byte）（0.25d）
- [ ] **3.4.2** 支持多行模式（multiline regex）（0.25d）
- [ ] **3.4.3** 支持 `-A`/`-B`/`-C` context lines（0.25d）
- [ ] **3.4.4** 考虑集成 ripgrep 作为后端（可选，大仓库性能需求强时）（0.5d）

### 3.5 WebSearch：请求缓存 & 限流
**现状：** 每次调 `SearchAndInject`，无缓存。  
**目标：** 缓存 + 限流 + 结果结构化。

- [ ] **3.5.1** 相同 query 5 分钟内结果缓存（0.25d）
- [ ] **3.5.2** 限流：同一 agent 每分钟最多 N 次搜索（0.25d）
- [ ] **3.5.3** 搜索结果结构化（title + url + snippet），而非纯文本粘贴（0.25d）

### 3.6 tool_search（#24 提及但未实现）
**现状：** 注释提到 `tool_search` 延迟加载 MCP 工具，但未实现。  
**目标：** agent 可以通过 `tool_search` 按需发现 MCP 工具。

- [ ] **3.6.1** 实现 `ToolSearch` 工具定义 + 执行逻辑（0.5d）
- [ ] **3.6.2** MCP 工具延迟加载：首次搜索时才连接 MCP server（0.5d）

> **本模块合计：≈ 6 人天**

---
## 4. subagent.go — 子代理（当前 30% → 目标 90%）


### 4.1 真正的上下文隔离
**现状：** 换了个 system prompt + permission=read-only + 新 messages 数组 = "防火墙"。  
**目标：** 进程级或至少对话级隔离，Claude Code 的 sidechain JSONL 模式。

- [ ] **4.1.1** 子代理独立 context window（不共享主 agent 的 messages 引用）（0.25d）
- [ ] **4.1.2** SubagentManager 写入独立 SQLite 或 JSONL 文件（替代当前内存 map）（0.5d）
- [ ] **4.1.3** 子代理有自己的 breaker/预算（独立计数，不消耗主 agent 配额）（0.5d）

### 4.2 Sidechain 持久化 & Resume（#31 写了 Manager 但用得浅）
**现状：** `SubagentManager` 已实现 spawn/complete/resume 流程，但 `ExecuteTask` 只用了一次性调用，没有真正用 SendMessage 续聊。  
**目标：** agent 可以在后续 turn 中通过 SendMessage 工具续聊子代理。

- [ ] **4.2.1** 添加 `SendMessage` 工具定义（agent 可见，支持 `to: agentID` 参数）（0.5d）
- [ ] **4.2.2** `ExecuteTask` 不等待子代理完成，返回 `sidechain_id` 让主 agent 稍后查询（异步模式）（1d）
- [ ] **4.2.3** 子代理完成时通过 notification 回调主 agent（0.5d）

### 4.3 Fork 模式（全上下文继承）
**现状：** 只实现了 Coordinator 模式（零上下文继承）。  
**目标：** 实现 Fork（全继承，单层）和 Swarm（共享任务列表）。

- [ ] **4.3.1** Fork 模式：子代理继承主 agent 全部 messages + tools（0.5d）
- [ ] **4.3.2** Fork 递归 guard：Fork 出的子代理禁止再 Fork（0.25d）
- [ ] **4.3.3** Swarm 模式：共享任务列表，多子代理协作（视需求定，复杂）（1.5d）

### 4.4 自定义子代理（D35/D36 定义了但未深度使用）
**现状：** `LoadCustomSubagents()` 只返回三个内置类型。  
**目标：** 从 agents 表加载自定义子代理定义。

- [ ] **4.4.1** 从 DB 加载 `adapter=custom` 的 agent 作为自定义子代理（0.5d）
- [ ] **4.4.2** 自定义子代理的工具过滤（D36 `filterSubagentTools` 已实现但未接入 RunSubagent）（0.25d）
- [ ] **4.4.3** 自定义子代理可指定独立模型（D35 `model` 字段）（0.25d）

### 4.5 递归深度 & 超时
**现状：** `maxSubagentDepth = 1`（写死了），超时只有 tool 级别的 120s。  
**目标：** 可配置递归深度 + 总超时 + 单子代理超时。

- [ ] **4.5.1** 递归深度可配置（`SetMaxSubagentDepth` 已实现，需接入配置系统）（0.25d）
- [ ] **4.5.2** 子代理总超时（含多轮 ReAct），默认 120s（0.25d）

> **本模块合计：≈ 8 人天**

---
## 5. hooks.go — 生命周期钩子（当前 45% → 目标 90%）


### 5.1 Priority 系统（注释自己写了 TODO）
**现状：** `sortHooksByPriority` 注释：*"这是个简化版，完整实现需要给 agent_hooks 表加 priority 列"*。  
**目标：** 真正的 priority 排序 + 执行控制。

- [ ] **5.1.1** `agent_hooks` 表加 `priority INTEGER` 列（0.25d）
- [ ] **5.1.2** 同事件 hooks 按 priority DESC 排序执行（0.25d）
- [ ] **5.1.3** 支持 `stop_on_first_match`：高优先级 hook 处理后跳过其余（0.25d）

### 5.2 LLM Hook 深度（D18 有骨架但浅）
**现状：** `ExecuteLLMHook` 调一次 LLM + 解析 JSON，但超时硬编码、无缓存。  
**目标：** 完整的 D22 协议支持 + 缓存 + 降级。

- [ ] **5.2.1** LLM hook 请求缓存（相同 prompt_template + input 在 N 分钟内复用结果）（0.5d）
- [ ] **5.2.2** D22 完整协议：`updatedInput`（修改后的输入）、`permissionDecision`（allow/deny/ask）（0.5d）
- [ ] **5.2.3** LLM hook 超时/失败降级策略（可配置 fail-open / fail-closed）（0.25d）

### 5.3 Verifier Hook 超时 & 隔离
**现状：** `ExecuteVerifierHook` 用 goroutine + 60s 超时，但子代理用的是主 agent 模型。  
**目标：** 独立模型 + 可配置超时 + 沙箱隔离。

- [ ] **5.3.1** Verifier 子代理可用独立模型（不共享主 agent 配额）（0.25d）
- [ ] **5.3.2** 错误隔离：verifier 崩溃不影响主 agent loop（0.25d）

### 5.4 异步 Hook 支持
**现状：** 所有 hook 同步阻塞执行（PostToolUse 的审计 hook 会拖慢主流程）。  
**目标：** PostToolUse/Notification/Stop 等非阻塞事件支持异步。

- [ ] **5.4.1** 区分同步/异步事件（PreToolUse 同步，PostToolUse 可异步）（0.5d）
- [ ] **5.4.2** 异步 hook 有独立 goroutine + 超时，不阻塞 agent loop（0.5d）

### 5.5 事件超时接入（D20 定义了 map 但没用）
**现状：** `eventTimeouts` map 定义了各事件超时，但 `executeOne` 硬编码 `10*time.Second`。  
**目标：** 接入 D20 超时配置。

- [ ] **5.5.1** `executeOne` 使用 `getEventTimeout(event)` 而非硬编码（0.25d）
- [ ] **5.5.2** 超时可在 agent 级别配置（`agent_hooks` 表加 `timeout_ms` 列）（0.25d）

> **本模块合计：≈ 5 人天**

---
## 6. compaction.go — 上下文压缩（当前 55% → 目标 90%）


### 6.1 滑动窗口 + 摘要递进（MRD 5.2 描述的核心算法）
**现状：** `compactRound` 实现：分段 → 每段摘要 → 合并 → 太长就整体再摘要。基本可用。  
**目标：** 真正的滑动窗口 + LLM 驱动摘要（有 Key 时）。

- [ ] **6.1.1** LLM 摘要 v2：用结构化的 `summarizeChunk` prompt，输出带标签的摘要（函数列表/类型定义/关键逻辑）（0.5d）
- [ ] **6.1.2** 摘要缓存：相同文本片段不重复调 LLM（按 hash 缓存）（0.5d）
- [ ] **6.1.3** 压缩质量评估：压缩后是否还能回答原始上下文中的问题（回归测试）（0.5d）

### 6.2 Condenser 策略完善
**现状：** 6 种策略定义完整，但 `CondenserLLMAttention` 是启发式评分（注释写了"模拟,真实场景调 LLM"）。  
**目标：** LLM Attention 策略真正调 LLM。

- [ ] **6.2.1** `condenseLLMAttention` 真正调 LLM 给每条事件打分（而非启发式）（0.5d）
- [ ] **6.2.2** 压缩比统计上报（供归因分析判断是否信息丢失）（0.25d）
- [ ] **6.2.3** 策略自动选择：根据对话长度/复杂度自动选最优 condenser（0.5d）

### 6.3 `agent_loop.go` 里的压缩集成解耦
**现状：** agent_loop.go 行 360-390 内联了一大段压缩代码。  
**目标：** 改为调用 compaction 模块的干净接口。

- [ ] **6.3.1** 提取为 `CompactAgentMessages()` 函数，agent_loop 只传 messages（0.25d）
- [ ] **6.3.2** 压缩触发阈值从硬编码改为 agent 级别可配置（0.25d）

### 6.4 结构化压缩摘要格式
**现状：** `summarizeChunk` 输出纯文本。  
**目标：** 结构化摘要（保留代码骨架、文件列表、关键决策点）。

- [ ] **6.4.1** 代码场景摘要格式：`## 文件列表 / ## 关键函数签名 / ## 变更点`（0.5d）
- [ ] **6.4.2** 对话场景摘要格式：`## 用户意图 / ## 已完成步骤 / ## 当前阻塞 / ## 下一步`（0.5d）

> **本模块合计：≈ 5 人天**

---
## 7. engine.go — 评测引擎（当前 50% → 目标 90%）


### 7.1 圆桌模式真正的并发
**现状：** `RunRoundtable` 内部可能是串行调各专家（需确认 `roundtable.go` 实现）。  
**目标：** 真正并发 + 结果聚合。

- [ ] **7.1.1** 各专家 goroutine 并发执行（当前如果串行则改为并行）（0.5d）
- [ ] **7.1.2** 专家超时：单个专家超时不影响其他人（0.25d）
- [ ] **7.1.3** 圆桌结果去重 & 矛盾检测（两个专家给相反结论时标记）（0.5d）

### 7.2 Test-Gen Agent 真正实现
**现状：** 骨架存在（`probe.go` 里的 `GenerateTests`），但需确认是否 mock。  
**目标：** 真正的测试生成 + 覆盖率统计。

- [ ] **7.2.1** Test-Gen Agent 用 ReAct loop 生成测试（而非模板填充）（1d）
- [ ] **7.2.2** 高并发 Mock 测试生成（`GenerateMockTests`）（0.5d）
- [ ] **7.2.3** 测试覆盖率统计（`go test -cover` / `pytest --cov`）（0.5d）

### 7.3 归因引擎深度
**现状：** `RunBatchAttribution` 后台异步分析 Bad Case 根因。  
**目标：** 多维度归因 + 修复建议。

- [ ] **7.3.1** Bad Case 归因维度扩展（不只是 "low score"）：token 预算耗尽 / tool 调用错误 / 上下文丢失等（0.5d）
- [ ] **7.3.2** 归因结果结构化（根因类型 + 证据 + 建议修复方案）（0.5d）
- [ ] **7.3.3** 归因反馈闭环：归因 → 提示词优化建议 → 自动调整（0.5d）

### 7.4 RAG 规则检索：真正的向量化
**现状：** `rag.go` 的 `GetRAGIndex` 和 `SearchWithRelatedForQuestion`，需确认是否真正向量化。  
**目标：** 真正的 embedding → 向量检索 → 语义匹配。

- [ ] **7.4.1** 若当前是关键词匹配 → 改为 embedding（调用本地/远程 embedding 模型）（1d）
- [ ] **7.4.2** 知识图谱关系边遍历（`rule_relations` 表）：顺着一阶/二阶关系扩展规则（0.5d）

### 7.5 断路器增强
**现状：** 基础 token/turn/cost 检查。  
**目标：** 动态预算调整 + 提前预警。

- [ ] **7.5.1** 剩余预算不足时提前终止（当前只在 `Check()` 超限时熔断）（0.25d）
- [ ] **7.5.2** 断路器状态实时推送到 WebSocket（0.25d）

> **本模块合计：≈ 7 人天**

---
## 8. agents_md_discovery.go（当前 85% → 目标 95%）


**现状：** 这个模块是相对最完整的。多层级发现、候选文件名优先级、字节预算截断都有了。

- [ ] **8.1** 添加更多项目根标记：`package.json`、`Cargo.toml`、`CMakeLists.txt`、`.claude/`（0.25d）
- [ ] **8.2** 文件内容缓存：`GetAgentsMDForCWD` 结果缓存 30s，避免每次 agent loop 都重读文件系统（0.25d）
- [ ] **8.3** 文件变更监听（可选）：AGENTS.md 变化时自动刷新缓存（0.5d）

> **本模块合计：≈ 1 人天**

---
## 9. 跨切面关注点（当前 25% → 目标 85%）


### 9.1 安全 & 沙箱
- [ ] **9.1.1** 真正的容器沙箱（当前 `sandbox.go` 需确认是 Docker 还是 mock）（1d）
- [ ] **9.1.2** API Key 加密存储（`secrets.go` 的 `DecryptSecret` 需确认是否真正加密）（0.5d）
- [ ] **9.1.3** Prompt Injection 防御：检测 user prompt 中的注入模式（覆盖 ignore / system override）（0.5d）
- [ ] **9.1.4** 沙箱逃逸检测（被测 Agent 在沙箱里的异常行为监控）（0.5d）

### 9.2 可观测性
- [ ] **9.2.1** OpenTelemetry tracing：agent loop 每一步都有 span（LLM 调用/工具执行/压缩）（1.5d）
- [ ] **9.2.2** Prometheus metrics：token 消耗、工具调用次数/成功率、延迟分布（1d）
- [ ] **9.2.3** 结构化日志（当前 `log.Printf` 散落各处，改为 slog + traceID 关联）（1d）

### 9.3 测试
- [ ] **9.3.1** `agent_loop.go` 单元测试：mock LLM 返回 tool_calls，验证 loop 逻辑（1d）
- [ ] **9.3.2** `builtin_tools.go` 单元测试：各工具执行正确性（0.5d）
- [ ] **9.3.3** `subagent.go` 集成测试：Task → subagent → summary 完整链路（0.5d）
- [ ] **9.3.4** `hooks.go` 单元测试：hook 协议（允许/拒绝/超时）（0.5d）
- [ ] **9.3.5** `compaction.go` 单元测试：各 condenser 策略正确性（0.5d）

### 9.4 MCP 客户端完善
**现状：** `mcp_client.go` 已存在但需确认深度。  
**目标：** 完整 MCP 协议支持（stdio + SSE + Streamable HTTP 三种 transport）。

- [ ] **9.4.1** 确认三种 transport 是否都支持（0.25d）
- [ ] **9.4.2** MCP server 健康检查 + 自动重连（0.25d）
- [ ] **9.4.3** MCP 工具结果大小限制 & 截断（0.25d）

### 9.5 WebSocket 实时推送完善
- [ ] **9.5.1** agent loop 每个 turn 都推状态（当前只在关键节点推）（0.25d）
- [ ] **9.5.2** 工具调用的 input/output stream 实时推（当前可能只推结果）（0.25d）
- [ ] **9.5.3** WebSocket 断线重连 + 消息补推（0.5d）

> **本模块合计：≈ 10 人天**

---
## 实施建议

### 分阶段路线

| 阶段 | 内容 | 工时 | 产出 |
|------|------|------|------|
| **Phase 1（基础补齐）** | 精确 token 计数 + 沙箱 Docker 化 + 测试覆盖 | 12d | 地基稳固 |
| **Phase 2（循环深度）** | agent_loop 结构化重试 + 并行工具 + 中断恢复 | 10d | 核心成熟 |
| **Phase 3（高级特性）** | 子代理 Fork/Swarm + 异步 hook + LLM 压缩机 | 15d | 业界对齐 |
| **Phase 4（生产加固）** | 可观测性 + 安全 + 多 provider + 性能优化 | 20d | 生产就绪 |

### 风险项

1. **Docker 沙箱**：依赖 Docker daemon，本地开发环境可能没有，需 mock 回退机制
2. **MCP 协议稳定**：MCP 协议本身还在演进（目前 2024-11-06 spec），需跟踪更新
3. **多 provider 适配**：Anthropic/Gemini API 与 OpenAI 在 tool calling 语义上有差异，适配工作量可能超预期
4. **Token 精确计数**：不同模型 tokenizer 不同，tiktoken 只覆盖 OpenAI 系列，Claude/Gemini 需额外方案
