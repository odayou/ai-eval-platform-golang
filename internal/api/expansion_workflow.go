package api

// expansion_workflow.go — 方向6: Multi-Agent Orchestration API handler。

import (
	"encoding/json"
	"net/http"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func listWorkflows(c *gin.Context) {
	rows, _ := store.DB.Query(`SELECT id, name, COALESCE(description,''), status, created_at FROM workflows ORDER BY created_at DESC`)
	type WF struct {
		ID, Name, Desc, Status, CreatedAt string
	}
	var items []WF
	for rows.Next() {
		var w WF
		rows.Scan(&w.ID, &w.Name, &w.Desc, &w.Status, &w.CreatedAt)
		items = append(items, w)
	}
	rows.Close()
	c.JSON(http.StatusOK, items)
}

func createWorkflow(c *gin.Context) {
	var req struct {
		Name        string          `json:"name" binding:"required"`
		Description string          `json:"description"`
		DAG         json.RawMessage `json:"dag" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := "wf-" + uuid.New().String()[:8]
	store.DB.Exec(`INSERT INTO workflows (id, name, description, dag, status) VALUES (?, ?, ?, ?, 'draft')`,
		id, req.Name, req.Description, string(req.DAG))
	c.JSON(http.StatusCreated, gin.H{"id": id, "name": req.Name})
}

func runWorkflow(c *gin.Context) {
	id := c.Param("id")
	var dagJSON string
	store.DB.QueryRow(`SELECT dag FROM workflows WHERE id=?`, id).Scan(&dagJSON)
	var dag struct {
		Nodes []workflowNode `json:"nodes"`
	}
	json.Unmarshal([]byte(dagJSON), &dag)

	runID := "wfrun-" + uuid.New().String()[:8]
	store.DB.Exec(`INSERT INTO workflow_runs (id, workflow_id, status) VALUES (?, ?, 'running')`, runID, id)

	go func() {
		results := make(map[string]string)
		// 拓扑排序(Kahn 算法):确保依赖节点先执行,避免节点顺序与依赖关系不一致时下游拿不到上游结果
		execOrder := topoSort(dag.Nodes)
		for _, node := range execOrder {
			agent, _ := store.GetAgent(node.AgentID)
			if agent == nil {
				results[node.ID] = "agent not found"
				continue
			}
			prompt := node.Prompt
			for _, dep := range node.Depends {
				if r, ok := results[dep]; ok {
					prompt += "\n\n上游结果(" + dep + "):\n" + r
				}
			}
			cr := engine.ChatAgent(*agent, prompt)
			results[node.ID] = cr.Text
		}
		outputJSON, _ := json.Marshal(results)
		store.DB.Exec(`UPDATE workflow_runs SET status='completed', output_data=?, completed_at=CURRENT_TIMESTAMP WHERE id=?`,
			string(outputJSON), runID)
	}()

	c.JSON(http.StatusOK, gin.H{"run_id": runID, "nodes": len(dag.Nodes), "message": "工作流已启动"})
}

func getWorkflowStatus(c *gin.Context) {
	runID := c.Param("id")
	var run struct {
		ID, Status, Output, StartedAt, CompletedAt string
	}
	store.DB.QueryRow(`SELECT id, status, COALESCE(output_data,''), started_at, COALESCE(completed_at,'')
		FROM workflow_runs WHERE id=?`, runID).Scan(&run.ID, &run.Status, &run.Output, &run.StartedAt, &run.CompletedAt)
	c.JSON(http.StatusOK, run)
}

// workflowNode 工作流 DAG 节点。
type workflowNode struct {
	ID      string   `json:"id"`
	AgentID string   `json:"agent_id"`
	Prompt  string   `json:"prompt"`
	Depends []string `json:"depends_on"`
}

// topoSort 对工作流节点做拓扑排序(Kahn 算法)。
// 确保依赖节点先执行,避免节点数组顺序与依赖关系不一致时下游拿不到上游结果。
// 检测到环时返回原始顺序(降级,不阻塞执行)。
func topoSort(nodes []workflowNode) []workflowNode {
	if len(nodes) <= 1 {
		return nodes
	}

	// 建立节点 ID → 节点索引映射
	idxByID := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idxByID[n.ID] = i
	}

	// 计算入度 + 邻接表(依赖 → 依赖它的节点)
	inDegree := make(map[string]int, len(nodes))
	adj := make(map[string][]string)
	for _, n := range nodes {
		inDegree[n.ID] = len(n.Depends)
		for _, dep := range n.Depends {
			// 只认存在的依赖(忽略指向不存在节点的边)
			if _, ok := idxByID[dep]; ok {
				adj[dep] = append(adj[dep], n.ID)
			} else {
				// 依赖的节点不存在,不算入度
				inDegree[n.ID]--
			}
		}
	}

	// 入度为 0 的节点入队(Kahn)
	queue := []string{}
	for id, d := range inDegree {
		if d <= 0 {
			queue = append(queue, id)
		}
	}

	ordered := []workflowNode{}
	queued := make(map[string]bool)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if queued[id] {
			continue
		}
		queued[id] = true
		ordered = append(ordered, nodes[idxByID[id]])
		for _, next := range adj[id] {
			inDegree[next]--
			if inDegree[next] <= 0 && !queued[next] {
				queue = append(queue, next)
			}
		}
	}

	// 处理环:未被排入的节点追加到末尾(降级)
	if len(ordered) < len(nodes) {
		for _, n := range nodes {
			if !queued[n.ID] {
				ordered = append(ordered, n)
			}
		}
	}
	return ordered
}
