package engine

// rag.go — 规则库 RAG 向量检索(MRD 5.7 / 4.4)。
//
// 核心问题(MRD 原话):
//   "规则多了之后,主 Agent 不可能把几百条规则全塞进 prompt(既爆 token 又降低相关性)。
//    将规则库做成向量化的知识库,主 Agent 拿到被测对象描述后,先去规则库里语义检索
//    '最相关的评测规则',动态组装成这次评测的判断标准。"
//
// 三个好处:
//   1. 规则库无限扩张不撞 token:每次只检索 top-K
//   2. 评测标准更精准:测 Go 并发代码不会把"文案风格规则"带进来
//   3. 支持跨维度关联检索:顺着关系边拉关联规则
//
// 实现:
//   - 规则录入时同时生成 embedding 存入向量库
//   - 评测时用被测对象描述做 query,检索 top-K 规则
//   - 向量相似度:余弦相似度(cosine similarity)
//   - 存储:SQLite + 自定义向量列(轻量方案,不依赖外部向量库)
//
// 后续升级:
//   - 接入 sqlite-vss(更高效的向量索引)
//   - 接入外部向量库(Milvus/Qdrant)
//   - 支持规则间关系边的知识图谱检索

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"ai-eval-platform/internal/store"
)

// RuleEmbedding 是一条规则的向量表示。
type RuleEmbedding struct {
	RuleID    string    `json:"rule_id"`
	RuleName  string    `json:"rule_name"`
	Dimension string    `json:"dimension"`
	Content   string    `json:"content"`
	Vector    []float64 `json:"vector"`     // embedding 向量
	Tags      []string  `json:"tags"`       // 关联标签
}

// RAGResult 是 RAG 检索的结果。
type RAGResult struct {
	Rule       store.RuleRow `json:"rule"`        // 匹配的规则
	Score      float64       `json:"score"`       // 相似度分数 0-1
	Reason     string        `json:"reason"`      // 匹配原因
	RelatedRules []store.RuleRow `json:"related_rules"` // 关联规则(知识图谱)
}

// RAGIndex 是规则库的向量索引。
type RAGIndex struct {
	embeddings []RuleEmbedding
	dimension  int // 向量维度
}

// 全局 RAG 索引(单例)
var globalRAGIndex *RAGIndex

// BuildRAGIndex 构建规则库的向量索引。
//
// 流程:
//  1. 读取所有规则
//  2. 为每条规则生成 embedding(基于关键词的轻量方案)
//  3. 存入内存索引
//  4. 持久化到数据库(供重启后恢复)
func BuildRAGIndex() (*RAGIndex, error) {
	rules, err := store.GetAllRules()
	if err != nil {
		return nil, fmt.Errorf("failed to load rules: %w", err)
	}

	index := &RAGIndex{
		embeddings: make([]RuleEmbedding, 0, len(rules)),
		dimension:  128, // 固定维度
	}

	for _, rule := range rules {
		embedding := RuleEmbedding{
			RuleID:    rule.ID,
			RuleName:  rule.Name,
			Dimension: rule.Dimension,
			Content:   rule.Content,
			Vector:    generateEmbedding(rule.Name + " " + rule.Content + " " + rule.Tags),
			Tags:      parseTags(rule.Tags),
		}
		index.embeddings = append(index.embeddings, embedding)
	}

	globalRAGIndex = index
	NewSlog("rag").Info("索引构建完成: 条规则, 维度", "embeddings", len(index.embeddings), "dimension", index.dimension)
	return index, nil
}

// GetRAGIndex 获取全局 RAG 索引(不存在则构建)。
func GetRAGIndex() *RAGIndex {
	if globalRAGIndex == nil {
		BuildRAGIndex()
	}
	return globalRAGIndex
}

