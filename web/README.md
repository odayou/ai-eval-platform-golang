# AI Eval Platform — 前端

## 技术栈

- Vue 3.5 (Composition API + `<script setup>`)
- TypeScript
- Vite 8
- Element Plus 2.14
- ECharts 6.1
- Pinia 3
- Vue Router 4
- Axios
- xterm.js
- Monaco Editor

## 开发

```bash
npm install
npm run dev      # 开发模式,端口 5173
npm run build    # 生产构建
npm run preview  # 预览构建产物
```

## 项目结构

```
src/
├── main.ts                  # 入口
├── App.vue                  # 主布局(侧边栏 + 内容区)
├── style.css                # 全局样式
├── router/index.ts          # 路由(11 个页面)
├── utils/api.ts             # HTTP 客户端(Axios)
├── stores/
│   ├── taskStore.ts         # 任务/场景状态
│   └── dictStore.ts         # 字典缓存(Agent/规则集等)
├── views/                   # 页面组件
│   ├── Tasks.vue            # 评测任务(首页)
│   ├── Resources.vue        # 资源管理中心(7 个 Tab)
│   ├── Questions.vue        # 评测题库
│   ├── Standards.vue        # 评测标准(规则集)
│   ├── Dashboard.vue        # 数据看板
│   ├── Clone.vue            # 克隆 A/B 测试
│   ├── Cron.vue             # 定时任务
│   ├── Regression.vue       # 回归看板
│   ├── Terminal.vue         # 终端轨道
│   ├── Proxy.vue            # 协议代理
│   ├── Settings.vue         # 设置
│   └── Leaderboard.vue      # 排行榜
├── components/
│   ├── BaseChart.vue        # ECharts 封装
│   ├── DiffViewer.vue       # Monaco Diff
│   └── RuleGraph.vue        # 知识图谱(Canvas)
│   └── tasks/
│       ├── TaskList.vue     # 任务列表(左栏)
│       ├── TaskDetail.vue   # 任务详情(右栏)
│       ├── TaskReport.vue   # 评测报告
│       ├── TaskRun.vue      # 运行监控
│       ├── TaskCreateDialog.vue # 创建任务
│       ├── TaskAttribution.vue  # 归因分析
│       ├── TrajectoryGraph.vue  # 轨迹拓扑图
│       └── XtermTerminal.vue    # 终端日志
├── composables/
│   └── useHeavyCompute.ts   # Web Worker 封装
└── workers/
    └── data-worker.ts       # 大数据计算 Worker
```

## API 代理

开发模式下,`/api/*` 请求自动代理到 `http://localhost:8080`（配置在 `vite.config.ts`）。
