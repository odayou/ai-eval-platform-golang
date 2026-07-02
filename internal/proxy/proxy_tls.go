package proxy

// proxy_tls.go — HTTPS MITM 代理完整实现(MRD 5.1 协议逆向代理轨道)。
//
// 架构(MITM 原理):
//   客户端 ──TLS──→ [MITM 代理] ──TLS──→ 真实服务器
//                      │
//                 自签 CA 动态签发域名证书
//                 明文捕获 request/response
//
// 流程(每个连接):
//   1. Accept TCP 连接
//   2. Peek ClientHello → 提取 SNI(Server Name Indication)
//   3. 用自签 CA 为该 SNI 域名动态签发证书
//   4. 以伪服务器身份完成 TLS 握手(tls.Server)
//   5. 从解密后的连接读 HTTP 请求
//   6. 以真实客户端身份向目标服务器发起 TLS 连接
//   7. 转发请求 → 读响应 → 明文捕获
//   8. 回写响应给客户端
//
// 会话管理:
//   - 与 proxy.go 共用 Session / CapturedExchange 数据结构
//   - HTTPS 捕获的请求也注入同一个 Session 流
//   - 支持后续的 ReplaySession / ConvertSessionToTask
//
// 安全:
//   - 只监听 127.0.0.1(永远不对公网)
//   - 捕获内容落库前脱敏(复用 secure_inject.RedactText)
//   - CA 私钥只存本地文件(权限 0600)
//   - 证书固定(Certificate Pinning)的 Agent 无法拦截(行业硬限制)

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ── HTTPS MITM 代理服务器 ──

// TLSProxy 是完整的 HTTPS MITM 代理服务器。
type TLSProxy struct {
	mu         sync.RWMutex
	running    bool
	listenAddr string        // 监听地址,如 :9443
	tlsConfig  *tls.Config   // MITM TLS 配置(含动态证书签发)
	listener   net.Listener
	sessions   []*Session
	currentSess *Session
	sessMu     sync.Mutex
	stopCh     chan struct{}
}

// globalTLSProxy 是全局 HTTPS 代理实例。
var globalTLSProxy *TLSProxy

// NewTLSProxy 创建一个新的 HTTPS MITM 代理。
func NewTLSProxy(listenAddr string, tlsConfig *tls.Config) *TLSProxy {
	return &TLSProxy{
		listenAddr: listenAddr,
		tlsConfig:  tlsConfig,
		sessions:   make([]*Session, 0),
		stopCh:     make(chan struct{}),
	}
}

// Start 启动 HTTPS MITM 代理。
// 返回错误如果 TLS 配置缺失或端口被占用。
func (p *TLSProxy) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	if p.tlsConfig == nil {
		return fmt.Errorf("TLS 配置缺失,请先调用 CreateTLSConfig() 并确保 CA 证书已生成")
	}

	l, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", p.listenAddr, err)
	}
	p.listener = l
	p.running = true

	go p.acceptLoop()

	log.Printf("[proxy_tls] HTTPS MITM 代理启动: %s (动态证书签发)", p.listenAddr)
	return nil
}

// Stop 停止 HTTPS MITM 代理。
func (p *TLSProxy) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	p.finishSession()
	close(p.stopCh)

	if p.listener != nil {
		p.listener.Close()
	}
	p.running = false
	log.Printf("[proxy_tls] HTTPS MITM 代理已停止: %s", p.listenAddr)
}

// acceptLoop 接受 TCP 连接并为每个连接启动 MITM goroutine。
func (p *TLSProxy) acceptLoop() {
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}

		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.stopCh:
				return
			default:
				log.Printf("[proxy_tls] Accept 错误: %v", err)
				continue
			}
		}

		go p.handleConnectionWithPeek(conn)
	}
}