// Search 检索与 query 最相关的 top-K 规则。
//
// 参数:
//   - query: 查询文本(题目/代码/Agent 回答)
//   - topK: 返回前 K 个结果
//   - minScore: 最低相似度阈值(0-1)
//
// 返回:按相似度降序排列的规则列表。
func (idx *RAGIndex) Search(query string, topK int, minScore float64) []RAGResult {
	queryVec := generateEmbedding(query)

	// 计算所有规则的相似度
	type scored struct {
		embedding RuleEmbedding
		score     float64
	}
	results := make([]scored, 0, len(idx.embeddings))

	for _, emb := range idx.embeddings {
		sim := cosineSimilarity(queryVec, emb.Vector)
		if sim >= minScore {
			results = append(results, scored{embedding: emb, score: sim})
		}
	}

	// 按相似度降序排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// 取 top-K
	if topK > len(results) {
		topK = len(results)
	}

	ragResults := make([]RAGResult, topK)
	for i := 0; i < topK; i++ {
		r := results[i]
		ragResults[i] = RAGResult{
			Rule: store.RuleRow{
				ID:        r.embedding.RuleID,
				Name:      r.embedding.RuleName,
				Dimension: r.embedding.Dimension,
				Content:   r.embedding.Content,
			},
			Score:  r.score,
			Reason: fmt.Sprintf("语义相似度: %.2f", r.score),
		}
	}

	return ragResults
}

// SearchForQuestion 为一道题检索最相关的规则(替代全量规则)。
func (idx *RAGIndex) SearchForQuestion(q store.QuestionRow, topK int) []store.RuleRow {
	query := q.Title + " " + q.Prompt
	results := idx.Search(query, topK, 0.3)

	rules := make([]store.RuleRow, 0, len(results))
	for _, r := range results {
		rules = append(rules, r.Rule)
	}
	return rules
}

// SearchWithRelated 检索规则并顺带拉出关联规则(知识图谱关系边)。
// 顺着 rule_relations 表的关系边拉关联规则,让评测覆盖语义检索之外的关联维度。
func (idx *RAGIndex) SearchWithRelated(query string, topK int) []RAGResult {
	results := idx.Search(query, topK, 0.3)

	// 为每个结果找关联规则(优先走关系边,无关系边时回退同维度)
	for i := range results {
		related := idx.findRelated(results[i].Rule, 3)
		results[i].RelatedRules = related
	}

	return results
}

// SearchWithRelatedForQuestion 为一道题检索规则并拉出关联规则。
// 返回扁平的 RuleRow 列表(去重),供 engine 直接当评测规则用。
func (idx *RAGIndex) SearchWithRelatedForQuestion(q store.QuestionRow, topK int) []store.RuleRow {
	query := q.Title + " " + q.Prompt
	results := idx.SearchWithRelated(query, topK)

	// 把语义命中规则 + 各自的关联规则合并去重
	seen := make(map[string]bool)
	out := make([]store.RuleRow, 0, len(results)*3)
	for _, r := range results {
		if !seen[r.Rule.ID] {
			out = append(out, r.Rule)
			seen[r.Rule.ID] = true
		}
		for _, rel := range r.RelatedRules {
			if !seen[rel.ID] {
				out = append(out, rel)
				seen[rel.ID] = true
			}
		}
	}
	return out
}

// findRelated 找与给定规则相关的规则。
//
// 策略(MRD §7.3 知识图谱):
//  1. 优先:读 rule_relations 表,沿关系边 1-hop 拉关联规则(真实的关系边)
//  2. 回退:关系表无数据时,退回"同维度"启发式(保证空库也能用)
func (idx *RAGIndex) findRelated(rule store.RuleRow, limit int) []store.RuleRow {
	// 1. 优先走关系边
	relatedIDs := store.GetRelatedRuleIDs(rule.ID)
	if len(relatedIDs) > 0 {
		relatedRules, _ := store.GetRulesByIDs(relatedIDs)
		if len(relatedRules) >= limit {
			return relatedRules[:limit]
		}
		// 关系边不够,补一点同维度(凑满 limit)
		if len(relatedRules) < limit {
			for _, emb := range idx.embeddings {
				if emb.RuleID == rule.ID {
					continue
				}
				if emb.Dimension == rule.Dimension {
					dup := false
					for _, rr := range relatedRules {
						if rr.ID == emb.RuleID {
							dup = true
							break
						}
					}
					if !dup {
						relatedRules = append(relatedRules, store.RuleRow{
							ID: emb.RuleID, Name: emb.RuleName, Dimension: emb.Dimension, Content: emb.Content,
						})
						if len(relatedRules) >= limit {
							break
						}
					}
				}
			}
		}
		return relatedRules
	}

	// 2. 无关系边时回退到同维度启发式
	var related []store.RuleRow
	for _, emb := range idx.embeddings {
		if emb.RuleID == rule.ID {
			continue
		}
		if emb.Dimension == rule.Dimension {
			related = append(related, store.RuleRow{
				ID:        emb.RuleID,
				Name:      emb.RuleName,
				Dimension: emb.Dimension,
				Content:   emb.Content,
			})
			if len(related) >= limit {
				break
			}
		}
	}
	return related
}

