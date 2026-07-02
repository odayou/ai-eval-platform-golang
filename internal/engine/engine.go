package engine

// engine.go — 评测引擎 Runner,平台的核心。
//
// 主流程(② 引擎核心 + ③④ 接线):
//   1. 读 task → 意图路由(router.go)→ 决定执行策略
//   2. 解析 rule_set → 取规则列表
//   3. 读题目(按 task.scene 过滤)
//   4. 初始化断路器(breaker.go)全程监控 token/cost/turns
//   5. 逐题:
//      a. 调被测 Agent(agentclient.go)
//      b. 断路器 Check → 超限则熔断 status=blown,中止
//      c. 按策略打分:
//         - 圆桌模式(默认):RunRoundtable(多专家并发,内部已含 metric+llm)
//         - 单判官模式:ScoreQuestion
//      d. agent 场景走沙箱(sandbox.go)采集硬指标
//      e. 记 conversation + Bad Case
//   6. 聚合成报告 → 写回 task.result + 标记 done
//
// 异步执行:由 scheduler.Submit 入队(⑤),worker pool 消费调 Run。
// 状态机: pending → running → done/failed/blown

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"ai-eval-platform/internal/store"
	"ai-eval-platform/internal/ws"

	"github.com/google/uuid"
)

// runOptions 是单次评测的运行时选项,由意图路由填充。
type runOptions struct {
	useRoundtable bool // 是否用圆桌合议(默认 true);model 场景可关
	useSandbox    bool // 是否走沙箱编译(agent 场景 true)
}