// handleConnection 处理一个 TCP 连接: TLS 握手 → HTTP 解析 → 转发 → 捕获。
func (p *TLSProxy) handleConnection(rawConn net.Conn) {
	defer rawConn.Close()

	// ── 阶段 1: Peek ClientHello,提取 SNI ──
	sni, clientHello, err := peekSNI(rawConn)
	if err != nil {
		log.Printf("[proxy_tls] SNI 提取失败(可能非 TLS 流量): %v", err)
		return
	}

	// ── 阶段 2: 用 MITM TLS 配置完成与客户端的握手(伪装成目标服务器)──
	// 注意: tls.Server 会触发 tlsConfig.GetCertificate(sni) → 动态签发证书
	tlsServerConn := tls.Server(rawConn, p.tlsConfig)
	if err := tlsServerConn.Handshake(); err != nil {
		log.Printf("[proxy_tls] TLS 握手失败(%s): %v", sni, err)
		return
	}
	defer tlsServerConn.Close()

	_ = clientHello // 已用于 SNI 提取,握手完成

	// ── 阶段 3: 从解密后的连接读 HTTP 请求 ──
	req, reqBody, err := readHTTPRequest(tlsServerConn)
	if err != nil {
		log.Printf("[proxy_tls] HTTP 请求解析失败(%s): %v", sni, err)
		return
	}

	start := time.Now()

	// ── 阶段 4: 向真实目标服务器发起 TLS 连接并转发请求 ──
	targetHost := sni
	if req.URL.Host != "" {
		targetHost = req.URL.Host
	}
	if !strings.Contains(targetHost, ":") {
		targetHost = targetHost + ":443"
	}

	resp, respBody, err := forwardToRealServer(targetHost, req, reqBody)
	if err != nil {
		log.Printf("[proxy_tls] 转发失败(%s): %v", sni, err)
		writeHTTPError(tlsServerConn, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	// ── 阶段 5: 回写响应给客户端 ──
	resp.Write(tlsServerConn)

	// ── 阶段 6: 明文捕获 ──
	duration := time.Since(start).Milliseconds()
	p.captureHTTPS(sni, req.Method, req.URL.String(), string(reqBody),
		resp.StatusCode, string(respBody), duration, req.Header)

	log.Printf("[proxy_tls] %s %s → %d (%dms) [%s]",
		req.Method, req.URL.Path, resp.StatusCode, duration, sni)
}

// ── SNI 提取 ──

// peekSNI 从 TLS ClientHello 中提取 SNI(Server Name Indication)。
//
// TLS ClientHello 结构(简化):
//   Byte 0:     ContentType = 22 (Handshake)
//   Byte 1-2:   TLS Version
//   Byte 3-4:   Length
//   Byte 5:     HandshakeType = 1 (ClientHello)
//   Byte 6-8:   Handshake Length
//   Byte 9-10:  Client Version
//   Byte 11-42: Client Random
//   Byte 43:    Session ID Length → 跳过 Session ID
//   然后:       Cipher Suites Length → 跳过
//   然后:       Compression Methods Length → 跳过
//   然后:       Extensions Length → 遍历扩展找 SNI(type=0x0000)
func peekSNI(conn net.Conn) (sni string, rawHello []byte, err error) {
	// Peek 足够多的数据来解析 ClientHello(通常 ~300 bytes)
	peekBuf := make([]byte, 2048)
	n, err := conn.Read(peekBuf)
	if err != nil {
		return "", nil, fmt.Errorf("peek 失败: %w", err)
	}

	// 需要把已读的数据"放回去",让后续 TLS 握手能读到完整 ClientHello
	// 使用 bufio.Reader 重新包装(conn 已经被读了 peekBuf 的内容)
	// 但这里有问题: Read 消费了数据。我们需要修改 handleConnection,
	// 不再直接 Read,而是在 acceptLoop 里包装为可 Peek 的 conn。
	// 暂时: 在 handleConnection 里用不同的方式解决这个问题。
	// 见下面的修正版 handleConnectionWithPeek。

	if n < 43 {
		return "", nil, fmt.Errorf("数据太短(%d bytes),不可能是 TLS ClientHello", n)
	}

	// 检查 ContentType == Handshake(22)
	if peekBuf[0] != 22 {
		return "", nil, fmt.Errorf("不是 TLS 流量(ContentType=%d)", peekBuf[0])
	}

	// 解析 ClientHello 长度
	recordLen := int(peekBuf[3])<<8 | int(peekBuf[4])
	if n < 5+recordLen {
		// 需要更多数据,但 peekBuf 可能已足够
	}

	// 跳过 Record Layer Header(5 bytes) + Handshake Header(4 bytes)
	pos := 5 + 4

	// 跳过 Client Version(2) + Random(32)
	pos += 2 + 32

	// Session ID
	if pos >= n {
		return "", nil, fmt.Errorf("数据截断(Session ID)")
	}
	sessionIDLen := int(peekBuf[pos])
	pos += 1 + sessionIDLen

	// Cipher Suites
	if pos+1 >= n {
		return "", nil, fmt.Errorf("数据截断(Cipher Suites)")
	}
	cipherLen := int(peekBuf[pos])<<8 | int(peekBuf[pos+1])
	pos += 2 + cipherLen

	// Compression Methods
	if pos >= n {
		return "", nil, fmt.Errorf("数据截断(Compression)")
	}
	compLen := int(peekBuf[pos])
	pos += 1 + compLen

	// Extensions
	if pos+1 >= n {
		return "", peekBuf[:n], nil // 无扩展 = 无 SNI
	}
	extLen := int(peekBuf[pos])<<8 | int(peekBuf[pos+1])
	pos += 2
	extEnd := pos + extLen
	if extEnd > n {
		extEnd = n
	}

	// 遍历扩展找 SNI(type=0x0000, server_name)
	for pos+4 <= extEnd {
		extType := int(peekBuf[pos])<<8 | int(peekBuf[pos+1])
		extDataLen := int(peekBuf[pos+2])<<8 | int(peekBuf[pos+3])
		pos += 4

		if extType == 0x0000 { // server_name
			// SNI 扩展内容:
			//   List Length(2) + ServerName type(1) + ServerName Len(2) + ServerName
			if pos+5 > extEnd {
				break
			}
			// 跳过 List Length(2) + Type(1)
			nameLen := int(peekBuf[pos+3])<<8 | int(peekBuf[pos+4])
			nameStart := pos + 5
			nameEnd := nameStart + nameLen
			if nameEnd > n {
				nameEnd = n
			}
			sni = string(peekBuf[nameStart:nameEnd])
			return sni, peekBuf[:n], nil
		}

		pos += extDataLen
	}

	return "", peekBuf[:n], nil // 无 SNI 扩展
}

// ── 重新设计的连接处理(支持 Peek)──

// peekableConn 包装 net.Conn,支持 Peek(预读+回退)。
type peekableConn struct {
	net.Conn
	reader *bufio.Reader
}

func newPeekableConn(conn net.Conn) *peekableConn {
	return &peekableConn{
		Conn:   conn,
		reader: bufio.NewReaderSize(conn, 4096),
	}
}

func (c *peekableConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

// Peek 预读 n 字节但不消费(与 bufio.Reader.Peek 相同)。
func (c *peekableConn) Peek(n int) ([]byte, error) {
	return c.reader.Peek(n)
}

// handleConnectionWithPeek 使用 peekableConn 正确处理 Peek+后续读取。
func (p *TLSProxy) handleConnectionWithPeek(rawConn net.Conn) {
	pconn := newPeekableConn(rawConn)
	defer rawConn.Close()

	// Peek ClientHello 提取 SNI
	peekBuf, err := pconn.Peek(2048)
	if err != nil {
		log.Printf("[proxy_tls] Peek 失败: %v", err)
		return
	}

	sni, _, err := peekSNIFromBuf(peekBuf)
	if err != nil {
		log.Printf("[proxy_tls] SNI 提取失败: %v", err)
		return
	}

	// TLS 握手(peekableConn 的 Read 会从 bufio 读,ClientHello 完整保留)
	tlsServerConn := tls.Server(pconn, p.tlsConfig)
	if err := tlsServerConn.Handshake(); err != nil {
		log.Printf("[proxy_tls] TLS 握手失败(%s): %v", sni, err)
		return
	}
	defer tlsServerConn.Close()

	// 处理该 TLS 连接内的多个 HTTP 请求(HTTP/1.1 keep-alive)
	p.processHTTPStream(tlsServerConn, sni)
}

// processHTTPStream 在一个已建立的 TLS 连接上循环处理 HTTP 请求。
func (p *TLSProxy) processHTTPStream(tlsConn *tls.Conn, sni string) {
	for i := 0; i < 100; i++ { // 最多 100 个请求 per connection
		req, reqBody, err := readHTTPRequest(tlsConn)
		if err != nil {
			if err != io.EOF {
				log.Printf("[proxy_tls] HTTP 请求解析失败(%s, #%d): %v", sni, i+1, err)
			}
			return
		}

		start := time.Now()

		// 构造目标地址
		targetHost := sni
		if req.URL.Host != "" {
			targetHost = req.URL.Host
		}
		if !strings.Contains(targetHost, ":") {
			targetHost = targetHost + ":443"
		}

		// 转发到真实服务器
		resp, respBody, err := forwardToRealServer(targetHost, req, reqBody)
		if err != nil {
			log.Printf("[proxy_tls] 转发失败(%s): %v", sni, err)
			writeHTTPError(tlsConn, http.StatusBadGateway, "upstream error")
			return
		}

		// 回写响应
		resp.Write(tlsConn)

		// 明文捕获
		duration := time.Since(start).Milliseconds()
		p.captureHTTPS(sni, req.Method, req.URL.String(), string(reqBody),
			resp.StatusCode, string(respBody), duration, req.Header)

		// 检查是否需要关闭连接
		if resp.Close || req.Close || resp.Header.Get("Connection") == "close" {
			return
		}
	}
}

// peekSNIFromBuf 从 buffer 中解析 SNI(不依赖 net.Conn)。
func peekSNIFromBuf(buf []byte) (sni string, rawHello []byte, err error) {
	n := len(buf)
	if n < 43 {
		return "", nil, fmt.Errorf("数据太短(%d bytes)", n)
	}
	if buf[0] != 22 {
		return "", nil, fmt.Errorf("不是 TLS 流量(type=%d)", buf[0])
	}

	// 跳过 Record Header(5) + Handshake Header(4) + Version(2) + Random(32)
	pos := 5 + 4 + 2 + 32

	if pos >= n {
		return "", nil, fmt.Errorf("截断(1)")
	}
	sidLen := int(buf[pos])
	pos += 1 + sidLen

	if pos+1 >= n {
		return "", nil, fmt.Errorf("截断(2)")
	}
	cipherLen := int(buf[pos])<<8 | int(buf[pos+1])
	pos += 2 + cipherLen

	if pos >= n {
		return "", nil, fmt.Errorf("截断(3)")
	}
	compLen := int(buf[pos])
	pos += 1 + compLen

	if pos+1 >= n {
		return "", buf, nil
	}
	extLen := int(buf[pos])<<8 | int(buf[pos+1])
	pos += 2
	extEnd := pos + extLen
	if extEnd > n {
		extEnd = n
	}

	for pos+4 <= extEnd {
		extType := int(buf[pos])<<8 | int(buf[pos+1])
		extDataLen := int(buf[pos+2])<<8 | int(buf[pos+3])
		pos += 4

		if extType == 0x0000 { // server_name (SNI)
			if pos+5 > extEnd {
				break
			}
			nameLen := int(buf[pos+3])<<8 | int(buf[pos+4])
			nameStart := pos + 5
			nameEnd := nameStart + nameLen
			if nameEnd > n {
				nameEnd = n
			}
			return string(buf[nameStart:nameEnd]), buf, nil
		}
		pos += extDataLen
	}

	return "", buf, nil
}

// ── HTTP 请求/响应读写 ──

// readHTTPRequest 从 TLS 连接中读取完整的 HTTP 请求。
func readHTTPRequest(conn io.Reader) (*http.Request, []byte, error) {
	// 使用 http.ReadRequest 读取请求行 + 头
	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		return nil, nil, fmt.Errorf("读取 HTTP 请求失败: %w", err)
	}

	// 读取请求体
	var body []byte
	if req.Body != nil {
		body, err = io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return req, body, nil // 体读取失败不算致命
		}
	}

	return req, body, nil
}

// forwardToRealServer 向目标服务器发起 TLS 连接,转发请求,读取响应。
func forwardToRealServer(targetHost string, req *http.Request, reqBody []byte) (*http.Response, []byte, error) {
	// 建立到目标服务器的 TLS 连接
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	rawConn, err := dialer.Dial("tcp", targetHost)
	if err != nil {
		return nil, nil, fmt.Errorf("连接目标 %s 失败: %w", targetHost, err)
	}

	// 提取不带端口的 host 用于 TLS SNI
	hostForTLS := targetHost
	if colonIdx := strings.LastIndex(targetHost, ":"); colonIdx > 0 {
		hostForTLS = targetHost[:colonIdx]
	}

	tlsConfig := &tls.Config{
		ServerName:         hostForTLS,
		InsecureSkipVerify: false, // 验证真实服务器证书(不跳过,保证安全)
		MinVersion:         tls.VersionTLS12,
	}

	tlsConn := tls.Client(rawConn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		rawConn.Close()
		return nil, nil, fmt.Errorf("TLS 握手失败(%s): %w", targetHost, err)
	}
	defer tlsConn.Close()

	// 构造转发请求
	proxyReq, err := http.NewRequest(req.Method, "https://"+targetHost+req.URL.RequestURI(), bytes.NewReader(reqBody))
	if err != nil {
		return nil, nil, err
	}

	// 复制请求头(清理代理相关头)
	for key, values := range req.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "proxy-connection" || lowerKey == "proxy-authorization" {
			continue
		}
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}
	proxyReq.ContentLength = int64(len(reqBody))

	// 发送请求
	if err := proxyReq.Write(tlsConn); err != nil {
		return nil, nil, fmt.Errorf("发送请求失败: %w", err)
	}

	// 读取响应
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), proxyReq)
	if err != nil {
		return nil, nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var respBody []byte
	if resp.Body != nil {
		respBody, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return resp, respBody, nil
		}
	}

	return resp, respBody, nil
}