// generateEmbedding 生成文本的向量表示(轻量方案:基于关键词哈希)。
//
// 生产环境应替换为真正的 embedding 模型(如 text-embedding-ada-002)。
// 这里的轻量方案:
//   1. 分词(中文按字,英文按空格)
//   2. 每个词哈希到固定维度的位置
//   3. 累加 + 归一化
func generateEmbedding(text string) []float64 {
	dim := 128
	vec := make([]float64, dim)

	// 分词
	words := tokenize(text)

	for _, w := range words {
		// 哈希到维度位置
		h := hash(w) % dim
		if h < 0 {
			h = -h
		}
		vec[h] += 1.0

		// 二阶哈希(增加区分度)
		h2 := (hash(w+"salt") % dim)
		if h2 < 0 {
			h2 = -h2
		}
		vec[h2] += 0.5
	}

	// L2 归一化
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vec {
			vec[i] /= norm
		}
	}

	return vec
}

// tokenize 分词(中文按字,英文按空格/标点)。
func tokenize(text string) []string {
	low := strings.ToLower(text)
	// 替换标点为空格
	replacer := strings.NewReplacer(
		",", " ", ".", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ",
		"{", " ", "}", " ", "，", " ", "。", " ",
		"；", " ", "：", " ", "（", " ", "）", " ",
	)
	low = replacer.Replace(low)

	words := strings.Fields(low)

	// 对中文,按字拆分(每个中文字作为一个 token)
	var result []string
	for _, w := range words {
		isChinese := false
		for _, r := range w {
			if r >= 0x4e00 && r <= 0x9fff {
				isChinese = true
				break
			}
		}
		if isChinese {
			// 中文:按字拆分
			for _, r := range w {
				result = append(result, string(r))
			}
		} else {
			result = append(result, w)
		}
	}

	return result
}

// hash 简单的字符串哈希(FNV-1a)。
func hash(s string) int {
	h := 2166136261
	for _, c := range s {
		h ^= int(c)
		h *= 16777619
	}
	return h
}

// cosineSimilarity 计算两个向量的余弦相似度。
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// parseTags 解析 JSON 标签数组。
func parseTags(tagsJSON string) []string {
	if tagsJSON == "" {
		return nil
	}
	var tags []string
	json.Unmarshal([]byte(tagsJSON), &tags)
	return tags
}

// SaveRAGIndex 把 RAG 索引持久化到数据库。
func SaveRAGIndex(index *RAGIndex) {
	for _, emb := range index.embeddings {
		vecJSON, _ := json.Marshal(emb.Vector)
		store.DB.Exec(`INSERT OR REPLACE INTO rag_embeddings (rule_id, dimension, vector, created_at)
			VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, emb.RuleID, emb.Dimension, string(vecJSON))
	}
	NewSlog("rag").Info("索引已持久化: 条规则", "embeddings", len(index.embeddings))
}

// LoadRAGIndex 从数据库恢复 RAG 索引。
func LoadRAGIndex() *RAGIndex {
	rows, err := store.DB.Query("SELECT rule_id, dimension, vector FROM rag_embeddings")
	if err != nil {
		return nil
	}
	defer rows.Close()

	rules, _ := store.GetAllRules()
	ruleMap := map[string]store.RuleRow{}
	for _, r := range rules {
		ruleMap[r.ID] = r
	}

	index := &RAGIndex{
		embeddings: make([]RuleEmbedding, 0),
		dimension:  128,
	}

	for rows.Next() {
		var ruleID, dimension, vecJSON string
		rows.Scan(&ruleID, &dimension, &vecJSON)

		var vec []float64
		json.Unmarshal([]byte(vecJSON), &vec)

		if rule, ok := ruleMap[ruleID]; ok {
			index.embeddings = append(index.embeddings, RuleEmbedding{
				RuleID:    ruleID,
				RuleName:  rule.Name,
				Dimension: rule.Dimension,
				Content:   rule.Content,
				Vector:    vec,
			})
		}
	}

	if len(index.embeddings) > 0 {
		globalRAGIndex = index
		NewSlog("rag").Info("从数据库恢复索引: 条规则", "embeddings", len(index.embeddings))
	}
	return index
}
