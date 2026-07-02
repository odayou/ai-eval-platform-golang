package api

// middleware_test.go — 限流中间件单元测试。
//
// 覆盖:
//   - tokenBucket 在容量内放行、超容量拒绝
//   - 令牌按速率恢复
//   - rateLimitMiddleware 超限返回 429、未超限放行

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTokenBucketAllowsWithinBurst(t *testing.T) {
	// 容量 3,速率很低(0.01/s)→ 前 3 个请求应全部放行(吃初始满桶)
	b := &tokenBucket{tokens: 3, last: time.Now(), rate: 0.01, burst: 3}
	for i := 0; i < 3; i++ {
		if !b.allow() {
			t.Fatalf("第 %d 个请求应放行(容量内)", i+1)
		}
	}
	// 第 4 个应被拒(桶空,速率太低来不及补充)
	if b.allow() {
		t.Fatal("第 4 个请求应被拒绝(超容量)")
	}
}

func TestTokenBucketRefills(t *testing.T) {
	// 容量 1,速率 100/s → 取空后等一小段时间应能再取
	b := &tokenBucket{tokens: 1, last: time.Now(), rate: 100, burst: 1}
	if !b.allow() {
		t.Fatal("首次应放行")
	}
	if b.allow() {
		t.Fatal("第二次应被拒(桶空)")
	}
	time.Sleep(30 * time.Millisecond) // 等待令牌补充
	if !b.allow() {
		t.Fatal("补充后应再次放行")
	}
}

func TestRateLimitMiddlewareAllowsAndBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 容量 2、速率 0(不补充)→ 前 2 个放行,第 3 个被拦
	r.Use(rateLimitMiddleware(0, 2))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	// 前 2 次应 200
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("第 %d 次请求应为 200,实际 %d", i+1, w.Code)
		}
	}
	// 第 3 次应 429
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("超限请求应为 429,实际 %d", w.Code)
	}
}
