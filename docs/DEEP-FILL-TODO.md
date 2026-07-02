# DEEP-FILL TODO — 从骨架到完整实现

> 诚实审计:当前后端 24K 行 + 前端 8K 行 = 32K 行。
> Claude Code cli.js 单文件几十万行,codex Rust 130 个 crate。
> 我们的骨架文件 100-290 行,真正实现需要 500-2000 行/文件。

## 审计:每个文件缺什么

### agent_loop.go (442行 → 目标 1200+)
- [ ] D1. **流式输出**:当前 callLLMWithTools 是非流式,需要 SSE stream 支持实时输出
- [ ] D2. **中途压缩**:ReAct 循环中消息超阈值时调 Condense(接入 compaction.go 6 种策略)
- [ ] D3. **工具执行超时+取消**:当前工具执行无超时,需要 per-tool timeout + 全局 cancel signal
- [ ] D4. **并发工具执行**:当前串行执行 tool_calls,需要支持并行执行无依赖的工具调用
- [ ] D5. **工具结果大小限制**:当前无截断,需要 max_tool_output_tokens(codex 默认 10000)
- [ ] D6. **错误恢复**:工具执行失败后 agent 应能自我修复,当前只是回填 error
- [ ] D7. **token 预算中途检查**:每轮后检查剩余预算,超限时压缩而非直接停

### guardian.go (226行 → 目标 800+)
- [ ] D8. **完整 7 种审批请求**:Shell/ExecCommand/Execve/ApplyPatch/NetworkAccess/McpToolCall/RequestPermissions
- [ ] D9. **转录压缩算法**:消息和工具调用分设备预算(各 10K),保留首尾锚点,从新到旧填充
- [ ] D10. **policy.md 完整策略**:数据外泄/凭据探测/持久安全削弱/破坏性操作 4 类详细判定规则
- [ ] D11. **滑动窗口断路器**:50 窗口内 10 次拒批(当前只有连续 3 次的简单版)
- [ ] D12. **策略模板**:policy_template.md 含 {tenant_policy_config} 占位符

### apply_patch.go (225行 → 目标 600+)
- [ ] D13. **Lark 形式文法**:完整 EBNF 文法定义 + 验证
- [ ] D14. **StreamingPatchParser**:流式解析(不是一次性 split)
- [ ] D15. **模糊匹配**:@@ 上下文锚点找不到时做模糊匹配(而非直接失败)
- [ ] D16. **多文件原子应用**:多个文件变更要么全成功要么全回滚(事务)
- [ ] D17. **diff 验证**:应用后验证结果是否符合预期(可选 diff 校验)

### hooks.go (334行 → 目标 700+)
- [ ] D18. **LLM hook 类型完整实现**:当前 ExecuteLLMHook 有骨架,需要完整 $ARGUMENTS 替换 + JSON 输出解析
- [ ] D19. **Agentic verifier hook 完整实现**:当前 ExecuteVerifierHook 有骨架,需要完整子 agent 启动
- [ ] D20. **hook timeoutMs 按事件配置**:不同事件不同超时(PreToolUse 快,Stop 慢)
- [ ] D21. **hook 执行顺序保证**:同事件多 hook 按 priority 排序(当前是注册顺序)
- [ ] D22. **hook stdout JSON 完整协议**:updatedInput/additionalContext/permissionDecision 全字段

### permission_gate.go + permission_mode.go (396+169行 → 目标 900+)
- [ ] D23. **Tool(specifier) 完整解析**:支持 glob 通配符 `Edit(docs/**)` / `Bash(npm run*)`
- [ ] D24. **规则优先级引擎**:deny > ask > allow > defaultMode 四级优先级链
- [ ] D25. **规则热加载**:从 settings.json 加载规则 + 运行时动态修改
- [ ] D26. **审批状态缓存**:已批准的命令前缀缓存(不重复问)

### agentclient.go (488行 → 目标 1000+)
- [ ] D27. **流式 SSE 解析**:当前全部非流式,需要 SSE event 解析(data: / [DONE])
- [ ] D28. **重试+指数退避**:429/500 错误自动重试 + 指数退避
- [ ] D29. **多 provider 适配**:不同 provider(OpenAI/Anthropic/Gemini)的消息格式差异处理
- [ ] D30. **usage 完整解析**:cache_read_input_tokens/cache_creation_input_tokens 从响应里解析

### mcp_client.go (239行 → 目标 500+)
- [ ] D31. **stdio transport**:当前只有 HTTP,需要 stdio(子进程 JSON-RPC over stdin/stdout)
- [ ] D32. **SSE transport**:Server-Sent Events transport(MCP 规范之一)
- [ ] D33. **工具发现缓存**:tools/list 结果缓存,避免每次 agent 调用都重新拉
- [ ] D34. **连接池+重连**:MCP server 断开后自动重连

