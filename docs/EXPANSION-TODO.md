# EXPANSION TODO — 6 大拓展方向全做

> 项目从"评测平台"升级为"AI Agent 全生命周期保障平台"。
> 依赖排序:基础设施(#4)→ 复用现有(#1/#2/#3)→ 新能力(#5/#6)

## 方向 1: Agent CI/CD Pipeline
> 代码提交→自动构建Agent→回归评测→性能/安全/正确性三维度→gate→部署→监控→退化告警→回滚

- [ ] 1.1 DB: pipelines 表(id/agent_id/trigger/gate_config/status) + pipeline_runs 表
- [ ] 1.2 引擎: pipeline.go — Webhook触发→clone基准→跑题库→三维度评分→gate判定→报告
- [ ] 1.3 API: POST /pipelines (创建) + POST /pipelines/:id/trigger (手动触发) + GET /pipelines/:id/runs
- [ ] 1.4 前端: Pipeline.vue — 管线列表 + 运行历史 + gate 结果可视化
- [ ] 1.5 Webhook: POST /webhook/git — 接收 git push 触发自动评测

## 方向 2: Agent Arena（排行榜+对战平台）
> 开发者提交Agent→自动跑题库→多维度打分→ELO评分→对战回放→实时排行榜

- [ ] 2.1 DB: arena_submissions 表(agent_id/scores/elo_rating/battles) + arena_battles 表
- [ ] 2.2 引擎: arena.go — 提交→跑标准题库→多维度评分→ELO计算→对战配对
- [ ] 2.3 API: POST /arena/submit + GET /arena/leaderboard + GET /arena/battles/:id/replay
- [ ] 2.4 前端: Arena.vue — 排行榜 + ELO 评分 + 对战详情

## 方向 3: Red Team as a Service（安全测试服务）
> 客户提交Agent→跑injection/泄露/越狱/权限绕过→生成安全报告(CVSS风格)

- [ ] 3.1 DB: redteam_scans 表(agent_id/scan_config/results/cvss_score/report)
- [ ] 3.2 引擎: redteam.go — 6类安全测试(injection/leakage/jailbreak/privilege/poison/side_channel)
- [ ] 3.3 API: POST /redteam/scan + GET /redteam/scans/:id + GET /redteam/scans/:id/report
- [ ] 3.4 前端: RedTeam.vue — 安全报告(风险等级/发现项/修复建议/CVSS雷达图)

## 方向 4: Agent Observability（生产监控）
> Agent在生产跑→事件流实时回传→面板(token/cost/延迟/错误率)→异常检测→告警

- [ ] 4.1 DB: agent_metrics 表(agent_id/timestamp/token/cost/latency/error_count) + alert_rules 表
- [ ] 4.2 引擎: observability.go — 实时指标采集+时序聚合+异常检测+告警触发
- [ ] 4.3 API: POST /observe/event (上报事件) + GET /observe/dashboard (聚合面板)
- [ ] 4.4 前端: Observability.vue — 实时面板(成本曲线/延迟分布/错误率/告警列表)
- [ ] 4.5 SDK: agent上报SDK(python/http) — 生产Agent集成后自动上报

## 方向 5: Agent Fine-tuning Pipeline（评测反哺训练）
> 评测bad case→归因分析→自动生成训练样本(SFT/DPO)→导出→微调→回归评测

- [ ] 5.1 DB: training_datasets 表(task_id/bad_cases/format/sft_pairs/dpo_pairs)
- [ ] 5.2 引擎: finetune.go — 评测bad case→attribution归因→生成SFT/DPO样本→验证
- [ ] 5.3 API: POST /finetune/export (导出数据集) + GET /finetune/datasets
- [ ] 5.4 前端: FineTune.vue — 数据集管理 + bad case 查看 + 导出

## 方向 6: Multi-Agent Orchestration（多Agent协作）
> 用户定义工作流(DAG)→平台调度多Agent协作→独立harness→中间结果传递→冲突仲裁→汇总

- [ ] 6.1 DB: workflows 表(dag_json/status) + workflow_runs 表
- [ ] 6.2 引擎: orchestrator.go — DAG解析→节点调度→Agent协作→结果传递→冲突仲裁
- [ ] 6.3 API: POST /workflows (创建) + POST /workflows/:id/run + GET /workflows/:id/status
- [ ] 6.4 前端: Workflow.vue — DAG编辑器 + 运行可视化 + 节点状态
