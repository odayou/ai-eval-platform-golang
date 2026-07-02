package api

// middleware.go — 通用 HTTP 中间件。
//
// 目前提供:
//   - rateLimitMiddleware: 基于 IP 的令牌桶限流,防止恶意高频请求灌满任务队列
//     或白嫖 LLM 额度(ai-create-agent / submit-task 等接口会真实调用 LLM 花钱)。
//
// 设计权衡:
//   - 纯标准库实现(不引入 golang.org/x/time/rate),零新依赖。
//   - 按 IP 维度限流(公网部署时 IP 是最细可用标识;内网/单机场景全局限流也够用)。
//   - 用 sync.Map 管理 per-IP 桶,懒加载;无后台清理(评测平台 QPS 不高,桶数量可控)。

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// tokenBucket 是一个简单的令牌桶(线程安全)。
// 按 fixed interval 补充令牌;取令牌时若不足则拒绝。
type tokenBucket struct {
	mu         sync.Mutex
	tokens     float64   // 当前令牌数
	last       time.Time // 上次补充时间
	rate       float64   // 每秒补充的令牌数(RPS)
	burst      float64   // 桶容量(突发上限)
}

// allow 尝试取 1 个令牌,成功返回 true。
func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	// 按经过时间补充令牌(不超容量)
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

// rateLimiter 维护 per-IP 的令牌桶集合。
type rateLimiter struct {
	buckets sync.Map // map[string]*tokenBucket
	rate    float64
	burst   float64
}

// newRateLimiter 创建一个限流器。rate=每秒请求数, burst=突发容量上限。
func newRateLimiter(rate, burst float64) *rateLimiter {
	return &rateLimiter{rate: rate, burst: burst}
}

// bucketFor 返回(或懒创建)指定 key(IP)的令牌桶。
func (rl *rateLimiter) bucketFor(key string) *tokenBucket {
	if v, ok := rl.buckets.Load(key); ok {
		return v.(*tokenBucket)
	}
	b := &tokenBucket{
		tokens: rl.burst, // 初始满桶,允许初始突发
		last:   time.Now(),
		rate:   rl.rate,
		burst:  rl.burst,
	}
	actual, _ := rl.buckets.LoadOrStore(key, b)
	return actual.(*tokenBucket)
}

// clientIP 提取客户端 IP(优先 X-Forwarded-For 首段,回退 RemoteAddr)。
func clientIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	return c.ClientIP()
}

// rateLimitMiddleware 返回一个按 IP 限流的 Gin 中间件。
// 超过速率的请求返回 429 Too Many Requests。
//
// 默认参数(见 NewRouter): 每秒 10 请求,突发 20。
// 对评测平台够用:正常用户不可能秒级连发,而脚本灌队列会立刻被拦。
func rateLimitMiddleware(rate, burst float64) gin.HandlerFunc {
	rl := newRateLimiter(rate, burst)
	return func(c *gin.Context) {
		ip := clientIP(c)
		if !rl.bucketFor(ip).allow() {
			log.Printf("[ratelimit] IP %s 被限流(超过 %.0f req/s)", ip, rate)
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "请求过于频繁,请稍后再试",
			})
			return
		}
		c.Next()
	}
}
