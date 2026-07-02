package engine

// websearch.go — 联网搜索能力(Brave Search / Tavily)。
//
// 被测 Agent 配置了 search_provider 后,调 LLM 前先搜索,把结果摘要拼入 prompt,
// 类似 RAG 但走实时联网(而非本地向量库)。
//
// 两个 provider:
//   - Brave Search API: GET https://api.search.brave.com/res/v1/web/search
//     Header: X-Subscription-Token: {apiKey}, Accept-Encoding: gzip
//     返回 web.results[].title/description/url
//   - Tavily API: POST https://api.tavily.com/search
//     Body: {api_key, query, max_results}
//     返回 results[].title/content/url

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SearchResult 一条搜索结果。
type SearchResult struct {
	Title   string `json:"title"`
	Snippet string `json:"snippet"` // 摘要(Brave:description / Tavily:content)
	URL     string `json:"url"`
}

// WebSearchProvider 联网搜索接口。
type WebSearchProvider interface {
	Search(query string, count int) ([]SearchResult, error)
}

// NewWebSearch 按 provider 名构造搜索器。provider=none 或 key 空 → 返回 nil(不搜索)。
func NewWebSearch(provider, apiKey string) WebSearchProvider {
	if provider == "" || provider == "none" || apiKey == "" {
		return nil
	}
	switch strings.ToLower(provider) {
	case "brave":
		return &BraveSearch{APIKey: apiKey}
	case "tavily":
		return &TavilySearch{APIKey: apiKey}
	}
	return nil
}

// ── Brave Search ──

// BraveSearch Brave Search API 实现。
type BraveSearch struct {
	APIKey string
}

func (b *BraveSearch) Search(query string, count int) ([]SearchResult, error) {
	if count <= 0 {
		count = 5
	}
	endpoint := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), count)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Subscription-Token", b.APIKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("brave search HTTP %d: %s", resp.StatusCode, string(body[:200]))
	}

	var data struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				URL         string `json:"url"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("brave search 解析失败: %w", err)
	}
	results := make([]SearchResult, 0, len(data.Web.Results))
	for _, r := range data.Web.Results {
		results = append(results, SearchResult{Title: r.Title, Snippet: r.Description, URL: r.URL})
	}
	return results, nil
}

// ── Tavily Search ──

// TavilySearch Tavily API 实现。
type TavilySearch struct {
	APIKey string
}

func (t *TavilySearch) Search(query string, count int) ([]SearchResult, error) {
	if count <= 0 {
		count = 5
	}
	body, _ := json.Marshal(map[string]any{
		"api_key":     t.APIKey,
		"query":       query,
		"max_results": count,
	})
	resp, err := http.Post("https://api.tavily.com/search", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("tavily search 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		rbody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tavily search HTTP %d: %s", resp.StatusCode, string(rbody[:200]))
	}

	var data struct {
		Results []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			URL     string `json:"url"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("tavily search 解析失败: %w", err)
	}
	results := make([]SearchResult, 0, len(data.Results))
	for _, r := range data.Results {
		results = append(results, SearchResult{Title: r.Title, Snippet: r.Content, URL: r.URL})
	}
	return results, nil
}

// SearchAndInject 搜索并把结果摘要拼成上下文文本,供拼入 prompt。
//
// 格式:
//   【联网搜索结果】
//   1. {title}
//      {snippet}
//      来源: {url}
//   ...
//
// provider=none 或搜索失败 → 返回空字符串(不注入,降级为无搜索)。
func SearchAndInject(provider, apiKey, query string) string {
	ws := NewWebSearch(provider, apiKey)
	if ws == nil {
		return "" // 不需要搜索
	}
	results, err := ws.Search(query, 5)
	if err != nil || len(results) == 0 {
		return "" // 搜索失败静默降级
	}
	var sb strings.Builder
	sb.WriteString("【联网搜索结果】\n")
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		if r.Snippet != "" {
			// 摘要截断到 300 字符
			snippet := r.Snippet
			if len([]rune(snippet)) > 300 {
				snippet = string([]rune(snippet)[:300]) + "..."
			}
			sb.WriteString(fmt.Sprintf("   %s\n", snippet))
		}
		if r.URL != "" {
			sb.WriteString(fmt.Sprintf("   来源: %s\n", r.URL))
		}
	}
	return sb.String()
}

// TestWebSearch 测试搜索 API Key 连通性(供前端"测试"按钮调用)。
func TestWebSearch(provider, apiKey string) (bool, string) {
	ws := NewWebSearch(provider, apiKey)
	if ws == nil {
		return false, "provider 或 apiKey 为空"
	}
	results, err := ws.Search("test", 1)
	if err != nil {
		return false, err.Error()
	}
	return true, fmt.Sprintf("连通成功,返回 %d 条结果", len(results))
}
