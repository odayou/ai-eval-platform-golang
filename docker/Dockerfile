# AI Eval Platform — 多阶段构建镜像
#
# 用法:
#   docker build -t ai-eval-platform -f docker/Dockerfile .
#   docker run -p 8080:8080 -v ./data:/app/data ai-eval-platform

# ── 阶段 1: 构建前端 ──
FROM node:20-alpine AS frontend-builder

WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# ── 阶段 2: 构建后端 ──
FROM golang:1.25-alpine AS backend-builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# 复制前端构建产物
COPY --from=frontend-builder /app/web/dist ./web/dist

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/server ./cmd/server/

# ── 阶段 3: 运行镜像 ──
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=backend-builder /app/server .
COPY --from=backend-builder /app/web/dist ./web/dist

# 数据目录(挂载卷)
VOLUME ["/app/data"]

EXPOSE 8080

ENV PORT=8080
ENV DB_PATH=/app/data/eval.db
ENV GIN_MODE=release
ENV DOCKER_ENABLED=false

ENTRYPOINT ["/app/server"]
