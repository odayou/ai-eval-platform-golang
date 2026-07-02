# 技术债清单与迁移指引

> 本文档记录 2026-07-01 代码审查发现、本次已修复项、以及因工作量大未在本次完成需后续渐进迁移的项。
> 每项标注:现状 / 风险 / 迁移步骤 / 验证方式。

---

## ✅ 本次已修复(2026-07-01)

| 编号 | 问题 | 修复方式 | 涉及文件 |
|------|------|---------|---------|
| P0-#2 | `MaxConcurrentEvals` 配置断裂(实际未传给 scheduler) | `InitGlobal` 加 worker 数参数,main 传入,默认改 1 | `scheduler/global.go` `cmd/server/main.go` `config/config.go` |
| P0-#1 | 并发竞态:包级全局审计/沙箱状态在并发 worker 下串台 | 默认 worker=1 串行;`SetAuditContext` 加并发守卫告警;归因 goroutine 显式清上下文 | `engine/audit.go` `engine/engine.go` |
| P0-#4 | SQLite 写竞争(database is locked) | 随 worker 串行(#1)一并解决 | — |
| P1-#8 | panic 兜底丢失堆栈 | `recover` 后补 `debug.Stack()` | `engine/engine.go`×2 `scheduler/cron.go` |
| P1-#10 | CORS AllowOrigins 写死 localhost | `CORS_ORIGINS` 环境变量驱动 | `api/router.go` `config/config.go` |
| P1-#9 | mockRetry 模板 tab/空格缩进混杂 | 统一缩进(%% 转义实为正确,未改) | `engine/agentclient.go` |
| P1-#5 | 无 HTTP 限流,可被灌队列/白嫖 LLM | 新增 per-IP 令牌桶中间件 + `RATE_LIMIT`/`RATE_BURST` 配置 + 回归测试 | `api/middleware.go`(新) `api/middleware_test.go`(新) `api/router.go` `config/config.go` |
| 附带 | `benchmark_test.go` 落后于引擎演进(RunRoundtable 少参数、InitGlobal 少参数) | 补齐参数,`go vet ./...` 全绿 | `benchmark_test.go` `scripts/acceptance_test.go` |

---

## 🔜 后续待迁移(本次未做,因工作量大需专项)

### TD-1: engine 包级全局状态 → EvalContext 化 ✅ 已完成(2026-07-02)

**状态**:已完成。采用 goroutine-local 绑定方案,而非原计划的 context.Context 透传。

**实现**:
- 新增 `engine/evalctx.go`:`EvalContext` 结构体 + per-goroutine 绑定(`sync.Map[goroutineID]*EvalContext`)。goroutine ID 通过 `runtime.Stack()` 解析(成熟做法)。
- 删除全部包级变量(`currentAuditTaskID`/`currentAuditTurn`/`currentAuditCallerVar`/`currentSandboxPermLevelVar`)。
- `SetAuditContext`/`SetAuditCaller`/`ClearAuditContext`/`SetSandboxPermLevel` 签名不变(向后兼容),内部改为操作 per-goroutine 的 EvalContext。
- `RecordLLMAudit`/`RecordToolAudit`/沙箱预检改读 `currentXxxSafe()`(per-goroutine)。
- `engine.Run`/api 入口 `BindEvalContext` + defer `UnbindEvalContext`。
- agent_loop 并行工具子 goroutine 用 `BindChildEvalContext` 继承父上下文。
- `MAX_CONCURRENT_EVALS` 默认从 1 上调到 2(并发解锁);SQLite 连接池 3→6。

**验证**:`evalctx_test.go` 含并发隔离测试(20 goroutine × 100 读 = 0 串台)、子继承测试、未绑定默认值测试。全量 `go test` 通过。

**为什么不用 context.Context 透传**:调用链过深(Run→InvokeAgent→callProviderWithRetry→RecordLLMAudit),且 callProvider* 是通用 LLM 封装,加 ctx 参数会波及几百处调用方。goroutine-local 绑定对现有签名零侵入。

### TD-2: 日志统一 slog/log.Printf(P2-#6) ✅ 已完成(2026-07-02)

**状态**:engine 包已完成统一。其余包(api/scheduler/store/proxy/ws)为渐进迁移。

