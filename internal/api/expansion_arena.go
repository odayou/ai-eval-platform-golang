package api

// expansion_arena.go — 方向2: Agent Arena API handler。

import (
	"log"
	"net/http"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func listArenaLeaderboard(c *gin.Context) {
	scene := c.DefaultQuery("scene", "agent")
	rows, err := store.DB.Query(`SELECT id, agent_name, developer, elo_rating, battles_won, battles_lost,
		avg_correctness, avg_security, avg_performance, total_cost
		FROM arena_submissions WHERE scene=? AND status='ranked' ORDER BY elo_rating DESC`, scene)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	type Entry struct {
		ID, Name, Dev                    string
		Elo, Won, Lost                   int
		Corr, Sec, Perf, Cost            float64
	}
	var items []Entry
	for rows.Next() {
		var e Entry
		rows.Scan(&e.ID, &e.Name, &e.Dev, &e.Elo, &e.Won, &e.Lost, &e.Corr, &e.Sec, &e.Perf, &e.Cost)
		items = append(items, e)
	}
	c.JSON(http.StatusOK, items)
}

func submitToArena(c *gin.Context) {
	var req struct {
		AgentID   string `json:"agent_id" binding:"required"`
		Developer string `json:"developer"`
		Scene     string `json:"scene"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Scene == "" { req.Scene = "agent" }
	agent, _ := store.GetAgent(req.AgentID)
	agentName := ""
	if agent != nil { agentName = agent.Name }
	id := "arena-" + uuid.New().String()[:8]
	store.DB.Exec(`INSERT INTO arena_submissions (id, agent_id, agent_name, developer, scene, elo_rating, status)
		VALUES (?, ?, ?, ?, ?, 1200, 'evaluating')`, id, req.AgentID, agentName, req.Developer, req.Scene)
	go func() { evaluateArenaSubmission(id, req.AgentID, req.Scene) }()
	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "已提交,正在评测..."})
}

func evaluateArenaSubmission(submissionID, agentID, scene string) {
	questions, _ := store.GetQuestionsByScene(scene)
	if len(questions) == 0 {
		questions, _ = store.GetQuestionsByScene("agent")
	}
	agent, _ := store.GetAgent(agentID)
	if agent == nil {
		if _, err := store.DB.Exec(`UPDATE arena_submissions SET status='failed' WHERE id=?`, submissionID); err != nil {
			log.Printf("[arena] 标记失败状态写入失败 submission=%s: %v", submissionID, err)
		}
		return
	}
	// 设置审计上下文(Arena 评测也走白盒审计)。
	// 绑定 per-goroutine EvalContext(并发安全,见 engine/evalctx.go)。
	engine.BindEvalContext("arena:" + submissionID)
	engine.SetAuditContext("arena:"+submissionID, 0)
	defer func() {
		engine.ClearAuditContext()
		engine.UnbindEvalContext()
	}()

	// 取规则集(优先场景默认规则集,否则全量兜底)
	var rules []store.RuleRow
	if sd, _ := store.GetSceneDefaults(scene); sd != nil && sd.RuleSetID != "" {
		if set, _ := store.GetRuleSet(sd.RuleSetID); set != nil {
			ids := store.ParseRuleIDs(set.RuleIDs)
			if len(ids) > 0 {
				rules, _ = store.GetRulesByIDs(ids)
			}
		}
	}
	if len(rules) == 0 {
		rules, _ = store.GetAllRules()
	}

	// 真实评测:逐题调被测 Agent + 圆桌合议打分
	var corrScores, secScores, perfScores []float64
	totalCost := 0.0
	for i, q := range questions {
		if i >= 10 { break } // Arena 限制最多 10 题,控制成本
		engine.SetAuditContext("arena:"+submissionID, i+1)

		// 调被测 Agent
		engine.SetAuditCaller("arena_agent")
		cr := engine.ChatAgent(*agent, q.Prompt)
		if cr.IsMock {
			continue // 无 Key 或失败,跳过该题
		}

		// 圆桌合议打分(4 专家各维度真实评分)
		engine.SetAuditCaller("arena_roundtable")
		rt := engine.RunRoundtable("", rules, q.Prompt, cr.Text, engine.AgentProfile{})

		// 收集各维度分数(圆桌维度名 → 分数)
		engine.SetAuditCaller("arena_score")
		dimMap := map[string]float64{}
		for _, op := range rt.Opinions {
			dimMap[op.Dimension] = op.Score
		}
		// 映射到 correctness/security/performance(缺失维度用总分兜底)
		corr := rt.FinalScore
		if v, ok := dimMap["correctness"]; ok { corr = v }
		sec := rt.FinalScore
		if v, ok := dimMap["security"]; ok { sec = v }
		perf := rt.FinalScore
		if v, ok := dimMap["performance"]; ok { perf = v }

		corrScores = append(corrScores, corr)
		secScores = append(secScores, sec)
		perfScores = append(perfScores, perf)

		// 估算成本(按 token 数 × 单价,简化用 $0.002/1K)
		tokens := cr.TokenIn + cr.TokenOut
		totalCost += float64(tokens) / 1000.0 * 0.002
	}

	// 更新 Arena 记录
	avgCorr := avgFloat(corrScores)
	avgSec := avgFloat(secScores)
	avgPerf := avgFloat(perfScores)
	if _, err := store.DB.Exec(`UPDATE arena_submissions SET status='ranked', avg_correctness=?, avg_security=?,
		avg_performance=?, total_cost=? WHERE id=?`,
		avgCorr, avgSec, avgPerf, totalCost, submissionID); err != nil {
		log.Printf("[arena] 更新排名数据失败 submission=%s: %v", submissionID, err)
	}

	// 简化 ELO:基于平均分映射到 1000-2000 区间
	overall := (avgCorr + avgSec + avgPerf) / 3
	elo := int(1000 + overall*10)
	if elo < 1000 { elo = 1000 }
	if elo > 2000 { elo = 2000 }
	if _, err := store.DB.Exec(`UPDATE arena_submissions SET elo_rating=? WHERE id=?`, elo, submissionID); err != nil {
		log.Printf("[arena] 更新 ELO 失败 submission=%s: %v", submissionID, err)
	}
}

func avgFloat(v []float64) float64 {
	if len(v) == 0 { return 0 }
	s := 0.0
	for _, x := range v { s += x }
	return s / float64(len(v))
}
