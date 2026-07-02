# sandbox-python.Dockerfile — Python 语言沙箱镜像(MRD 5.3)
#
# 构建: docker build -f docker/sandbox-python.Dockerfile -t eval-sandbox-python:latest .
# 使用: docker run --rm -v $(pwd):/code:ro --network none --cpus=1 --memory=512m eval-sandbox-python:latest /eval.sh main.py python 60

FROM python:3.12-alpine

# 安装测试和 Lint 工具
RUN pip install --no-cache-dir pytest pylint

# 复制入口脚本
COPY docker/eval.sh /eval.sh
RUN chmod +x /eval.sh

RUN mkdir -p /code
WORKDIR /code

ENTRYPOINT ["/eval.sh"]
