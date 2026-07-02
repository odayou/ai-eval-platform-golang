# sandbox-go.Dockerfile — Go 语言沙箱镜像(MRD 5.3 动态沙箱压测层)
#
# 构建: docker build -f docker/sandbox-go.Dockerfile -t eval-sandbox-go:latest .
# 使用: docker run --rm -v $(pwd):/code:ro --network none --cpus=1 --memory=512m eval-sandbox-go:latest /eval.sh main.go go 60
#
# 内含:
#   - Go 1.22 工具链(编译 + 测试 + vet)
#   - git/gcc(部分 Go 包需要 CGO)
#   - eval.sh 统一入口脚本

FROM golang:1.22-alpine

# 基础工具
RUN apk add --no-cache git gcc musl-dev

# 安装常用 Go 工具
RUN go install golang.org/x/tools/cmd/goimports@latest 2>/dev/null || true

# 复制入口脚本
COPY docker/eval.sh /eval.sh
RUN chmod +x /eval.sh

# 创建工作目录
RUN mkdir -p /code
WORKDIR /code

ENTRYPOINT ["/eval.sh"]
