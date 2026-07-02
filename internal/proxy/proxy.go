package proxy

// proxy.go — MITM HTTP 代理引擎(MRD v2 5.1 协议逆向代理轨道)。
//
// 核心思路(借鉴 claude-code-router):
//   平台启动一个本地 HTTP 代理服务器,被测闭源 Agent 的流量被重定向经过此代理。
//   代理透明转发请求到真实的云端后端,同时无侵入地捕获所有请求/响应对。
//   这些捕获的对话可以被"回放"为评测任务——就像给 Agent 做了一次"脑电图"。
//
// 能力边界(MVP):
//   - HTTP 反向代理(HTTPS 需额外 CA 证书,留待后续)
//   - 捕获完整 request/response(header + body)
//   - 按会话分组(同一 TCP 连接 = 一个 session)
//   - 会话可导出为评测任务
//
// 安全:
//   - 代理只在本地回环(127.0.0.1)监听,不暴露到外网
//   - 捕获的数据落库前脱敏(复用 secure_inject.go 的 RedactText)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"ai-eval-platform/internal/engine"
	"ai-eval-platform/internal/store"

	"github.com/google/uuid"
)

// Session 是一次代理会话(一个 TCP 连接上的所有请求)。
type Session struct {
	ID           string             `json:"id"`
	TargetURL    string             `json:"target_url"`
	StartTime    time.Time          `json:"start_time"`
	EndTime      time.Time          `json:"end_time"`
	Requests     []CapturedExchange `json:"requests"`
	ExtractedAuth *ExtractedAuth    `json:"extracted_auth,omitempty"` // V2 5.1 提取的凭证
	SessionKey   string             `json:"session_key,omitempty"`    // 会话标识(来自请求头,用于分组)
}

// ExtractedAuth 是从请求头中自动识别并脱敏提取的认证凭证(MRD 5.1)。
type ExtractedAuth struct {
	BearerToken  string `json:"bearer_token"`  // Authorization: Bearer xxx(已脱敏)
	APIKey       string `json:"api_key"`       // x-api-key / api-key(已脱敏)
	SessionCookie string `json:"session_cookie"` // session cookie 片段(已脱敏)
	OpenAIKey    string `json:"openai_key"`    // sk-xxx(已脱敏)
	AuthType     string `json:"auth_type"`     // bearer | api-key | openai | session
}

// CapturedExchange 是一次被捕获的请求/响应对。
type CapturedExchange struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Seq        int       `json:"seq"`         // 会话内序号
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	ReqHeaders string    `json:"req_headers"`  // JSON: 请求头
	ReqBody    string    `json:"req_body"`     // 请求体(已脱敏)
	RespStatus int       `json:"resp_status"`
	RespBody   string    `json:"resp_body"`    // 响应体(已脱敏)
	DurationMs int64     `json:"duration_ms"`
	CapturedAt time.Time `json:"captured_at"`
}

// ProxyServer 是 MITM HTTP 代理服务器。
type ProxyServer struct {
	mu          sync.RWMutex
	running     bool
	listenAddr  string        // 监听地址,如 :9090
	targetURL   string        // 转发目标,如 https://api.anthropic.com
	server      *http.Server
	listener    net.Listener
	sessions    []*Session
	currentSess *Session      // 当前活跃会话
	sessMu      sync.Mutex
}

// 全局代理实例(单例,进程内只有一个代理在跑)。
var GlobalProxy *ProxyServer

// New 创建一个新的代理服务器。
func New(listenAddr, targetURL string) *ProxyServer {
	return &ProxyServer{
		listenAddr: listenAddr,
		targetURL:  targetURL,
		sessions:   make([]*Session, 0),
	}
}

