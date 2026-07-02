# sandbox-node.Dockerfile — Node.js 语言沙箱镜像(MRD 5.3)
#
# 构建: docker build -f docker/sandbox-node.Dockerfile -t eval-sandbox-node:latest .
# 使用: docker run --rm -v $(pwd):/code:ro --network none --cpus=1 --memory=512m eval-sandbox-node:latest /eval.sh index.js javascript 60

FROM node:20-alpine

# 安装测试和 Lint 工具
RUN npm install -g jest eslint

# 复制入口脚本
COPY docker/eval.sh /eval.sh
RUN chmod +x /eval.sh

RUN mkdir -p /code
WORKDIR /code

ENTRYPOINT ["/eval.sh"]
