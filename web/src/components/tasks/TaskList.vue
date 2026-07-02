<script setup lang="ts">
/**
 * TaskList.vue — 任务列表(评测任务页左栏)。
 *
 * 职责单一:展示当前场景下的任务卡片,响应点击/新建/删除。
 * 数据来自 taskStore,本组件不直接发请求(除轮询刷新)。
 *
 * 轮询:列表里存在 running/pending 任务时,每 1.5s 刷新一次,
 * 让状态徽标随引擎进度实时变化(②阶段用轮询,⑤阶段换 WebSocket)。
 */
import { computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useTaskStore, taskOverall } from '../../stores/taskStore'
import { ElMessageBox, ElMessage } from 'element-plus'
import api from '../../utils/api'

const store = useTaskStore()

// 是否存在进行中的任务(决定是否开启轮询)
const hasActive = computed(() =>
  store.filteredTasks.some((t) => t.status === 'running' || t.status === 'pending'),
)

let pollTimer: number | null = null
async function pollAndCheck() {
  // 先记录当前运行中的任务
  const wasRunning = store.filteredTasks.filter(
    (t) => t.status === 'running' || t.status === 'pending',
  )
  await store.loadTasks()
  // 检查哪些任务刚完成
  for (const t of wasRunning) {
    const updated = store.tasks.find((x) => x.id === t.id)
    if (updated && (updated.status === 'done' || updated.status === 'failed' || updated.status === 'blown')) {
      const score = taskOverall(updated)
      if (updated.status === 'done') {
        ElMessage({
          message: `✅ 评测完成！「${updated.name}」综合分 ${score ?? '?'}，结果已自动填入报告`,
          type: 'success',
          duration: 6000,
        })
        // 自动选中该任务,展示详情
        store.selectTask(updated.id)
      } else if (updated.status === 'failed') {
        ElMessage.error(`❌ 评测失败：「${updated.name}」`)
      } else {
        ElMessage.warning(`⚠️ 评测熔断：「${updated.name}」(预算/Token 超限)`)
      }
    }
  }
}

