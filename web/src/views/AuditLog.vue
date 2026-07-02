<script setup lang="ts">
/**
 * AuditLog.vue — 白盒调用审计日志(独立页)。
 *
 * 左侧:会话列表(每次评测/调试/AI创建一行)
 * 右侧:选中会话的审计时间线(复用 AuditTimeline 组件)
 */
import { ref, onMounted, nextTick } from 'vue'
import api from '../utils/api'
import AuditTimeline from '../components/AuditTimeline.vue'

interface AuditSession {
  task_id: string; task_name: string; status: string
  call_count: number; llm_count: number; tool_count: number; created_at: string
}

const sessions = ref<AuditSession[]>([])
const selectedTaskId = ref<string>('')
const timelineRef = ref<InstanceType<typeof AuditTimeline> | null>(null)

async function loadSessions() {
  try {
    sessions.value = await api.get('/audit/sessions')
    if (sessions.value.length && !selectedTaskId.value) {
      selectSession(sessions.value[0].task_id)
    }
  } catch { sessions.value = [] }
}

function selectSession(taskId: string) {
  selectedTaskId.value = taskId
  nextTick(() => timelineRef.value?.load())
}

function statusTag(status: string) {
  const map: Record<string, string> = {
    done: 'success', running: 'primary', failed: 'danger', blown: 'warning', pending: 'info',
  }
  return map[status] || 'info'
}

onMounted(loadSessions)
</script>

<template>
  <div class="page-title">审计日志 · 白盒调用回放</div>

  <div class="audit-layout">
    <!-- 左侧:会话列表 -->
    <div class="session-sidebar">
      <div class="session-header">
        <span>会话列表</span>
        <el-button text size="small" @click="loadSessions">刷新</el-button>
      </div>
      <div
        v-for="s in sessions"
        :key="s.task_id"
        class="session-item"
        :class="{ active: selectedTaskId === s.task_id }"
        @click="selectSession(s.task_id)"
      >
        <div class="si-title">
          <span class="si-name">{{ s.task_name || s.task_id }}</span>
          <el-tag v-if="s.status" size="small" :type="statusTag(s.status)">{{ s.status }}</el-tag>
        </div>
        <div class="si-meta">
          <span>🤖 {{ s.llm_count }}</span>
          <span>🔧 {{ s.tool_count }}</span>
          <span>共 {{ s.call_count }} 条</span>
          <span class="si-time">{{ s.created_at?.slice(0, 16) }}</span>
        </div>
      </div>
      <el-empty v-if="!sessions.length" description="暂无会话" :image-size="50" />
    </div>

    <!-- 右侧:审计时间线 -->
    <div class="session-content">
      <AuditTimeline v-if="selectedTaskId" ref="timelineRef" :task-id="selectedTaskId" />
      <el-empty v-else description="选择左侧会话查看审计详情" />
    </div>
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 16px; }
.audit-layout { display: flex; gap: 16px; height: calc(100vh - 110px); }
.session-sidebar { width: 280px; border: 1px solid #ebeef5; border-radius: 8px; overflow-y: auto; background: #fafafa; flex-shrink: 0; }
.session-header { display: flex; justify-content: space-between; align-items: center; padding: 12px 14px; font-weight: 600; border-bottom: 1px solid #ebeef5; position: sticky; top: 0; background: #fafafa; z-index: 1; }
.session-item { padding: 10px 14px; cursor: pointer; border-bottom: 1px solid #f0f2f5; transition: background .2s; }
.session-item:hover { background: #f0f2f5; }
.session-item.active { background: #ecf5ff; border-left: 3px solid #409eff; }
.si-title { display: flex; align-items: center; justify-content: space-between; gap: 6px; margin-bottom: 4px; }
.si-name { font-size: 13px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.si-meta { display: flex; gap: 10px; font-size: 11px; color: #909399; }
.si-time { margin-left: auto; }
.session-content { flex: 1; border: 1px solid #ebeef5; border-radius: 8px; padding: 16px; overflow-y: auto; background: #fff; }
</style>