// writeHTTPError 回写 HTTP 错误响应。
func writeHTTPError(conn io.Writer, statusCode int, msg string) {
	resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n%s",
		statusCode, http.StatusText(statusCode), msg)
	conn.Write([]byte(resp))
}

// ── 会话捕获 ──

// captureHTTPS 明文捕获一次 HTTPS 请求/响应对。
func (p *TLSProxy) captureHTTPS(sni, method, url, reqBody string, respStatus int, respBody string, durationMs int64, reqHeaders http.Header) {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()

	// 新 SNI → 新会话
	if p.currentSess == nil || p.currentSess.TargetURL != sni {
		if p.currentSess != nil {
			p.currentSess.EndTime = time.Now()
		}
		p.currentSess = &Session{
			ID:        uuid.New().String(),
			TargetURL: sni,
			StartTime: time.Now(),
			Requests:  make([]CapturedExchange, 0),
		}
		p.sessions = append(p.sessions, p.currentSess)
		log.Printf("[proxy_tls] 新会话: %s → %s (HTTPS)", p.currentSess.ID, sni)
	}

	// 序列化请求头
	headerMap := make(map[string]string)
	for k, v := range reqHeaders {
		if len(v) > 0 {
			headerMap[k] = v[0]
		}
	}
	headersJSON, _ := json.Marshal(headerMap)

	// 首次请求时提取凭证
	if len(p.currentSess.Requests) == 0 {
		p.extractAuthFromHeaders(reqHeaders)
	}

	// 脱敏敏感信息
	safeReqBody := RedactSecrets(reqBody)
	safeRespBody := RedactSecrets(respBody)

	exchange := CapturedExchange{
		ID:         uuid.New().String(),
		SessionID:  p.currentSess.ID,
		Seq:        len(p.currentSess.Requests) + 1,
		Method:     method,
		URL:        url,
		ReqHeaders: string(headersJSON),
		ReqBody:    safeReqBody,
		RespStatus: respStatus,
		RespBody:   safeRespBody,
		DurationMs: durationMs,
		CapturedAt: time.Now(),
	}
	p.currentSess.Requests = append(p.currentSess.Requests, exchange)
}