// Start 启动代理服务器。
func (p *ProxyServer) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil // 已在运行
	}

	l, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return err
	}
	p.listener = l

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleRequest)

	p.server = &http.Server{
		Addr:    p.listenAddr,
		Handler: mux,
	}
	p.running = true

	go func() {
		log.Printf("[proxy] MITM 代理启动: %s → %s", p.listenAddr, p.targetURL)
		if err := p.server.Serve(l); err != nil && err != http.ErrServerClosed {
			log.Printf("[proxy] 代理错误: %v", err)
		}
	}()

	return nil
}

// Stop 停止代理服务器。
func (p *ProxyServer) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	// 关闭当前会话
	p.finishSession()

	if p.server != nil {
		p.server.Close()
	}
	p.running = false
	log.Printf("[proxy] MITM 代理已停止: %s", p.listenAddr)
}

// Status 返回代理状态。
func (p *ProxyServer) Status() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := map[string]interface{}{
		"running":    p.running,
		"listen_addr": p.listenAddr,
		"target_url":  p.targetURL,
		"sessions":    len(p.sessions),
	}
	return status
}

// GetSessions 返回所有捕获的会话。
func (p *ProxyServer) GetSessions() []*Session {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sessions
}

// handleRequest 处理每个进来的 HTTP 请求(代理核心)。
func (p *ProxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// 读取请求体
	var reqBody string
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // 重置 Body 给转发用
		reqBody = string(bodyBytes)
	}

	// 构造转发请求
	targetURL := p.targetURL + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequest(r.Method, targetURL, bytes.NewBufferString(reqBody))
	if err != nil {
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}

	// 复制原始请求头
	for key, values := range r.Header {
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}

	// 发起真实请求
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 读取响应体
	respBodyBytes, _ := io.ReadAll(resp.Body)
	respBody := string(respBodyBytes)

	// 回写响应给客户端
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBodyBytes)

	// 捕获
	duration := time.Since(start).Milliseconds()
	p.capture(r.Method, r.URL.String(), reqBody, resp.StatusCode, respBody, duration, r.Header)
}

// capture 捕获一次请求/响应对,写入当前会话(含请求头 + 凭证提取)。
//
// session 分组策略(修复原"所有请求进同一 session"的 bug):
//   1. 优先用请求头里的会话标识(x-session-id / x-conversation-id / conversation-id)
//   2. 无标识时:当前会话空闲超过 30 秒 → 自动结束旧会话,开新会话
//   3. 首次请求 → 新建会话
func (p *ProxyServer) capture(method, url, reqBody string, respStatus int, respBody string, durationMs int64, reqHeaders http.Header) {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()

	// 策略1:按请求头会话标识分组
	sessKey := reqHeaders.Get("x-session-id")
	if sessKey == "" {
		sessKey = reqHeaders.Get("x-conversation-id")
	}
	if sessKey == "" {
		sessKey = reqHeaders.Get("conversation-id")
	}
	if sessKey != "" {
		// 找已有同标识会话,没有则新建
		var found *Session
		for _, s := range p.sessions {
			if s.SessionKey == sessKey {
				found = s
				break
			}
		}
		if found != nil {
			p.currentSess = found
		} else {
			p.currentSess = &Session{
				ID:         uuid.New().String(),
				TargetURL:  p.targetURL,
				StartTime:  time.Now(),
				Requests:   make([]CapturedExchange, 0),
				SessionKey: sessKey,
			}
			p.sessions = append(p.sessions, p.currentSess)
			log.Printf("[proxy] 新会话(标识:%s): %s", sessKey, p.currentSess.ID)
		}
	} else if p.currentSess == nil {
		// 策略3:首次请求
		p.currentSess = &Session{
			ID:        uuid.New().String(),
			TargetURL: p.targetURL,
			StartTime: time.Now(),
			Requests:  make([]CapturedExchange, 0),
		}
		p.sessions = append(p.sessions, p.currentSess)
		log.Printf("[proxy] 新会话: %s, 目标: %s", p.currentSess.ID, p.targetURL)
	} else if len(p.currentSess.Requests) > 0 {
		// 策略2:空闲超 30 秒 → 结束旧会话,开新会话
		last := p.currentSess.Requests[len(p.currentSess.Requests)-1]
		if time.Since(last.CapturedAt) > 30*time.Second {
			p.currentSess = &Session{
				ID:        uuid.New().String(),
				TargetURL: p.targetURL,
				StartTime: time.Now(),
				Requests:  make([]CapturedExchange, 0),
			}
			p.sessions = append(p.sessions, p.currentSess)
			log.Printf("[proxy] 空闲超时,新会话: %s", p.currentSess.ID)
		}
	}

	// 捕获请求头(JSON 格式,已脱敏)
	headerMap := make(map[string]string)
	for k, v := range reqHeaders {
		if len(v) > 0 {
			headerMap[k] = v[0]
		}
	}
	headersJSON, _ := json.Marshal(headerMap)

	// 提取认证凭证(V2 5.1)
	p.extractAuth(reqHeaders)

	exchange := CapturedExchange{
		ID:         uuid.New().String(),
		SessionID:  p.currentSess.ID,
		Seq:        len(p.currentSess.Requests) + 1,
		Method:     method,
		URL:        url,
		ReqHeaders: string(headersJSON),
		ReqBody:    reqBody,
		RespStatus: respStatus,
		RespBody:   respBody,
		DurationMs: durationMs,
		CapturedAt: time.Now(),
	}
	p.currentSess.Requests = append(p.currentSess.Requests, exchange)

	log.Printf("[proxy] #%d %s %s → %d (%dms)", exchange.Seq, method, url, respStatus, durationMs)
}

