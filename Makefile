# AI Eval Platform — Makefile
# 用法: mingw32-make [target] (Windows) 或 make [target] (Linux/Mac)

.PHONY: all build test test-race vet clean run dev frontend frontend-build docker docker-run coverage help

GO ?= go
GOFLAGS ?= -ldflags="-s -w"
BINARY ?= server
NPM ?= npm

all: vet build test frontend-build clean-stale  ## 完整构建+测试

# ── 后端 ──

build:  ## 编译后端
	$(GO) build $(GOFLAGS) -o $(BINARY)$(EXE_EXT) ./cmd/server/

run:  ## 启动后端(开发模式)
	$(GO) run ./cmd/server/

test:  ## 运行全部测试
	$(GO) test -count=1 -timeout=180s ./internal/...

test-race:  ## 带竞态检测的测试
	$(GO) test -race -count=1 -timeout=300s ./internal/...

test-short:  ## 运行快捷测试(跳过集成)
	$(GO) test -count=1 -timeout=60s -short ./internal/engine/ ./internal/store/ ./internal/proxy/ ./internal/api/ ./internal/sdk/

test-integration:  ## 运行集成测试
	$(GO) test -count=1 -timeout=120s -v -run "TestIntegration" ./internal/engine/

coverage:  ## 测试覆盖率报告
	$(GO) test -count=1 -timeout=180s -coverprofile=coverage.out ./internal/...
	$(GO) tool cover -html=coverage.out -o coverage.html
	$(GO) tool cover -func=coverage.out
	@echo "覆盖率报告: coverage.html"

vet:  ## 代码静态检查
	$(GO) vet ./internal/...
	@echo "VET OK"

clean:  ## 清理构建产物
	$(GO) clean
	rm -f $(BINARY) $(BINARY).exe coverage.out coverage.html nul
	rm -rf web/dist

clean-stale:  ## 清理残余文件
	rm -f internal/engine/go.mod internal/engine/go.sum server.exe nul

# ── 前端 ──

frontend: frontend-build  ## 前端构建(别名)

frontend-install:  ## 安装前端依赖
	cd web && $(NPM) install

frontend-dev:  ## 启动前端开发服务器
	cd web && $(NPM) run dev

frontend-build:  ## 构建前端
	cd web && $(NPM) run build

frontend-lint:  ## 前端类型检查
	cd web && npx vue-tsc --noEmit

# ── Docker ──

docker:  ## 构建 Docker 镜像
	docker build -t ai-eval-platform -f docker/Dockerfile .

docker-run:  ## 启动 Docker 容器
	docker compose up -d

docker-stop:  ## 停止 Docker 容器
	docker compose down

# ── 开发工具 ──

dev:  ## 启动完整开发环境(后端+前端)
	@echo "启动后端 (端口 8080)..."
	$(GO) run ./cmd/server/ &
	@echo "启动前端 (端口 5173)..."
	cd web && $(NPM) run dev

help:  ## 显示帮助
	@echo "AI Eval Platform — Makefile"
	@echo ""
	@echo "用法:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | head -1
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