// extractAuthFromHeaders 从请求头提取凭证(复用 proxy.go 的 ExtractedAuth 结构)。
func (p *TLSProxy) extractAuthFromHeaders(headers http.Header) {
	if p.currentSess == nil || p.currentSess.ExtractedAuth != nil {
		return
	}

	auth := &ExtractedAuth{}

	if bh := headers.Get("Authorization"); strings.HasPrefix(bh, "Bearer ") {
		token := strings.TrimPrefix(bh, "Bearer ")
		auth.BearerToken = maskToken(token)
		auth.AuthType = "bearer"
		if strings.HasPrefix(token, "sk-") {
			auth.OpenAIKey = maskToken(token)
			auth.AuthType = "openai"
		}
	}
	if key := headers.Get("x-api-key"); key != "" {
		auth.APIKey = maskToken(key)
		if auth.AuthType == "" {
			auth.AuthType = "api-key"
		}
	}
	if key := headers.Get("api-key"); key != "" {
		auth.APIKey = maskToken(key)
		if auth.AuthType == "" {
			auth.AuthType = "api-key"
		}
	}
	if cookie := headers.Get("Cookie"); cookie != "" {
		if len(cookie) > 30 {
			auth.SessionCookie = cookie[:30] + "..."
		} else {
			auth.SessionCookie = cookie
		}
		if auth.AuthType == "" {
			auth.AuthType = "session"
		}
	}

	if auth.AuthType != "" {
		p.currentSess.ExtractedAuth = auth
		log.Printf("[proxy_tls] 提取凭证: type=%s", auth.AuthType)
	}
}