### subagent.go (168行 → 目标 500+)
- [ ] D35. **自定义子代理**:从 agent_hooks 表加载自定义子代理定义(YAML frontmatter .md)
- [ ] D36. **子代理工具作用域**:不同子代理类型限制不同工具集
- [ ] D37. **递归深度限制**:单层 guard 已有,需要可配置的深度限制

### skills_loader.go (138行 → 目标 400+)
- [ ] D38. **三级 skill 来源**:managed(管理员)/user(~/.claude/skills)/project(.claude/skills)
- [ ] D39. **skill 触发条件匹配**:description 里的触发词匹配(而非全量注入)
- [ ] D40. **skill 版本管理**:skill 更新后缓存失效

### builtin_tools.go (245行 → 目标 600+)
- [ ] D41. **FileEdit 工具**:当前只有 FileWrite(全文重写),需要 FileEdit(部分编辑 SEARCH/REPLACE)
- [ ] D42. **Glob 工具**:文件模式匹配(**/* go / src/**.py)
- [ ] D43. **Grep 工具**:内容搜索(正则 + 文件过滤)
- [ ] D44. **BashTool 输出流式**:当前 CombinedOutput 一次性,需要流式逐行

### engine.go (548行 → 目标 1000+)
- [ ] D45. **专家配置完整性**:用户创建的专家组需支持内嵌 prompt_text 优先级链完整执行
- [ ] D46. **断路器+agent loop 集成**:agent_loop 的 maxTurns 与 breaker 的 MaxTurns 联动
- [ ] D47. **评测报告增强**:报告含事件流 replay + tool 调用统计 + 双路 token 趋势

### pipeline.go (177行 → 目标 500+)
- [ ] D48. **完整评测集成**:当前评分是硬编码 75/80/70,需要真实跑 judge/roundtable
- [ ] D49. **退化检测完整实现**:与上次基准的 all 维度对比(不只是 correctness)
- [ ] D50. **gate 配置完整解析**:min_score/max_cost/max_regression 从 gate_config JSON 读
- [ ] D51. **通知机制**:管线通过/失败后发通知(webhook/email)

### redteam (API 内联 → 目标独立文件 600+)
- [ ] D52. **独立 redteam.go**:从 expansion.go 抽出来
- [ ] D53. **完整 6 类攻击 payload 库**:每类 20+ 个真实 payload
- [ ] D54. **响应分析器**:不简单检测关键词,用 LLM 判断 agent 是否"上当"
- [ ] D55. **CVSS 向量计算**:根据 AV/AC/PR/UI 计算 CVSS 3.1 向量分数
- [ ] D56. **修复建议引擎**:根据漏洞类型自动生成修复建议

### observability (API 内联 → 目标独立文件 500+)
- [ ] D57. **独立 observability.go**:从 expansion.go 抽出来
- [ ] D58. **时序聚合**:1min/5min/1hour/1day 多粒度聚合(不是只有全部)
- [ ] D59. **异常检测算法**:不只是阈值,需要移动平均+标准差异常检测
- [ ] D60. **告警渠道**:webhook/email/slack 多渠道通知

### expansion.go (800行 → 拆分成 6 个文件)
- [ ] D61. **拆分 expansion.go**:pipeline/arena/redteam/observe/finetune/workflow 各独立文件

### 前端缺失
- [ ] D62. **Dashboard 增强**:含双路 token 趋势图 + 事件流时间线
- [ ] D63. **Pipeline 可视化**:gate 结果雷达图 + 退化趋势线图
- [ ] D64. **Arena ELO 图**:ELO 评分变化折线图 + 对战详情
- [ ] D65. **RedTeam CVSS 雷达图**:6 维度安全评分可视化
- [ ] D66. **Observability 实时面板**:WebSocket 实时推送 + 成本/延迟曲线
- [ ] D67. **Workflow DAG 编辑器**:拖拽式节点编辑 + 运行可视化

### 测试缺失
- [ ] D68. **agent_loop 测试**:ReAct 循环 + 工具执行 + Guardian 审批的单测
- [ ] D69. **apply_patch 测试**:各种 patch 格式的解析+应用测试
- [ ] D70. **Guardian 测试**:断路器 + fail-closed + 策略校验测试
- [ ] D71. **redteam 测试**:6 类攻击的响应分析测试
- [ ] D72. **pipeline 测试**:gate 判定 + 退化检测测试
