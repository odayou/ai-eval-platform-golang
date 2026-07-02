package engine

// github_resolver.go — GitHub Resolver 回调(skill #50)。
// 监听 finished 事件 → 拉 final_response → inline PR 评论。
// 依赖 event_callback.go(#44)。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// GitHubResolverConfig GitHub resolver 配置。
type GitHubResolverConfig struct {
	Token       string // GitHub Token(从环境变量 GITHUB_TOKEN 读)
	APIBase     string // GitHub API 基础 URL
	Triggered   bool   // 一次性保护(已触发过不再重复)
}

// GitHubReviewComment PR inline 评论。
type GitHubReviewComment struct {
	Body  string `json:"body"`            // 评论内容
	Path  string `json:"path,omitempty"`  // 文件路径
	Line  int    `json:"line,omitempty"`  // 行号
	Side  string `json:"side,omitempty"`  // LEFT / RIGHT
}

// NewGitHubResolver 从环境变量创建 resolver。
func NewGitHubResolver() *GitHubResolverConfig {
	return &GitHubResolverConfig{
		Token:   os.Getenv("GITHUB_TOKEN"),
		APIBase: "https://api.github.com",
	}
}

// ShouldRequestSummary 一次性保护:确保只触发一次。
func (g *GitHubResolverConfig) ShouldRequestSummary() bool {
	if g.Triggered {
		return false
	}
	g.Triggered = true
	return g.Token != ""
}

// PostPRComment 在 PR 上发表评论(非 inline,简单 body)。
// repo 格式: "owner/repo", prNumber: PR 编号。
func (g *GitHubResolverConfig) PostPRComment(repo string, prNumber int, body string) error {
	if g.Token == "" {
		return fmt.Errorf("GITHUB_TOKEN 未配置")
	}

	url := fmt.Sprintf("%s/repos/%s/issues/%d/comments", g.APIBase, repo, prNumber)
	payload, _ := json.Marshal(map[string]string{"body": body})

	req, err := http.NewRequest("POST", url, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// FormatAgentResponse 把 agent 的 final response 格式化为 GitHub 评论 markdown。
func FormatAgentResponse(taskName string, score float64, summary string) string {
	return fmt.Sprintf("## 🤖 %s 评测完成\n\n"+
		"**综合分**: %.1f/100\n\n"+
		"**摘要**: %s\n\n"+
		"---\n*由 AI Eval Platform 自动生成*",
		taskName, score, summary)
}