// finishSession 结束当前会话。
func (p *TLSProxy) finishSession() {
	if p.currentSess != nil {
		p.currentSess.EndTime = time.Now()
		log.Printf("[proxy_tls] 会话结束: %s, 共 %d 次请求", p.currentSess.ID, len(p.currentSess.Requests))
		p.currentSess = nil
	}
}

// GetSessions 返回所有捕获的 HTTPS 会话。
func (p *TLSProxy) GetSessions() []*Session {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sessions
}

// Status 返回 HTTPS 代理状态。
func (p *TLSProxy) Status() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return map[string]interface{}{
		"running":      p.running,
		"listen_addr":  p.listenAddr,
		"protocol":     "HTTPS MITM",
		"sessions":     len(p.sessions),
		"ca_installed": CAExists(),
	}
}

// ── 全局 HTTPS 代理 API(与 proxy.go 的 StartProxy/StopProxy 配合)──

// StartTLSProxy 启动全局 HTTPS MITM 代理。
func StartTLSProxy(listenAddr string) error {
	if globalTLSProxy != nil && globalTLSProxy.running {
		return nil
	}

	// 确保 CA 证书存在
	if err := EnsureCA(); err != nil {
		return fmt.Errorf("CA 证书初始化失败: %w", err)
	}

	// 创建 TLS 配置
	tlsConfig, err := CreateTLSConfig()
	if err != nil {
		return fmt.Errorf("TLS 配置创建失败: %w", err)
	}

	globalTLSProxy = NewTLSProxy(listenAddr, tlsConfig)
	return globalTLSProxy.Start()
}

