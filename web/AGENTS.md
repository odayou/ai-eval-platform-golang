# AGENTS.md — Frontend (Vue3)

> 前端 agent 上下文入口。

## 结构

- `views/`：页面组件（Tasks/Resources/CreateAgent/Settings 等）
- `components/`：复用组件（tasks/ 下有 TaskCreateDialog/TaskRun/TrajectoryGraph 等）
- `stores/`：Pinia（taskStore/dictStore）
- `router/index.ts`：路由配置
- `utils/api.ts`：统一 API 调用封装

## 命令

- 构建（含类型检查）：`npm run build`（先 vue-tsc -b 再 vite build）
- 测试：`npm test -- --run`（vitest）
- npm 在 `C:\Program Files\nodejs`

## 约定

- `<script setup lang="ts">` + Element Plus 组件
- API 调用走 `import api from '../utils/api'`，返回已 unwrap 的 data
- 新增 Tab：在对应 views 的 `<el-tabs>` 加 `<el-tab-pane>`，数据在 `<script setup>` 加 ref + load 函数 + onMounted
- 新增页面：路由 + App.vue navItems + dictStore（如需字典）

## 禁止

- 不要用 any 绕过类型检查（vue-tsc 会报错）
- 不要在组件里直接 fetch（走 utils/api）
- 路由测试 `src/router/__tests__/index.spec.ts` 要同步更新路由数期望