// Run 执行一次评测任务。由 scheduler 的 worker 调用(或裸 go Run 调用)。
// 全程不 panic:任何步骤失败都标记 task=failed 并记录原因。
func Run(taskID string) {
	start := time.Now()
	// 绑定 per-goroutine 的 EvalContext(并发安全的核心,见 evalctx.go)。
	// 此后当前 worker goroutine 内的所有审计/沙箱读取都隔离于此 taskID。
	BindEvalContext(taskID)
	SetAuditContext(taskID, 0)
	defer func() {
		ClearAuditContext()
		UnbindEvalContext()
	}()

	// 进入 running 状态
	if err := store.UpdateTaskStatus(taskID, "running"); err != nil {
		NewTaskSlog("engine").Error("状态更新失败", "error", err)
		return
	}

	// 失败兜底:任何 panic/error 都标记 failed
	defer func() {
		if r := recover(); r != nil {
			// 记录完整堆栈,排查线上 panic 必备(只记 r 会丢失调用栈)
			NewTaskSlog("engine").Error("评测 panic", "error", r, "stack", string(debug.Stack()))
			store.UpdateTaskStatus(taskID, "failed")
		}
	}()

	// 1. 读任务 + Agent + 规则集 + 题目
	task, agent, rules, questions, err := loadTaskContext(taskID)
	if err != nil {
		NewTaskSlog("engine").Error("加载上下文失败", "error", err)
		store.UpdateTaskStatus(taskID, "failed")
		return
	}

	// 1b. 意图路由:根据场景决定执行策略(router.go)
	opts := resolveStrategy(task)
	// 主流程日志器:自动携带当前 goroutine 的 task_id(见 slog.go NewTaskSlog)
	slog := NewTaskSlog("engine")
	slog.Info("评测任务开始",
		"name", task.Name, "agent", agent.Name,
		"rules", len(rules), "questions", len(questions),
		"roundtable", opts.useRoundtable, "sandbox", opts.useSandbox)

	// 1b-bis. 设置沙箱权限级别(MRD §5.6):被测 Agent 在沙箱/终端执行代码的权限级别。
	//   沙箱预检被测代码内容,拦截嵌入的极端自毁命令;终端轨道门控启动命令。
	if agent.PermissionLevel == "" {
		agent.PermissionLevel = "workspace-write"
	}
	SetSandboxPermLevel(agent.PermissionLevel)
	slog.Info("被测 Agent 权限级别", "perm_level", agent.PermissionLevel)

	if len(questions) == 0 {
		slog.Info("无题目,跳过")
		writeEmptyReport(taskID, "该场景暂无题目,评测未执行")
		ws.Emit(taskID, "task_done", ws.TaskDoneData{TaskID: taskID})
		return
	}

	// 1c. 初始化断路器(breaker.go)—— 钱包保护,全程监控
	breaker := NewBreakerState(DefaultBreakerConfig())
	slog.Info("断路器就绪",
		"max_tokens", breaker.Config.MaxTokens,
		"max_turns", breaker.Config.MaxTurns,
		"max_cost_usd", breaker.Config.MaxCostUSD)

	// 1d. 构建 RAG 规则索引(④.4):规则多了不爆 token,每题只检索相关的。
	//     索引构建是幂等的(已存在则复用),首次构建后进程内复用。
	ragIdx := GetRAGIndex()
	if ragIdx != nil && len(ragIdx.embeddings) > 0 {
		slog.Info("RAG 索引就绪", "rules", len(ragIdx.embeddings))
	}

	// 1e. Agent 能力画像分析(自适应评测核心)。
	//   分析 Agent 的 SystemPrompt + 示例对话,生成能力画像。
	//   画像驱动选题(专项题+通用筛选)和专家评审(注入能力上下文)。
	agentInfo := agent.Name
	if agent.SystemPrompt != "" {
		agentInfo += "\n\nSystem Prompt:\n" + agent.SystemPrompt
	}
	if agent.ExampleConversations != "" {
		agentInfo += "\n\n示例对话:\n" + agent.ExampleConversations
	}
	agentProfile := BuildAgentProfile(agentInfo)
	slog.Info("Agent 画像",
		"category", agentProfile.Category, "domain", agentProfile.Domain,
		"capabilities", agentProfile.Capabilities, "mock", agentProfile.IsMock)

	// 1f. 自适应选题: 专项题(画像驱动) + 通用题筛选(保留核心素养)
	if !agentProfile.IsMock && len(questions) > 0 {
		// 筛选通用题:过滤与 Agent 能力完全无关的题,保留安全/错误处理等核心素养题
		filtered := []store.QuestionRow{}
		filteredCount := 0
		for _, q := range questions {
			if agentProfile.ShouldFilterQuestion(q.Prompt) {
				filteredCount++
				continue // 过滤掉无关题
			}
			filtered = append(filtered, q)
		}
			if filteredCount > 0 {
				slog.Info("选题适配:过滤无关题", "filtered", filteredCount, "kept", len(filtered))
			}

		// 动态生成专项题(按画像能力)
		adaptiveQuestions := GenerateAdaptiveQuestions(agentProfile, 3)
			if len(adaptiveQuestions) > 0 {
				slog.Info("动态生成专项题", "count", len(adaptiveQuestions), "category", agentProfile.Category)
			for _, aq := range adaptiveQuestions {
				filtered = append(filtered, store.QuestionRow{
					ID:     "adaptive-" + taskID[:8] + "-" + aq.Title[:8],
					Scene:  task.Scene,
					Title:  aq.Title,
					Prompt: aq.Prompt,
					Source: "adaptive",
				})
			}
		}

		// 用适配后的题库替换(保留至少原有题量做兜底)
		if len(filtered) > 0 {
			questions = filtered
		}
			slog.Info("最终题库", "count", len(questions))
	}

	// 2. 逐题评测
	results := []QuestionResult{}
	anyMock := false
	blown := false
	var chaosResults []ChaosResult // 混沌工程结果(仅 chaos_enabled 时收集)
	var leakageResults []LeakageResult // 反泄漏扫描结果(每题一个,V2 9.5)

	for i, q := range questions {
		slog.Info("开始评测题目", "turn", i+1, "total", len(questions), "title", q.Title)
		SetAuditContext(taskID, i+1) // 更新审计上下文 turn

		// WebSocket 事件:开始评测第 N 题
		ws.Emit(taskID, "question_start", ws.QuestionStartData{
			TaskID: taskID, Turn: i + 1, Total: len(questions), Title: q.Title,
		})

		// 2a-pre. RAG 规则检索(④.4 + §7.3 知识图谱):用题目检索 top-K 相关规则,
		//   并顺着 rule_relations 关系边拉出关联规则,与 rule_set 合并去重。
		//   好处:测 SQL 题时不会把"文案风格规则"带进来,且能顺着关系边覆盖语义检索之外的关联维度。
		//   策略:rule_set 规则(用户意图)必跑 + RAG 检索+关系边规则(语义+图谱关联)补充。
		questionRules := rules // 默认用任务规则集
		if ragIdx != nil && len(ragIdx.embeddings) > 0 {
			questionRules = mergeRulesByRAG(rules, ragIdx.SearchWithRelatedForQuestion(q, 3))
		}

		// 2a. 断路器预检(在调 Agent 前检查累计是否已超限)
		if err := breaker.Check(0, 0); err != nil {
			slog.Warn("断路器熔断(预检)", "error", err)
			blown = true
			break
		}

		// 2b. 调被测 Agent(统一入口:api 走 ChatAgent,terminal 走 RunTerminal)
		SetAuditCaller("agent")
		cr := InvokeAgent(*agent, q.Prompt)
		if cr.IsMock {
			anyMock = true
		}

		// WebSocket 事件:Agent 回答完成
		answerPreview := cr.Text
		if len([]rune(answerPreview)) > 200 {
			answerPreview = string([]rune(answerPreview)[:200]) + "..."
		}
		ws.Emit(taskID, "agent_response", ws.AgentResponseData{
			TaskID: taskID, Turn: i + 1,
			AnswerPreview: answerPreview,
			TokenIn: cr.TokenIn, TokenOut: cr.TokenOut,
		})

		// 2c. 断路器记账(实际 token 消耗)
		if err := breaker.Check(cr.TokenIn, cr.TokenOut); err != nil {
			slog.Warn("断路器熔断", "error", err)
			LogBreakerStatus(breaker, taskID)
			store.UpdateTaskStatus(taskID, "blown")
			blown = true
			// 已评测的题仍然写入报告(部分结果),只是中止后续
			break
		}

		// 2d. 打分 —— 按策略二选一,避免重复调用(圆桌内部已含 metric+llm)
		var scores []RuleScore
		var total float64
		var rt RoundtableResult

		if opts.useRoundtable {
			// 圆桌模式:多专家并发,内部已对每条规则(metric)和每维度(llm)打分
			SetAuditCaller("roundtable_leader")
			rt = RunRoundtable(task.ExpertGroupID, questionRules, q.Prompt, cr.Text, agentProfile)
			if rt.IsMock {
				anyMock = true
			}
			total = rt.FinalScore
			// 从圆桌意见构造 RuleScore 列表(供报告明细展示)
			scores = roundtableToScores(rt)

			// 7.1.3 专家矛盾检测:任意两位专家评分差 >30 分时标记矛盾
			if contradiction := detectExpertContradiction(rt.Opinions); contradiction != "" {
				slog.Info("专家矛盾检测", "turn", i+1, "detail", contradiction)
			}
		} else {
			// 单判官模式:model 场景或规则少时用,更轻量
			SetAuditCaller("judge")
			scores, total = ScoreQuestion(questionRules, q.Prompt, cr.Text)
			for _, s := range scores {
				if s.IsMock {
					anyMock = true
				}
			}
		}

		// 2e. Test-Gen Agent: 针对 Diff 块自动生成单元测试 + 高并发 Mock
			//    (MRD 5.3: "触发内部 Test-Gen Agent,针对 Diff 块自动生成单元测试
			//     和高并发流量 Mock 测试")
			var sb SandboxResult
			var testGenBlocks []DiffBlock
			if opts.useSandbox {
				// 检测语言,传给 Test-Gen Agent
				lang := detectLanguage(cr.Text)
				testCode, blocks := GenerateTests(cr.Text, q.Prompt, q.Expected, lang)
				testGenBlocks = blocks
				if len(blocks) > 0 {
					slog.Info("Test-Gen",
						"turn", i+1, "diff_blocks", len(blocks), "test_bytes", len(testCode))
				}

				// 2f. 沙箱编译执行(agent 场景才走,采集硬指标)
				//     将生成的测试代码注入沙箱,与源码一同编译运行
				sb = RunSandboxWithTests(cr.Text, testCode)
					if sb.Success {
						slog.Info("沙箱编译成功",
							"turn", i+1, "compile_ms", sb.CompileTime,
							"test_pass", sb.TestPassed, "test_total", sb.TestTotal,
							"lint_errors", sb.LintErrors, "test_gen_blocks", len(testGenBlocks))
					// 7.2.3 测试覆盖率统计:有测试运行时计算通过率并记录
					if sb.TestTotal > 0 {
						passRate := float64(sb.TestPassed) / float64(sb.TestTotal) * 100
							slog.Info("测试通过率",
								"turn", i+1, "rate", passRate,
								"pass", sb.TestPassed, "total", sb.TestTotal)
					}
					} else if !sb.IsMock {
						slog.Warn("沙箱编译失败", "turn", i+1, "error", sb.CompileError)
					}
			}

		// 2f. 封装单题结果
		var rtPtr *RoundtableResult
		var sbPtr *SandboxResult
		if opts.useRoundtable {
			rtPtr = &rt
		}
		if opts.useSandbox {
			sbPtr = &sb
		}
		// 结构化代码评审(对被测 Agent 的代码回答做 P0-P3 findings 评审)
		// 不依赖沙箱结果——沙箱可能因语言识别失败而跳过,但代码评审仍应进行。
		// 语言从回答内容自动检测(沙箱有结果用沙箱的,否则让 code_review 自己检测)。
		var crReview *ReviewResult
		if cr.Text != "" && len(cr.Text) > 30 {
			reviewLang := sb.Language
			if reviewLang == "" || reviewLang == "unknown" {
				reviewLang = detectLanguage(cr.Text)
			}
			review := RunCodeReview(cr.Text, reviewLang, q.Title)
			crReview = &review
		}
		qr := makeQuestionResult(q, scores, total, cr.Text, rtPtr, sbPtr)
		qr.CodeReview = crReview
		results = append(results, qr)

		// WebSocket 事件:第 N 题评测完成
		ws.Emit(taskID, "question_done", ws.QuestionDoneData{
			TaskID: taskID, Turn: i + 1,
			Score: qr.Score, IsBadCase: qr.IsBadCase,
		})

		// 7.5.2 断路器 WebSocket 推送:每题评测后实时推送断路器状态(token/轮数/费用)
		ws.Emit(taskID, "breaker_status", ws.BreakerStatusData{
			TaskID:      taskID,
			Turn:        i + 1,
			TotalTokens: breaker.TotalTokens,
			TotalTurns:  breaker.TotalTurns,
			EstCostUSD:  round1(breaker.EstCostUSD),
			Tripped:     breaker.Tripped,
		})

		// 2g. 持久化对话(V2 7.4:落盘前脱敏,确保 Key 不泄密)
		rawMsgs := []map[string]string{
			{"role": "user", "content": q.Prompt},
			{"role": "assistant", "content": cr.Text},
		}
		msgs, err := json.Marshal(rawMsgs)
		if err != nil { msgs = []byte("[]") }
		// 脱敏:扫描消息内容中的 Key,替换为 ***REDACTED***
		msgStr := string(msgs)
		safeMsgs, redactedCount := RedactText(msgStr)
		store.InsertConversation(uuid.New().String(), taskID, i+1, safeMsgs, cr.TokenIn, cr.TokenOut)
			if redactedCount > 0 {
				slog.Info("会话脱敏", "turn", i+1, "redacted", redactedCount)
			}

		// 2h-pre. 反泄漏扫描(V2 9.5):检测 System Prompt 泄露/注入/踩额度/凭据泄露
		lr := ScanLeakage(q.Prompt, cr.Text)
		leakageResults = append(leakageResults, lr)
			if !lr.Passed {
				slog.Warn("反泄漏扫描发现隐患",
					"turn", i+1, "findings", len(lr.Findings), "score", lr.Score)
			}

		// 2h. Bad Case 落库
		if qr.IsBadCase {
			store.InsertBadCase(uuid.New().String(), taskID, q.ID, agent.ID, cr.Text, qr.Score, "auto_low_score")
		}

		// 2i. 混沌工程对抗模式(④.2):开启时,对前 2 道题注入故障测鲁棒性。
		//   限制题数避免成本爆炸(每题 4 种注入 × 调 Agent)。
		//   结果汇总进报告,作为"鲁棒性"指标。
		if task.ChaosEnabled && i < 2 {
			chaosCfg := DefaultChaosConfig()
			chaosRes := RunChaos(*agent, q.Prompt, chaosCfg, taskID)
			chaosResults = append(chaosResults, chaosRes...)
			slog.Info("混沌注入", "turn", i+1, "tests", len(chaosRes))
		}
	}

	// 3. 聚合报告
	report := BuildReport(results, anyMock)
	// 把 Agent 能力画像写进报告(前端展示)
	report.AgentProfile = &agentProfile
	// 把断路器状态写进报告(前端展示费用/token 消耗)
	report.Breaker = breakerStateToReport(breaker, blown)
	// 把混沌结果写进报告(开启时)
	if task.ChaosEnabled && len(chaosResults) > 0 {
		report.Chaos = buildChaosSummary(chaosResults)
	}
	// 把反泄漏扫描结果写进报告(V2 9.5)
	if len(leakageResults) > 0 {
		report.Leakage = buildLeakageSummary(leakageResults)
	}
	reportJSON, _ := json.Marshal(report)

	// 4. 写回 task(status 由 UpdateTaskResult 设为 done;熔断时覆盖为 blown)
	if err := store.UpdateTaskResult(taskID, string(reportJSON)); err != nil {
		slog.Error("写报告失败", "error", err)
		store.UpdateTaskStatus(taskID, "failed")
		return
	}
	if blown {
		store.UpdateTaskStatus(taskID, "blown")
	}

	LogBreakerStatus(breaker, taskID)
	status := "done"
	if blown {
		status = "blown"
	}
	slog.Info("评测完成",
		"status", status, "overall", report.Overall,
		"questions", fmt.Sprintf("%d/%d", len(results), len(questions)),
		"bad_cases", report.BadCases, "duration", time.Since(start), "mock", anyMock)

	// WebSocket 事件:任务评测完成
	ws.Emit(taskID, "task_done", ws.TaskDoneData{
		TaskID: taskID, Overall: report.Overall,
		Dimensions: report.Dimensions, BadCases: report.BadCases, MockMode: anyMock,
	})

	// 5. 自动归因闭环(④.3):有 Bad Case 时,后台异步分析根因。
	//    不阻塞主流程,不影响 status(归因结果写入 attributions 表,前端独立展示)。
	//    这是"评测→暴露Bug→归因→提示词进化"闭环的第一步。
	//
	//    并发安全说明(2026-07-02 TD-1):归因 goroutine 是独立的 goroutine,
	//    其 goroutine ID 与主 worker 不同,因此 currentEvalContext() 在其中返回 nil,
	//    天然不会误用主流程的审计上下文。无需额外的 ClearAuditContext 防御。
	if report.BadCases > 0 {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// 归因在独立 goroutine,无 EvalContext 绑定,显式带 task_id
					NewSlog("engine").With("task_id", taskID).Error("自动归因 panic",
						"error", r, "stack", string(debug.Stack()))
				}
			}()
			n := RunBatchAttribution(taskID)
			NewSlog("engine").With("task_id", taskID).Info("自动归因完成", "bad_cases", n)

			// 7.3.3 归因反馈闭环:检查是否有高置信度的修正建议,提示用户采纳
			suggestHighConfidenceFixes(taskID)
		}()
	}
}

