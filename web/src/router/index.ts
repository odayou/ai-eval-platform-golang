import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'Tasks',
    component: () => import('../views/Tasks.vue'),
    meta: { title: '评测任务', icon: 'Monitor' },
  },
  {
    path: '/resources',
    name: 'Resources',
    component: () => import('../views/Resources.vue'),
    meta: { title: '资源管理中心', icon: 'Box' },
  },
  {
    path: '/create-agent',
    name: 'CreateAgent',
    component: () => import('../views/CreateAgent.vue'),
    meta: { title: '创造 Agent', icon: 'MagicStick' },
  },
  {
    path: '/pipeline',
    name: 'Pipeline',
    component: () => import('../views/Pipeline.vue'),
    meta: { title: 'CI/CD 管线', icon: 'Connection' },
  },
  {
    path: '/arena',
    name: 'Arena',
    component: () => import('../views/Arena.vue'),
    meta: { title: 'Agent 对战', icon: 'Trophy' },
  },
  {
    path: '/redteam',
    name: 'RedTeam',
    component: () => import('../views/RedTeam.vue'),
    meta: { title: '安全红队', icon: 'Warning' },
  },
  {
    path: '/observe',
    name: 'Observability',
    component: () => import('../views/Observability.vue'),
    meta: { title: '生产监控', icon: 'DataLine' },
  },
  {
    path: '/finetune',
    name: 'FineTune',
    component: () => import('../views/FineTune.vue'),
    meta: { title: '微调数据', icon: 'MagicStick' },
  },
  {
    path: '/workflow',
    name: 'Workflow',
    component: () => import('../views/Workflow.vue'),
    meta: { title: '多Agent编排', icon: 'Share' },
  },
  {
    path: '/questions',
    name: 'Questions',
    component: () => import('../views/Questions.vue'),
    meta: { title: '评测题库', icon: 'Document' },
  },
  {
    path: '/standards',
    name: 'Standards',
    component: () => import('../views/Standards.vue'),
    meta: { title: '评测标准', icon: 'Scale' },
  },
  {
    path: '/dashboard',
    name: 'Dashboard',
    component: () => import('../views/Dashboard.vue'),
    meta: { title: '数据看板', icon: 'DataAnalysis' },
  },
  {
    path: '/cron',
    name: 'Cron',
    component: () => import('../views/Cron.vue'),
    meta: { title: '定时任务', icon: 'Timer' },
  },
  {
    path: '/clone',
    name: 'Clone',
    component: () => import('../views/Clone.vue'),
    meta: { title: '克隆对比', icon: 'CopyDocument' },
  },
  {
    path: '/terminal',
    name: 'Terminal',
    component: () => import('../views/Terminal.vue'),
    meta: { title: '终端轨道', icon: 'Monitor' },
  },
  {
    path: '/proxy',
    name: 'Proxy',
    component: () => import('../views/Proxy.vue'),
    meta: { title: '协议代理', icon: 'Connection' },
  },
  {
    path: '/regression',
    name: 'Regression',
    component: () => import('../views/Regression.vue'),
    meta: { title: '回归看板', icon: 'TrendCharts' },
  },
  {
    path: '/settings',
    name: 'Settings',
    component: () => import('../views/Settings.vue'),
    meta: { title: '设置', icon: 'Setting' },
  },
  {
    path: '/leaderboard',
    name: 'Leaderboard',
    component: () => import('../views/Leaderboard.vue'),
    meta: { title: '排行榜', icon: 'Trophy' },
  },
  {
    path: '/audit',
    name: 'AuditLog',
    component: () => import('../views/AuditLog.vue'),
    meta: { title: '审计日志', icon: 'View' },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

export default router
