# AI Eval Platform SDK — Agent 接入指南

## 概述

AI Eval Platform SDK 提供标准化的 Agent 接入协议。任何 Agent 只需实现 SDK 定义的接口，
即可被平台自动发现、注册、评测。

## 接入方式

### 方式一：Go SDK（推荐）

如果你的 Agent 用 Go 编写，直接导入 SDK 包：

```go
import "ai-eval-platform/internal/sdk"

// 1. 创建平台客户端
client := sdk.NewPlatformClient("http://localhost:8080/api", "")

// 2. 注册 Agent
agent, _ := client.RegisterAgent(sdk.AgentRegistration{
    Name:    "My Agent",
    Model:   "gpt-4o",
    Adapter: "api",
})

// 3. 提交评测
task, _ := client.SubmitTask(sdk.TaskSubmission{
    Name:    "首次评测",
    AgentID: agent.ID,
    Scene:   "agent",
    RuleSet: `{"rule_set_id":"rs-fullstack"}`,
})

// 4. 等待结果
result, _ := client.WaitForTask(task.ID)
fmt.Printf("综合分: %.1f\n", result.Overall)
```

### 方式二：REST API（任何语言）

平台提供标准 REST API，任何语言都可以调用：

```bash
# 注册 Agent
curl -X POST http://localhost:8080/api/agents \
  -H "Content-Type: application/json" \
  -d '{"name":"My Agent","model":"glm-4","adapter":"api"}'

# 创建评测任务
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"name":"首次评测","agent_id":"<agent-id>","scene":"agent","rule_set":"{\"rule_set_id\":\"rs-fullstack\"}"}'

# 查看结果
curl http://localhost:8080/api/tasks/<task-id>
```

### 方式三：MCP 协议（高级）

平台支持 MCP (Model Context Protocol) 作为底层评测协议。
Agent 只需暴露一个 `/eval` 端点，平台自动按 MCP 格式调用：

```bash
# Agent 侧实现
POST /eval
Content-Type: application/json

{
  "prompt": "实现一个线程安全的计数器",
  "context": {},
  "max_tokens": 2048,
  "temperature": 0.2
}

# 响应
{
  "text": "// 代码实现...",
  "token_in": 128,
  "token_out": 256,
  "model": "glm-4",
  "latency_ms": 1500
}
```

## API 参考

### Agent 注册

```
POST /api/agents
{
  "name": "string",           // 必填: Agent 名称
  "model": "string",           // 必填: 模型标识
  "adapter": "api|terminal|source", // 接入方式
  "endpoint": "string",        // 评测端点 URL(可选)
  "description": "string",     // 描述(可选)
  "version": "string"          // 版本(可选)
}
```

### 创建评测任务

```
POST /api/tasks
{
  "name": "string",            // 必填: 任务名称
  "scene": "agent",            // 场景
  "agent_id": "string",        // Agent ID
  "rule_set": "{\"rule_set_id\":\"rs-fullstack\"}", // 规则集
  "chaos_enabled": false       // 混沌对抗
}
```

### 评测结果格式

```json
{
  "id": "task-id",
  "name": "评测任务",
  "status": "done",
  "result": {
    "overall": 82.5,
    "dimensions": {
      "security": 90,
      "performance": 78,
      "architecture": 80,
      "correctness": 85
    },
    "bad_cases": 1,
    "breaker": {
      "total_tokens": 15000,
      "est_cost_usd": 0.03
    }
  }
}
```

## 快速测试

```bash
# 使用 SDK 自带的 CLI 工具
go run internal/sdk/cmd/eval-cli/main.go \
  -agent "My Agent" \
  -model "glm-4" \
  -scene "agent" \
  -rule-set "rs-fullstack"
```

## 最佳实践

1. **始终配置 API Key**：在平台 Settings 页填写真实 Key，否则评测回退到 mock 模式
2. **从简单开始**：先用 `scene=agent` + `rs-fullstack` 规则集跑一次完整流程
3. **逐步启用高级功能**：混沌对抗 → 克隆对比 → 回归测试
4. **监控断路器**：在评测报告中查看 Token/费用，调整熔断阈值