// resolveStrategy 根据任务场景决定执行策略(接入 router.go 的意图路由)。
//   - agent 场景:圆桌 + 沙箱(最完整流程)
//   - model 场景:单判官(轻量,直接评模型输出,不走沙箱)
//   - 其他(prd/competitor/product):圆桌(文本评审,不走沙箱)
func resolveStrategy(task *store.TaskRow) runOptions {
	intent := ClassifyIntent(task.Name+" "+task.Scene, task.Scene)
	NewSlog("engine").With("task_id", task.ID).Info("意图路由",
		"scene", intent.Scene, "confidence", intent.Confidence, "strategy", intent.Strategy)

	switch intent.Strategy {
	case "api_direct":
		// 大模型评测:直接评 API 输出,不走沙箱,用单判官即可(规则少)
		return runOptions{useRoundtable: false, useSandbox: false}
	case "sandbox_roundtable":
		// Agent 评测:圆桌 + 沙箱(最完整)
		return runOptions{useRoundtable: true, useSandbox: true}
	default:
		// text_review / multimodal_review:圆桌评审,不走沙箱
		return runOptions{useRoundtable: true, useSandbox: false}
	}
}

// roundtableToScores 把圆桌各专家意见转成 RuleScore 列表(供报告明细展示)。
// 每个专家意见 → 一条 RuleScore(dimension 来自专家,scoring 标 roundtable)。
func roundtableToScores(rt RoundtableResult) []RuleScore {
	scores := make([]RuleScore, 0, len(rt.Opinions))
	for _, op := range rt.Opinions {
		scores = append(scores, RuleScore{
			RuleID:    "rt-" + op.Role,
			RuleName:  dimensionLabel(op.Dimension) + "专家",
			Dimension: op.Dimension,
			Scoring:   "roundtable",
			Weight:    1.0,
			Score:     op.Score,
			Reason:    op.Reason,
			IsMock:    op.IsMock,
		})
	}
	return scores
}

