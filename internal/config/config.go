// Package config 负责平台运行配置的加载与默认值兜底。
//
// 设计原则:
//   - 所有可调参数集中在此,业务代码只读 Config,不直接读环境变量
//   - 优先级:环境变量 > 默认值(暂不支持配置文件,后续 ⑤ 产品化再加 config.yaml)
//   - 敏感信息(API Key)不入配置对象,留在数据库 models 表里按需取用
package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 是平台运行配置的不可变快照,启动时一次性加载。
type Config struct {
	// Server 服务监听端口,默认 8080,可用 PORT 覆盖
	Port string

	// DBPath SQLite 数据库文件路径,默认 ./data/eval.db,可用 DB_PATH 覆盖
	DBPath string

	// GinMode Gin 运行模式:debug / release / test,默认 debug
	GinMode string

	// MaxConcurrentEvals 单进程内最大并发评测任务数(worker pool 大小)。
	//
	// 默认 2。并发安全已通过 per-goroutine 的 EvalContext 保障(见 engine/evalctx.go):
	// 各 worker goroutine 绑定各自的审计/沙箱上下文,互不串台。
	// 瓶颈转为 SQLite 单写模型(WAL 模式 + busy_timeout 兜底),2 并发是吞吐与
	// 写锁竞争的平衡点。需要更高吞吐可上调(建议同时评估迁移到 Postgres)。
	// 可用 MAX_CONCURRENT_EVALS 覆盖。
	MaxConcurrentEvals int

		// EvalTimeoutSec 单次评测超时秒数(为 ②引擎熔断预留),默认 300 秒
		EvalTimeoutSec int

	// DockerEnabled 是否启用 Docker 沙箱容器池。
	// 默认 false(进程级沙箱),可用 DOCKER_ENABLED=true 开启。
	// 开启后若 Docker 不可用则自动回退到进程级沙箱。
	DockerEnabled bool

	// CORSOrigins 允许的前端来源(逗号分隔)。默认本地开发用的两个来源。
	// 生产部署时用 CORS_ORIGINS=https://eval.example.com,https://eval2.example.com 覆盖。
	// 留空则允许所有来源(仅建议内网/调试用)。
	CORSOrigins []string

	// RateLimit 每秒每 IP 最大请求数(令牌桶 RPS)。0=关闭限流。
	// 默认 10:防恶意高频请求灌满任务队列或白嫖 LLM 额度,正常用户够用。
	RateLimit float64

	// RateBurst 限流突发容量上限(令牌桶容量)。默认 20。
	RateBurst float64
}

// Load 从环境变量读取配置,未设置的字段使用默认值,并确保数据目录存在。
// 启动失败时直接 fatal —— 配置错误无法继续运行。
func Load() *Config {
	cfg := &Config{
		Port:               getenv("PORT", "8080"),
		DBPath:             getenv("DB_PATH", filepath.Join("data", "eval.db")),
		GinMode:            getenv("GIN_MODE", "debug"),
		MaxConcurrentEvals: getenvInt("MAX_CONCURRENT_EVALS", 2),
		EvalTimeoutSec:     getenvInt("EVAL_TIMEOUT_SEC", 300),
		DockerEnabled:      getenv("DOCKER_ENABLED", "false") == "true",
		CORSOrigins:        parseCORSOrigins(getenv("CORS_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173")),
		RateLimit:          getenvFloat("RATE_LIMIT", 10),
		RateBurst:          getenvFloat("RATE_BURST", 20),
	}

	// 确保数据库所在目录存在,否则 modernc/sqlite 打开会失败
	if dir := filepath.Dir(cfg.DBPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("config: cannot create data dir %q: %v", dir, err)
		}
	}

	return cfg
}

// getenv 读取环境变量,缺失时返回 fallback。
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getenvInt 读取整型环境变量,缺失或非法时返回 fallback。
func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("config: invalid int for %s=%q, using default %d", key, v, fallback)
	}
	return fallback
}

// getenvFloat 读取浮点环境变量,缺失或非法时返回 fallback。
func getenvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
		log.Printf("config: invalid float for %s=%q, using default %g", key, v, fallback)
	}
	return fallback
}

// parseCORSOrigins 把逗号分隔的来源字符串切成切片(去空白、忽略空项)。
// 输入为空时返回 nil(调用方据此判断"允许所有来源")。
func parseCORSOrigins(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		origin := strings.TrimSpace(part)
		if origin != "" {
			out = append(out, origin)
		}
	}
	return out
}
