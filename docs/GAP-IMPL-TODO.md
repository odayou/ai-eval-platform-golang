# gap-impl TODO — 部分实现 + 未实现缺口补齐

> 依据专家评审（3 组子代理对照 53 个 skill 审计），按依赖关系排序。
> 每项改完立即 go build 验证。

## 已完成（之前几轮做的）
- ✅ agent_loop.go ReAct 循环 + builtin_tools + mcp_client + skills_loader + subagent + hooks + backpressure（harness 六层骨架）
- ✅ 专家组 19/25/27 已完整实现

## 实施顺序（18 项）

### 第一组：已有文件扩展（改动最小，先做）
- [ ] 1. #46 condenser 补5种策略 — 扩展 compaction.go 加 CondenserType 枚举 + noop/masking/recent/amortized/llm_attention
- [ ] 2. #52 双路token统计 — compaction.go 的 summarizeChunk 返回 token + agent_loop.go 分桶累加 + stats 表加字段
- [ ] 3. #40 stats-cache-8dim — 新建 stats_cache.go 加 cacheRead/cacheWrite/webSearch/cost 四维 + dailyActivity/hourCounts
- [ ] 4. #36 9-hook 补6种事件 — hooks.go HookEvent 加 SubagentStop/UserPromptSubmit/PreCompact/SessionStart/SessionEnd/Notification + LLM hook 类型
- [ ] 5. #37 权限补plan/bypass+ask三态 — permission_gate.go 加 PermissionMode 枚举 + allow/deny/ask 规则 + Tool(specifier) 解析
- [ ] 6. #49 secrets 加密存储 — 新建 secrets.go AES-GCM 加密 + 序列化脱敏 + expose 协议

### 第二组：新建文件（中等改动）
- [ ] 7. #17 Guardian LLM审批器 — 新建 guardian.go 二级LLM审批 + 断路器 + fail-closed + 接入 agent_loop PreToolUse
- [ ] 8. #21 apply-patch DSL — 新建 apply_patch.go Begin/End Patch parser + applier + 注册为工具
- [ ] 9. #35 TodoWrite 工具 — 新建 todo.go TodoItem{content,status,activeForm} + 原子写 + 注册为工具
- [ ] 10. #28 config-layer-stack — 改 config.go 加多源合并(global/project/local/runtime) + sticky 字段
- [ ] 11. #26 目标/预算补objective — 新建 goal.go GoalManager{objective,budget,untrusted} + 注入 agent_loop

### 第三组：较大改动
- [ ] 12. #23 AGENTS.md文件系统多层发现 — 新建 agents_md.go 从cwd向上找.git+逐层收集+override优先
- [ ] 13. #24 tool-search-deferred — 改 agent_loop.go 工具分always-loaded+deferred + tool_search元工具
- [ ] 14. #43 事件流 — 新建 event_stream.go 9种事件+EventStorage+emit/replay + 接入agent_loop
- [ ] 15. #29 shell拦截syscall级 — 新建 syscall_guard.go seccomp(Linux)+ptrace prctl
- [ ] 16. #31 sidechain resume — 改 subagent.go 加 agentId+独立transcript+SendMessage续聊
- [ ] 17. #18 原生沙箱 — 新建 native_sandbox.go seatbelt(.sbpl)+bubblewrap(bwrap)

### 最终
- [ ] 18. go build + go test + npm build 全绿