// breakerStateToReport 把断路器状态转成报告里的可展示结构。
func breakerStateToReport(bs *BreakerState, blown bool) BreakerReport {
	return BreakerReport{
		Tripped:    blown,
		TripReason: bs.TripReason,
		TotalTokens: bs.TotalTokens,
		TotalTurns:  bs.TotalTurns,
		EstCostUSD:  round1(bs.EstCostUSD),
	}
}

// buildLeakageSummary 聚合反泄漏扫描结果。
func buildLeakageSummary(results []LeakageResult) *LeakageSummary {
	total := 0
	failed := 0
	var allFindings []LeakageFinding
	for _, r := range results {
		total++
		if !r.Passed {
			failed++
		}
		allFindings = append(allFindings, r.Findings...)
	}
	avgScore := 100
	if total > 0 {
		sum := 0
		for _, r := range results {
			sum += r.Score
		}
		avgScore = sum / total
	}
	return &LeakageSummary{
		TotalScans:    total,
		FailedScans:   failed,
		AvgScore:      avgScore,
		Findings:      allFindings,
	}
}

// buildChaosSummary 聚合混沌注入结果,算鲁棒性平均分。
func buildChaosSummary(results []ChaosResult) *ChaosSummary {
	if len(results) == 0 {
		return &ChaosSummary{Enabled: true, Results: []ChaosResult{}, AvgScore: 0}
	}
	var sum float64
	for _, r := range results {
		sum += r.Score
	}
	return &ChaosSummary{
		Enabled:  true,
		Results:  results,
		AvgScore: round1(sum / float64(len(results))),
	}
}

