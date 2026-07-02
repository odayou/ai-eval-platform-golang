# 贡献指南

## 开发环境

- Go 1.25+
- Node.js 20+ (for frontend)
- Docker (optional, for sandbox execution)

## 本地开发

### 后端

```bash
# 热重载(需要 air)
go install github.com/air-verse/air@latest
air

# 或手动编译
go build -o server.exe ./cmd/server/
./server.exe
```

### 前端

```bash
cd web
npm install
npm run dev
```

## 代码规范

### Go

- 遵循 [Effective Go](https://go.dev/doc/effective_go) 和 [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments)
- 每个包必须有包注释（package comment）
- 导出函数必须有 GoDoc 注释
- 错误处理优先，不吞 error
- 文件名用 snake_case

### Vue/TypeScript

- 使用 `<script setup>` 语法
- 组件名用 PascalCase
- 事件名用 kebab-case
- 状态管理用 Pinia（不使用 Vuex）
- API 调用统一走 `src/utils/api.ts`

## 测试

```bash
# 运行全部后端测试
go test -v -count=1 ./internal/...

# 带覆盖率
go test -v -count=1 -coverprofile=coverage.out ./internal/...
go tool cover -html=coverage.out
```

## 提 PR 流程

1. Fork 仓库
2. 创建 feature branch: `git checkout -b feat/your-feature`
3. 提交代码: `git commit -m "feat: add xxx"`
4. 推送到 fork: `git push origin feat/your-feature`
5. 创建 Pull Request

### Commit 规范

遵循 [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` 新功能
- `fix:` Bug 修复
- `docs:` 文档
- `test:` 测试
- `refactor:` 重构
- `perf:` 性能优化
- `chore:` 构建/工具链

## 架构决策

### 为什么用 SQLite 而不是 PostgreSQL？

- **免安装** — 纯 Go 驱动 (`modernc.org/sqlite`)，无 CGO，无外部依赖
- **单机够用** — 评测平台是单用户/小团队工具，不需要分布式数据库
- **易于部署** — 一个二进制文件 + 一个数据库文件即可运行

### 为什么用纯 Go SQLite 驱动？

- **跨平台编译** — CGO 依赖 GCC/MinGW，纯 Go 驱动一行 `go build` 搞定
- **CI 简单** — 不需要安装 C 编译器

### 为什么用 mock 回退策略？

- **Demo 可用** — 无 API Key 时自动 mock 回退，确保演示环境可运行
- **开发隔离** — 前端开发不依赖真实 AI 模型
- **测试方便** — 测试不需要网络和 API Key

## 模块职责

| 包 | 职责 | 禁止依赖 |
|----|------|---------|
| `api` | HTTP 路由 + 请求处理 | 不能直接操作数据库（通过 store） |
| `engine` | 评测核心逻辑 | 不能依赖 `api` 包（循环引用） |
| `scheduler` | 任务调度 | 只依赖 `store` 和回调函数 |
| `store` | 数据持久化 | 不能依赖其他 internal 包 |
| `proxy` | MITM 代理 | 不能依赖 `engine`（业务无关） |
| `sdk` | 外部接入 | 不能依赖 internal 其他包 |
