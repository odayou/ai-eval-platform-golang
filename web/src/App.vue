<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'

const router = useRouter()
const route = useRoute()
const collapsed = ref(false)

const navItems = [
  { path: '/', icon: 'Monitor', label: '评测任务' },
  { path: '/create-agent', icon: 'MagicStick', label: '创造 Agent' },
  { path: '/pipeline', icon: 'Connection', label: 'CI/CD' },
  { path: '/arena', icon: 'Trophy', label: '对战' },
  { path: '/redteam', icon: 'Warning', label: '红队' },
  { path: '/observe', icon: 'DataLine', label: '监控' },
  { path: '/finetune', icon: 'MagicStick', label: '微调' },
  { path: '/workflow', icon: 'Share', label: '编排' },
  { path: '/resources', icon: 'Box', label: '资源管理' },
  { path: '/questions', icon: 'Document', label: '评测题库' },
  { path: '/standards', icon: 'Scale', label: '评测标准' },
  { path: '/dashboard', icon: 'DataAnalysis', label: '数据看板' },
  { path: '/cron', icon: 'Timer', label: '定时任务' },
  { path: '/clone', icon: 'CopyDocument', label: '克隆对比' },
  { path: '/regression', icon: 'TrendCharts', label: '回归看板' },
  { path: '/terminal', icon: 'Monitor', label: '终端轨道' },
  { path: '/proxy', icon: 'Connection', label: '协议代理' },
  { path: '/leaderboard', icon: 'TrophyBase', label: '排行榜' },
  { path: '/audit', icon: 'View', label: '审计日志' },
  { path: '/settings', icon: 'Setting', label: '设置' },
]

function navigate(path: string) {
  router.push(path)
}
</script>

<template>
  <el-container class="app-container">
    <!-- Narrow sidebar -->
    <el-aside :width="collapsed ? '56px' : '180px'" class="sidebar">
      <div class="sidebar-logo" @click="collapsed = !collapsed">
        <el-icon :size="24"><Cpu /></el-icon>
        <span v-if="!collapsed" class="logo-text">AI Eval</span>
      </div>

      <el-menu
        :default-active="route.path"
        :collapse="collapsed"
        class="sidebar-menu"
        background-color="#1a1a2e"
        text-color="#8a8a9a"
        active-text-color="#409eff"
        :collapse-transition="false"
      >
        <el-menu-item
          v-for="item in navItems"
          :key="item.path"
          :index="item.path"
          @click="navigate(item.path)"
        >
          <el-icon><component :is="item.icon" /></el-icon>
          <template #title>{{ item.label }}</template>
        </el-menu-item>
      </el-menu>

      <div class="sidebar-footer">
        <el-avatar :size="32" style="background: #409eff; cursor: pointer">
          <el-icon><User /></el-icon>
        </el-avatar>
      </div>
    </el-aside>

    <!-- Main content -->
    <el-main class="main-content">
      <!-- 统一页面标题栏(面包屑) -->
      <div class="page-header">
        <span class="page-header-title">{{ route.meta?.title || '' }}</span>
      </div>
      <router-view />
    </el-main>
  </el-container>
</template>

<style scoped>
.app-container {
  height: 100vh;
  overflow: hidden;
}

.sidebar {
  background: #1a1a2e;
  display: flex;
  flex-direction: column;
  transition: width 0.2s;
  overflow: hidden;
}

.sidebar-logo {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: #409eff;
  cursor: pointer;
  border-bottom: 1px solid #2a2a3e;
  flex-shrink: 0;
}

.logo-text {
  font-size: 16px;
  font-weight: 700;
  white-space: nowrap;
}

.sidebar-menu {
  flex: 1;
  border-right: none;
  overflow-y: auto;
  overflow-x: hidden;
}

.sidebar-menu:not(.el-menu--collapse) {
  width: 180px;
}

/* 自定义滚动条:深色背景匹配导航栏,收起/展开都能滚动 */
.sidebar-menu::-webkit-scrollbar {
  width: 6px;
}
.sidebar-menu::-webkit-scrollbar-track {
  background: #1a1a2e; /* 和导航栏背景一致 */
}
.sidebar-menu::-webkit-scrollbar-thumb {
  background: #3a3a4e; /* 比导航栏稍亮的深灰 */
  border-radius: 3px;
}
.sidebar-menu::-webkit-scrollbar-thumb:hover {
  background: #4a4a5e;
}
/* Firefox */
.sidebar-menu {
  scrollbar-width: thin;
  scrollbar-color: #3a3a4e #1a1a2e;
}

.sidebar-footer {
  padding: 12px;
  display: flex;
  justify-content: center;
  border-top: 1px solid #2a2a3e;
  flex-shrink: 0;
}

.main-content {
  background: #f5f7fa;
  overflow-y: auto;
  padding: 24px;
}

/* 页面标题栏 */
.page-header {
  display: flex;
  align-items: center;
  padding: 0 0 12px 0;
  margin-bottom: 8px;
  border-bottom: 1px solid #ebeef5;
}
.page-header-title {
  font-size: 18px;
  font-weight: 700;
  color: #303133;
}
</style>