// mergeRulesByRAG 把 rule_set 规则与 RAG 检索结果合并去重。
//   - baseRules: 用户在 rule_set 里显式指定的规则(必跑,尊重用户意图)
//   - ragRules: RAG 按题目语义检索到的相关规则(补充,扩大覆盖面)
// 去重:按 rule.ID 去重,RAG 规则若已在 baseRules 则跳过。
func mergeRulesByRAG(baseRules, ragRules []store.RuleRow) []store.RuleRow {
	if len(ragRules) == 0 {
		return baseRules
	}
	seen := make(map[string]bool, len(baseRules))
	for _, r := range baseRules {
		seen[r.ID] = true
	}
	merged := make([]store.RuleRow, len(baseRules))
	copy(merged, baseRules)
	for _, r := range ragRules {
		if !seen[r.ID] {
			merged = append(merged, r)
			seen[r.ID] = true
		}
	}
	return merged
}

// ── 7.1.3 专家矛盾检测 ──

// detectExpertContradiction 检测圆桌专家之间的评分矛盾。
// 当任意两位专家对同一题的评分差 >30 分时,视为矛盾并返回描述。
// 矛盾说明评测维度之间存在显著分歧,需要人工关注。
func detectExpertContradiction(opinions []ExpertOpinion) string {
	if len(opinions) < 2 {
		return ""
	}

	// 按维度分组,同维度内检测矛盾
	dimScores := make(map[string][]struct {
		Role  string
		Score float64
	})
	for _, op := range opinions {
		dimScores[op.Dimension] = append(dimScores[op.Dimension], struct {
			Role  string
			Score float64
		}{op.Role, op.Score})
	}

	for dim, experts := range dimScores {
		for i := 0; i < len(experts); i++ {
			for j := i + 1; j < len(experts); j++ {
				diff := experts[i].Score - experts[j].Score
				if diff < 0 {
					diff = -diff
				}
				if diff > 30 {
					return fmt.Sprintf("矛盾:维度 %q 中 %s(%.0f分) 与 %s(%.0f分) 差距 %.0f 分",
						dim, experts[i].Role, experts[i].Score, experts[j].Role, experts[j].Score, diff)
				}
			}
		}
	}

	// 跨维度检测:取每位专家的最高分和最低分,差距 >30 也标记
	if len(opinions) >= 2 {
		minScore, maxScore := opinions[0].Score, opinions[0].Score
		minRole, maxRole := opinions[0].Role, opinions[0].Role
		for _, op := range opinions[1:] {
			if op.Score < minScore {
				minScore = op.Score
				minRole = op.Role
			}
			if op.Score > maxScore {
				maxScore = op.Score
				maxRole = op.Role
			}
		}
		if maxScore-minScore > 30 {
			return fmt.Sprintf("矛盾:跨维度 %s(%.0f分) 与 %s(%.0f分) 差距 %.0f 分",
				maxRole, maxScore, minRole, minScore, maxScore-minScore)
		}
	}

	return ""
}