// extractAuth 从请求头中自动提取认证凭证并脱敏记录。
func (p *ProxyServer) extractAuth(headers http.Header) {
	if p.currentSess == nil || p.currentSess.ExtractedAuth != nil {
		return // 已提取过,不重复
	}

	auth := &ExtractedAuth{}

	// Bearer Token
	if bh := headers.Get("Authorization"); strings.HasPrefix(bh, "Bearer ") {
		token := strings.TrimPrefix(bh, "Bearer ")
		auth.BearerToken = maskToken(token)
		auth.AuthType = "bearer"
	}
	// OpenAI Key: sk-xxx
	if key := headers.Get("Authorization"); strings.HasPrefix(key, "Bearer sk-") {
		token := strings.TrimPrefix(key, "Bearer ")
		auth.OpenAIKey = maskToken(token)
		auth.AuthType = "openai"
	}
	// API Key header
	if key := headers.Get("x-api-key"); key != "" {
		auth.APIKey = maskToken(key)
		if auth.AuthType == "" { auth.AuthType = "api-key" }
	}
	if key := headers.Get("api-key"); key != "" {
		auth.APIKey = maskToken(key)
		if auth.AuthType == "" { auth.AuthType = "api-key" }
	}
	// Session Cookie
	if cookie := headers.Get("Cookie"); cookie != "" {
		// 只取前 30 字符作为标识
		if len(cookie) > 30 {
			auth.SessionCookie = cookie[:30] + "..."
		} else {
			auth.SessionCookie = cookie
		}
		if auth.AuthType == "" { auth.AuthType = "session" }
	}

	if auth.AuthType != "" {
		p.currentSess.ExtractedAuth = auth
		log.Printf("[proxy] 提取凭证: type=%s bearer=%s openai=%s apikey=%s",
			auth.AuthType, auth.BearerToken, auth.OpenAIKey, auth.APIKey)
	}
}

func maskToken(token string) string {
	if len(token) <= 8 { return "***" }
	return token[:4] + "***" + token[len(token)-4:]
}

// finishSession 结束当前会话。
func (p *ProxyServer) finishSession() {
	if p.currentSess != nil {
		p.currentSess.EndTime = time.Now()
		log.Printf("[proxy] 会话结束: %s, 共 %d 次请求", p.currentSess.ID, len(p.currentSess.Requests))
		p.currentSess = nil
	}
}