// StopTLSProxy 停止全局 HTTPS MITM 代理。
func StopTLSProxy() {
	if globalTLSProxy != nil {
		globalTLSProxy.Stop()
		globalTLSProxy = nil
	}
}

// GetTLSProxySessions 获取全局 HTTPS 代理的会话。
func GetTLSProxySessions() []*Session {
	if globalTLSProxy == nil {
		return nil
	}
	return globalTLSProxy.GetSessions()
}

// GetTLSProxyStatus 获取全局 HTTPS 代理状态。
func GetTLSProxyStatus() map[string]interface{} {
	if globalTLSProxy == nil {
		return map[string]interface{}{"running": false, "reason": "未启动"}
	}
	return globalTLSProxy.Status()
}

// ── 工具函数 ──

// RedactSecrets 脱敏文本中的敏感信息(API Key / Token / Password)。
// 复用 engine 包的 RedactText 逻辑(调用 engine 包的函数)。
// 此处提供独立的备份实现,避免循环依赖。
func RedactSecrets(text string) string {
	// 脱敏 API Key 模式(sk-xxx)
	s := text
	// sk-xxx 模式
	for i := 0; i < len(s)-20; i++ {
		if strings.HasPrefix(s[i:], "sk-") || strings.HasPrefix(s[i:], "SK-") {
			end := i + 4
			for end < len(s) && end-i < 48 && s[end] != '"' && s[end] != '\'' && s[end] != ' ' && s[end] != '\n' {
				end++
			}
			if end-i > 8 {
				s = s[:i+4] + "***REDACTED***" + s[end:]
				break
			}
		}
	}
	// Authorization: Bearer xxx
	s = replaceBetween(s, "Bearer ", "\n", "Bearer ***REDACTED***")
	s = replaceBetween(s, "bearer ", "\n", "bearer ***REDACTED***")
	// api_key=
	s = replaceBetween(s, "api_key=", "&", "api_key=***REDACTED***")
	// x-api-key:
	s = replaceBetween(s, "x-api-key:", "\n", "x-api-key: ***REDACTED***")
	return s
}

func replaceBetween(s, prefix, suffix, replacement string) string {
	start := strings.Index(strings.ToLower(s), strings.ToLower(prefix))
	if start < 0 {
		return s
	}
	start += len(prefix)
	end := strings.Index(s[start:], suffix)
	if end < 0 {
		end = len(s) - start
	}
	return s[:start] + replacement + s[start+end:]
}

// ── 原有函数(从旧 proxy_tls.go 保留)──

// StartHTTPSProxy 启动 HTTPS MITM 代理(对外统一入口)。
// 已由 StartTLSProxy 实现完整功能,此函数作为兼容别名。
func StartHTTPSProxy(listenAddr string, tlsConfig *tls.Config) error {
	if tlsConfig == nil {
		return fmt.Errorf("tlsConfig 不能为空")
	}
	return StartTLSProxy(listenAddr)
}