// ── 7.3.3 归因反馈闭环 ──

// suggestHighConfidenceFixes 检查归因结果中的高置信度修正建议并记录。
// 置信度 >= 0.8 的修正建议值得人工关注,通过日志提示用户采纳。
func suggestHighConfidenceFixes(taskID string) {
	rows, err := store.DB.Query(
		`SELECT id, root_cause, suggested_fix, confidence FROM attributions
		 WHERE task_id = ? AND confidence >= 0.8 AND applied = 0`, taskID)
	if err != nil {
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, rootCause, suggestedFix string
		var confidence float64
		if err := rows.Scan(&id, &rootCause, &suggestedFix, &confidence); err != nil {
			continue
		}
		count++
		NewSlog("engine").With("task_id", taskID).Info("归因建议",
			"index", count, "confidence", confidence,
			"root_cause", rootCause, "fix", truncateForCompaction(suggestedFix, 200))
	}
	if count > 0 {
		NewSlog("engine").With("task_id", taskID).Info("高置信度归因建议待采纳", "count", count)
	}
}

// loadTaskContext 读任务关联的 Agent + 规则 + 题目。
//
// scene↔规则集↔题 三者联动(MRD 批次④:分类套件):
//   - 规则:任务显式指定 rule_set 优先;否则读 scenes.rule_set_id(分类默认规则集);再否则兜底全量
//   - 题目:任务可经分类默认题集(scenes.default_question_set_id)指定;否则按 task.scene 过滤
func loadTaskContext(taskID string) (task *store.TaskRow, agent *store.AgentRow,
	rules []store.RuleRow, questions []store.QuestionRow, err error) {

	task, err = store.GetTask(taskID)
	if err != nil {
		return
	}

	// Agent(无 agent_id 用一个占位 Agent,走 mock)
	if task.AgentID != "" {
		agent, err = store.GetAgent(task.AgentID)
		if err != nil {
			return
		}
	} else {
		agent = &store.AgentRow{ID: "", Name: "(未指定)", Model: ""}
	}

	// 规则:任务 rule_set 优先;否则用分类默认规则集(scenes.rule_set_id);再否则全量兜底
	if task.RuleSet != "" {
		rules, err = loadRules(task.RuleSet)
		if err != nil {
			return
		}
	} else if task.Scene != "" {
		// 读分类默认规则集
		if sd, _ := store.GetSceneDefaults(task.Scene); sd != nil && sd.RuleSetID != "" {
			NewSlog("engine").With("task_id", taskID).Info("使用分类默认规则集", "scene", task.Scene, "rule_set_id", sd.RuleSetID)
			rules, err = loadRules(`{"rule_set_id":"` + sd.RuleSetID + `"}`)
			if err != nil {
				return
			}
		}
	}
	if len(rules) == 0 {
		// 全量兜底
		rules, err = store.GetAllRules()
		if err != nil {
			return
		}
	}

	// 题目:优先用分类默认题集(scenes.default_question_set_id);否则按 scene 字符串过滤
	if task.Scene != "" {
		if sd, _ := store.GetSceneDefaults(task.Scene); sd != nil && sd.DefaultQuestionSetID != "" {
			if qs, qerr := store.GetQuestionsBySetID(sd.DefaultQuestionSetID); qerr == nil && len(qs) > 0 {
					NewSlog("engine").With("task_id", taskID).Info("使用分类默认题集",
						"scene", task.Scene, "question_set_id", sd.DefaultQuestionSetID, "questions", len(qs))
					questions = qs
				}
		}
	}
	if len(questions) == 0 {
		questions, err = store.GetQuestionsByScene(task.Scene)
		if err != nil {
			return
		}
	}
	return
}

