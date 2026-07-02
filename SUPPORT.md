# 支持

## 问题报告

- GitHub Issues: [创建新 Issue](https://github.com/ai-eval-platform/issues/new)
- 安全漏洞: 见 [SECURITY.md](SECURITY.md)

## FAQ

### Q: 启动后访问页面空白？
A: 确保前端已构建 (`cd web && npm run build`)，或使用 Vite 开发模式 (`npm run dev`)。

### Q: 评测一直 pending 不执行？
A: 检查调度器是否正常运行（日志中有 `[scheduler] worker 0 启动` 字样）。单次评测默认超时 5 分钟。

### Q: 如何配置 Docker 沙箱？
A: 设置环境变量 `DOCKER_ENABLED=true`，并确保 Docker 已安装且当前用户有权限。

### Q: 如何更换前端端口？
A: 修改 `web/vite.config.ts` 中的 `server.port`。

### Q: 评测结果全是 mock 数据？
A: 在设置页配置真实 API Key。未配置 Key 时自动回退到 mock 模式。