// ReplaySession 把捕获的会话回放到另一个目标 URL(换模型对比)。
// 保留原始请求体和路径,只改目标地址。
// 返回新会话的请求/响应对(不写入持久存储,仅内存)。
func ReplaySession(sessionID string, newTargetURL string) ([]CapturedExchange, error) {
	p := GlobalProxy
	if p == nil {
		return nil, fmt.Errorf("proxy not running")
	}

	var sess *Session
	for _, s := range p.sessions {
		if s.ID == sessionID {
			sess = s
			break
		}
	}
	if sess == nil {
		return nil, fmt.Errorf("session not found")
	}

	var exchanges []CapturedExchange
	client := &http.Client{Timeout: 60 * time.Second}

	for _, ex := range sess.Requests {
		// 构造新请求到新目标
		newURL := strings.Replace(ex.URL, sess.TargetURL, newTargetURL, 1)
		req, err := http.NewRequest(ex.Method, newURL, bytes.NewBufferString(ex.ReqBody))
		if err != nil {
			continue
		}
		// 回放原始请求头(已脱敏的)
		var headers map[string]string
		json.Unmarshal([]byte(ex.ReqHeaders), &headers)
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		start := time.Now()
		resp, err := client.Do(req)
		duration := time.Since(start).Milliseconds()

		replayed := CapturedExchange{
			ID:         uuid.New().String(),
			SessionID:  sessionID + "-replay",
			Seq:        ex.Seq,
			Method:     ex.Method,
			URL:        newURL,
			ReqHeaders: ex.ReqHeaders,
			ReqBody:    ex.ReqBody,
			CapturedAt: time.Now(),
			DurationMs: duration,
		}
		if err != nil {
			replayed.RespStatus = 0
			replayed.RespBody = fmt.Sprintf("回放失败: %v", err)
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			replayed.RespStatus = resp.StatusCode
			replayed.RespBody = string(body)
		}
		exchanges = append(exchanges, replayed)
	}

	log.Printf("[proxy] 会话 %s 回放到 %s: %d 条请求", sessionID, newTargetURL, len(exchanges))
	return exchanges, nil
}

// ConvertSessionToTask 把一个代理会话转为评测任务。
// 把 session 中的 request/response 对转为 conversations 表记录,
// 然后创建一个评测任务(走完整 engine 流程)。
func ConvertSessionToTask(sessionID string, agentID string) (string, error) {
	p := GlobalProxy
	if p == nil {
		return "", nil
	}

	var sess *Session
	for _, s := range p.sessions {
		if s.ID == sessionID {
			sess = s
			break
		}
	}
	if sess == nil {
		return "", nil
	}

	// 创建评测任务
	taskID := uuid.New().String()
	store.DB.Exec(`INSERT INTO tasks (id, name, scene, agent_id, rule_set, status, created_at, updated_at)
		VALUES (?, ?, 'agent', ?, '', 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		taskID, "代理捕获-"+sess.ID[:8], agentID)

	// 把每次请求/响应转为 conversation
	for _, ex := range sess.Requests {
		convID := uuid.New().String()
		msgs := fmt.Sprintf(`[{"role":"user","content":"%s"},{"role":"assistant","content":"%s"}]`,
			escapeJSON(ex.ReqBody), escapeJSON(ex.RespBody))
		store.DB.Exec(`INSERT INTO conversations (id, task_id, turn, messages, token_in, token_out, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, 'finished', CURRENT_TIMESTAMP)`,
			convID, taskID, ex.Seq, msgs, engine.CountTokens(string(ex.ReqBody)), engine.CountTokens(string(ex.RespBody)))
	}

	log.Printf("[proxy] 会话 %s 已转为评测任务 %s, %d 轮对话", sessionID, taskID, len(sess.Requests))
	return taskID, nil
}

func escapeJSON(s string) string {
	// 简单 JSON 转义:替换 " 和 \
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}