// loadRules 从任务的 rule_set 字段(JSON)解析规则。
// rule_set 格式: {"rule_set_id": "rs-fullstack"} 或直接 rule_ids 数组。
func loadRules(ruleSetJSON string) ([]store.RuleRow, error) {
	if ruleSetJSON == "" {
		// 无规则集 → 返回全部规则兜底(确保有规则可跑)
		return store.GetAllRules()
	}

	// 尝试解析为 {rule_set_id: xxx}
	var rs struct {
		RuleSetID string   `json:"rule_set_id"`
		RuleIDs   []string `json:"rule_ids"`
	}
	if err := json.Unmarshal([]byte(ruleSetJSON), &rs); err == nil {
		// 优先用 rule_set_id 查规则集
		if rs.RuleSetID != "" {
			set, err := store.GetRuleSet(rs.RuleSetID)
			if err == nil {
				ids := store.ParseRuleIDs(set.RuleIDs)
				if len(ids) > 0 {
					return store.GetRulesByIDs(ids)
				}
			}
		}
		// 其次用内联 rule_ids
		if len(rs.RuleIDs) > 0 {
			return store.GetRulesByIDs(rs.RuleIDs)
		}
	}
	// 都失败 → 全部规则兜底
	return store.GetAllRules()
}

// writeEmptyReport 写一个空报告(无题目时)。
func writeEmptyReport(taskID, msg string) {
	report := Report{
		Overall:        0,
		Dimensions:     map[string]float64{},
		PerQuestion:    []QuestionResult{},
		BadCases:       0,
		MockMode:       false,
		TotalQuestions: 0,
	}
	reportJSON, _ := json.Marshal(report)
	store.UpdateTaskResult(taskID, string(reportJSON))
	NewSlog("engine").With("task_id", taskID).Info("空报告", "message", msg)
}