function ensurePolling() {
  // 有进行中任务 → 启动轮询;否则停止
  if (hasActive.value && pollTimer === null) {
    pollTimer = window.setInterval(pollAndCheck, 1500)
  } else if (!hasActive.value && pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}
onMounted(ensurePolling)
onBeforeUnmount(() => { if (pollTimer !== null) clearInterval(pollTimer) })
// hasActive 变化时调整轮询
watch(hasActive, ensurePolling)

// 状态 → el-tag 类型映射
function tagType(s: string): string {
  return { pending: 'info', running: 'warning', done: 'success', failed: 'danger', blown: 'danger' }[s] || 'info'
}
function statusLabel(s: string): string {
  return { pending: '待运行', running: '运行中', done: '已完成', failed: '失败', blown: '已熔断' }[s] || s
}

// 综合分颜色等级
function scoreClass(score: number): string {
  if (score >= 80) return 'score-high'
  if (score >= 60) return 'score-mid'
  return 'score-low'
}

// 提取任务的维度分数摘要(从 result JSON 解析)
function taskDims(t: any): { name: string; score: string; cls: string }[] {
  if (!t.result) return []
  try {
    const r = JSON.parse(t.result)
    const dims = r.dimensions || {}
    const map: Record<string, string> = {
      security: '安全', performance: '性能', architecture: '架构',
      product: '产品', correctness: '正确性',
    }
    const result: { name: string; score: string; cls: string }[] = []
    for (const [key, val] of Object.entries(dims)) {
      const score = typeof val === 'object' ? (val as any)?.score : val
      if (typeof score === 'number') {
        result.push({
          name: map[key] || key,
          score: score.toFixed(0),
          cls: score >= 80 ? 'dim-high' : score >= 60 ? 'dim-mid' : 'dim-low',
        })
      }
    }
    return result
  } catch {
    return []
  }
}

// 触发删除(带确认)
async function onDelete(id: string, name: string) {
  await ElMessageBox.confirm(`确定删除任务"${name}"?`, '删除任务', { type: 'warning' })
  await store.deleteTask(id)
}

// 重跑/复测:复制原任务配置创建新任务并立即入队
async function onRerun(id: string, name: string) {
  try {
    await api.post(`/tasks/${id}/rerun`)
    ElMessage.success(`已创建复测任务"${name} (重跑)",正在执行...`)
    await store.loadTasks()
  } catch (e: any) {
    ElMessage.error(e.message || '复测失败')
  }
}

defineEmits<{ (e: 'create'): void }>()
</script>

<template>
  <el-card shadow="never" class="task-list-card">
    <template #header>
      <div class="header">
        <span>任务列表 ({{ store.filteredTasks.length }})</span>
        <el-button type="primary" size="small" @click="$emit('create')">
          <el-icon><Plus /></el-icon>&nbsp;新建
        </el-button>
      </div>
    </template>

    <el-skeleton v-if="store.loading" :rows="4" animated />

    <el-empty v-else-if="!store.filteredTasks.length" description="该场景暂无任务，点击上方按钮创建第一个评测" :image-size="60">
      <el-button type="primary" @click="$emit('create')">+ 新建评测任务</el-button>
    </el-empty>

    <div v-else class="task-items">
      <div
        v-for="t in store.filteredTasks"
        :key="t.id"
        :class="['task-item', { active: t.id === store.selectedId }]"
        @click="store.selectTask(t.id)"
      >
        <div class="row1">
          <span class="name">{{ t.name }}</span>
          <div class="row1-tags">
            <span v-if="taskOverall(t) !== null" class="score" :class="scoreClass(taskOverall(t)!)">{{ taskOverall(t) }}</span>
            <el-tag v-if="t.chaos_enabled" type="warning" size="small" effect="dark">🌪</el-tag>
            <el-tag :type="tagType(t.status)" size="small" effect="light">{{ statusLabel(t.status) }}</el-tag>
          </div>
        </div>
        <div class="row2">
          <span>{{ t.created_at?.slice(0, 16).replace('T', ' ') }}</span>
          <el-button
            class="rerun-btn"
            type="primary"
            text
            size="small"
            @click.stop="onRerun(t.id, t.name)"
          >重跑</el-button>
          <el-button
            class="del-btn"
            type="danger"
            text
            size="small"
            @click.stop="onDelete(t.id, t.name)"
          >删除</el-button>
        </div>
        <!-- 已完成任务的维度分数摘要(底部历史生成信息) -->
        <div v-if="t.status === 'done' && taskDims(t).length" class="row3-dims">
          <span v-for="d in taskDims(t)" :key="d.name" class="dim-chip" :class="d.cls">
            {{ d.name }} {{ d.score }}
          </span>
        </div>
      </div>
    </div>
  </el-card>
</template>

<style scoped>
.task-list-card { min-height: 500px; }
.header { display: flex; justify-content: space-between; align-items: center; }
.task-items { display: flex; flex-direction: column; gap: 4px; }
.task-item {
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s;
}
.task-item:hover { background: #f5f7fa; }
.task-item.active { background: #ecf5ff; }
.row1 { display: flex; justify-content: space-between; align-items: center; margin-bottom: 4px; }
.row1-tags { display: flex; align-items: center; gap: 4px; }
.row3-dims { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 6px; }
.dim-chip {
  font-size: 11px; padding: 1px 6px; border-radius: 8px; font-weight: 600;
}
.dim-high { background: #f0f9eb; color: #67c23a; }
.dim-mid { background: #ecf5ff; color: #409eff; }
.dim-low { background: #fef0f0; color: #f56c6c; }
.name { font-weight: 500; font-size: 14px; }
.score { font-size: 15px; font-weight: 700; min-width: 32px; text-align: right; }
.score-high { color: #67c23a; }
.score-mid { color: #e6a23c; }
.score-low { color: #f56c6c; }
.row2 { display: flex; justify-content: space-between; align-items: center; color: #999; font-size: 12px; }
.del-btn { opacity: 0; transition: opacity 0.15s; }
.task-item:hover .del-btn { opacity: 1; }
</style>
