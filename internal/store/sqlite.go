// Package store 是平台的数据访问层,负责 SQLite 连接管理与表结构迁移。
//
// 文件拆分(按业务域分组,避免单文件膨胀):
//   - sqlite.go      : 连接初始化 + 迁移编排(本文件)
//   - schema_core.go : 七库核心表(agents/models/tasks/rules/tools/mcp/skills/...)
//   - schema_eval.go : 评测域表(questions/bad_cases/attribution)
//   - schema_agent.go: Agent 运行时表(借鉴 claw-code:对话/权限/压缩/会话)
//
// claw-code 架构映射(在 schema_agent.go 中预留):
//   - conversation → 评测会话(ConversationRuntime 的持久化镜像)
//   - permission   → 权限门控记录(借鉴 claw-code permission_enforcer)
//   - compaction   → 上下文压缩摘要(借鉴 claw-code compact.rs)
//   - session      → 会话快照 + 密钥脱敏(借鉴 claw-code session.rs)
package store

import (
	"database/sql"
	"log"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动,免 CGO
)

// DB 是全局数据库连接,由 Init 注入,整个进程共享。
// SQLite 为单写模型,已在 Init 中设置 SetMaxOpenConns(1)。
var DB *sql.DB

// Init 打开(或创建)SQLite 数据库并执行全部迁移。
// dbPath: 数据库文件路径;连接启用 WAL 模式以提升并发读性能。
func Init(dbPath string) {
	var err error
	// _pragma 通过 DSN 启用 WAL(写前日志)+ 忙等待,适配 SQLite 单写并发模型
	DB, err = sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

		// SQLite 写串行:WAL 模式支持并发读 + 单写入。
		// 连接数按并发评测需求配置(MaxConcurrentEvals 默认 2):
		//   - 2 主引擎 Runner(并发 worker)
		//   - 1~2 后台异步归因 goroutine
		//   - 1~2 HTTP API 查询
		// busy_timeout(5000) 保证写冲突时等待而非报 "database is locked"。
		DB.SetMaxOpenConns(6)
		DB.SetMaxIdleConns(4)
	if err = DB.Ping(); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}

	// 按域分批执行迁移
	migrateCore()    // 七库核心表
	migrateEval()    // 评测域表(题库/Bad Case/归因)
	migrateAgent()   // Agent 运行时表(claw-code 借鉴)

	log.Println("database initialized:", dbPath)
}

// Close 关闭数据库连接,通常在进程退出时由 main defer 调用。
func Close() {
	if DB != nil {
		_ = DB.Close()
	}
}

// execAll 按顺序执行一组建表/迁移语句,任一失败即 fatal。
// 抽出来供各 schema_*.go 文件复用,保持迁移逻辑统一。
func execAll(label string, stmts []string) {
	for _, s := range stmts {
		if _, err := DB.Exec(s); err != nil {
			log.Fatalf("[%s] migration failed: %v\nSQL: %s", label, err, s)
		}
	}
}