**实现**:
- `engine/slog.go` 新增 `NewTaskSlog(module)`:自动从当前 goroutine 的 EvalContext 注入 task_id(配合 TD-1)。
- `engine.go`(31 处)手工转换为 `NewTaskSlog("engine").Info/Error(...)`。
- 其余 37 个 engine 文件(141 处)用脚本批量转换 `log.Printf("[module] ...")` → `NewSlog("module").Info/Error(...)`。
- engine 包 `log.Printf` 归零(`grep` 仅剩 slog.go 注释里的示例文本)。

**验证**:`grep -rn "log.Printf" internal/engine/*.go | grep -v _test | grep -v slog.go` = 0。全量 `go test` 通过。

**剩余(后续渐进)**:api(13)/scheduler(24)/store(10)/proxy(30)/ws(2) 包的 `log.Printf` 待迁移。模式相同,可用同一脚本批处理。

### TD-3: 收敛 store.DB 直接调用(P2-#3) 🔄 进行中(2026-07-02)

**状态**:6 张核心表(agents/tasks/models/questions/rules/judge_models)的 CRUD 已完整收敛。其余表为同模式重复工作。

**本次完成(6 张核心表)**:
| 表 | 新增 store 方法 | 收敛调用 | 残留(合理保留) |
|----|----------------|---------|---------------|
| agents | 7 个(List/Get/Insert/Update/UpdateOnline/Delete/Count) | 9处→0 | — |
| tasks | 6 个(List/GetSummary/Insert/Delete/Count/StatusStats) | 8处→0 | — |
| models | 7 个(List/ListPublic/GetPublic/Insert/Update/Delete/Count) | api层6处→0 | engine健康检查/路由/probe 5处(运行时逻辑) |
| questions | 4 个(Get/Insert/Update/Delete) | 7处→0 | baseline.go 1处(特殊desensitized语义) |
| rules | 6 个(List/Insert/InsertIgnore/Update/Delete/Count) | 8处→0 | — |
| judge_models | 2 个(UpdateDimension/DeleteByRole) | 2处→0 | — |

**新增文件**:`store/agents_crud.go`、`tasks_crud.go`、`models_crud.go`、`questions_crud.go`、`rules_crud.go`(judge_models 方法补在 query.go)。

**核心收益**:
- 创建 Agent/Question/Rule 的 SQL 各有 3~4 处重复,现分别统一到 `store.InsertAgent`/`InsertQuestion`/`InsertRule`。
- 6 张核心表的 schema 改动只需改 store 层一处。
- 整体直接调用从 **181 处降到 138 处**(6 张核心表 CRUD 清零)。

**关键设计决策**:
- `nullStr` 等 NULL 处理收敛进 store 层,api 层不再关心 SQL NULL 语义。
- models 表 api_key 加密留在 api 层(`engine.EncryptSecret`),store 层 `ModelRow.APIKey` 存加密值,保持分层纯净。
- 动态多条件查询(如 `listQuestions` 的 WHERE 拼装)保留在 api 层——收敛价值低且复杂。
- ruleio 批量导入用 `InsertRuleIgnore`(INSERT OR IGNORE)语义。

**剩余收敛路线图**(按调用密度排序,同模式):
| 表 | 待收敛调用 | 涉及文件 | 备注 |
|----|-----------|---------|------|
| scenes | ~6 处 | api/scene.go | 标准 CRUD |
| workflows/workflow_runs | ~6 处 | api/workflow*.go | 标准 CRUD |
| pipelines/pipeline_runs | ~6 处 | api/pipeline*.go | 标准 CRUD |
| 其他(redteam_scans/training_datasets/...) | ~110 处 | api 各 handler | 同模式 CRUD |
| models(engine层) | 5 处 | agentclient/model_router/probe | 运行时健康检查,可选收敛 |

**最终目标**:收敛完成后抽 `Store` interface,使 `engine.Run` 可在不依赖真实 SQLite 的情况下跑单测(用内存/假实现注入)。

### TD-4(可选): 日志/审计的敏感信息脱敏审计

**现状**:`RedactText` 已用于会话落库前脱敏。TD-2 完成后,日志已走 slog,可在 slog 层加统一的脱敏 hook(对 system_prompt/prompt/api_key 字段自动脱敏),避免各调用点手工脱敏遗漏。

---

## 本次修复的验证清单

```bash
cd ai-eval-platform
go build ./...                    # ✅ 通过
go vet ./...                      # ✅ 全绿
go test ./internal/...            # ✅ engine/api/scheduler/store 全通过
```
