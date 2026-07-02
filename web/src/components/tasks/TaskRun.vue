<script setup lang="ts">
/**
 * TaskRun.vue — 评测"运行"Tab(展示进度 + 对话轨迹回放)。
 *
 * 内容:
 *   1. 当前状态徽标(待运行/运行中/已完成/失败)
 *   2. 进度概览(题数、耗时、token)
 *   3. 对话轨迹:每轮 user 提问 + agent 回答(来自 conversations 表)
 *
 * 实时性:
 *   - WebSocket 实时推送(优先),引擎逐题发射 question_start/agent_response/question_done/task_done
 *   - 回退轮询(WebSocket 不可用时,status=running 每 1.5s 刷新)
 */
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useTaskStore } from '../../stores/taskStore'
import api from '../../utils/api'
import TrajectoryGraph from './TrajectoryGraph.vue'
import XtermTerminal from './XtermTerminal.vue'

const store = useTaskStore()

interface Message { role: string; content: string }
interface Conversation {
  id: string; turn: number; messages: Message[]
  token_in: number; token_out: number; status: string
}
interface Report { total_questions?: number; mock_mode?: boolean }

const conversations = ref<Conversation[]>([])
const loading = ref(false)
const viewMode = ref<'timeline' | 'graph' | 'terminal'>('timeline')
const report = ref<Report | null>(null)
let pollTimer: number | null = null
let ws: WebSocket | null = null
const wsConnected = ref(false)

const task = () => store.selectedTask

// 解析报告 JSON
function parseReport(): Report | null {
  if (!task()?.result) return null
  try { return JSON.parse(task()!.result) } catch { return null }
}

// 加载对话轨迹
async function loadConversations() {
  if (!task()?.id) return
  loading.value = true
  try {
    conversations.value = await api.get(`/tasks/${task()!.id}/conversations`)
  } finally {
    loading.value = false
  }
}

// 完整刷新(任务状态 + 对话)
async function refresh() {
  await store.loadTasks()       // 刷新任务状态(status 可能已变化)
  report.value = parseReport()
  await loadConversations()
}

// WebSocket 连接(实时推送,fallback 到轮询)
function connectWS() {
  if (!task()?.id) return
  const tid = task()!.id
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  const url = `${proto}//${location.host}/api/tasks/${tid}/ws`
  
  try {
    ws = new WebSocket(url)
    ws.onopen = () => { wsConnected.value = true; stopPolling() } // WS 通了,停轮询
    ws.onmessage = (e) => {
      const ev = JSON.parse(e.data)
      switch (ev.type) {
        case 'question_start':
        case 'agent_response':
        case 'question_done':
          // 进度事件:刷新对话列表
          refresh()
          break
        case 'task_done':
          // 任务完成:最终刷新 + 断开 WS
          refresh()
          ws?.close()
          break
      }
    }
    ws.onerror = () => { wsConnected.value = false; startPolling() } // WS 失败,回退轮询
    ws.onclose = () => { wsConnected.value = false }
  } catch {
    wsConnected.value = false
    startPolling() // WS 不支持,回退轮询
  }
}

function disconnectWS() {
  if (ws) { ws.close(); ws = null }
  wsConnected.value = false
}

// 启动/停止轮询(WebSocket 不可用时的回退)
function startPolling() {
  stopPolling()
  if (task()?.status === 'running' || task()?.status === 'pending') {
    pollTimer = window.setInterval(refresh, 1500)
  }
}
function stopPolling() {
  if (pollTimer !== null) { clearInterval(pollTimer); pollTimer = null }
}

// 选中任务变化时:断开旧 WS、刷新数据、连接新 WS
watch(() => store.selectedId, async () => {
  disconnectWS()
  stopPolling()
  await refresh()
  if (task()?.status === 'running' || task()?.status === 'pending') {
    connectWS()
  }
})

// Token 总量(供进度概览展示)
const totalTokens = computed(() =>
  conversations.value.reduce((s, c) => s + c.token_in + c.token_out, 0),
)

// 逐题进度:总题数(报告里有)× 完成数(conversations 数量)
// 每题状态:done(有对话记录)/ running(任务进行中且是下一题)/ pending
const totalQuestions = computed(() => report.value?.total_questions ?? 0)
const progressSteps = computed(() => {
  const total = totalQuestions.value
  const done = conversations.value.length
  const isRunning = task()?.status === 'running'
  const steps: { index: number; status: 'done' | 'running' | 'pending' }[] = []
  for (let i = 0; i < total; i++) {
    let status: 'done' | 'running' | 'pending' = 'pending'
    if (i < done) {
      status = 'done'
    } else if (i === done && isRunning) {
      status = 'running'
    }
    steps.push({ index: i + 1, status })
  }
  return steps
})

// 进度百分比
const progressPercent = computed(() => {
  const total = totalQuestions.value
  if (total === 0) return 0
  return Math.round((conversations.value.length / total) * 100)
})

onMounted(async () => {
  await refresh()
  if (task()?.status === 'running' || task()?.status === 'pending') {
    connectWS()
  }
})
onBeforeUnmount(() => { disconnectWS(); stopPolling() })

// 状态相关
const statusMeta: Record<string, { label: string; type: string; icon: string }> = {
  pending: { label: '待运行', type: 'info', icon: 'Clock' },
  running: { label: '运行中', type: 'warning', icon: 'Loading' },
  done: { label: '已完成', type: 'success', icon: 'CircleCheck' },
  failed: { label: '失败', type: 'danger', icon: 'CircleClose' },
  blown: { label: '已熔断', type: 'danger', icon: 'WarnTriangleFilled' },
}
function meta() {
  return statusMeta[task()?.status || 'pending'] || statusMeta.pending
}
</script>

<template>
  <div v-if="!task()">
    <el-empty description="未选中任务" :image-size="60" />
  </div>

  <div v-else class="run-view">
    <!-- 状态条 -->
    <div class="status-bar">
      <el-tag :type="meta().type as any" size="large" effect="dark">
        {{ meta().label }}
      </el-tag>
      <span v-if="task()?.status === 'running' && wsConnected" class="polling-hint" style="color:#67c23a">
        ⚡ WebSocket 实时
      </span>
      <span v-else-if="task()?.status === 'running'" class="polling-hint">
        <el-icon class="is-loading"><Loading /></el-icon>
        实时刷新中...
      </span>
      <el-tag v-if="report?.mock_mode" type="warning" size="small">Mock 模式(未配置真实 Key)</el-tag>
      <el-button size="small" @click="refresh" :loading="loading">刷新</el-button>
    </div>

    <!-- 逐题进度条 -->
    <div v-if="totalQuestions > 0" class="question-progress">
      <div class="qp-header">
        <span>评测进度 {{ conversations.length }}/{{ totalQuestions }}</span>
        <el-progress :percentage="progressPercent" :stroke-width="8" :show-text="false" style="flex:1;margin:0 12px" />
        <span>{{ progressPercent }}%</span>
      </div>
      <div class="qp-steps">
        <div
          v-for="step in progressSteps"
          :key="step.index"
          :class="['qp-step', step.status]"
          :title="`第 ${step.index} 题: ${step.status === 'done' ? '已完成' : step.status === 'running' ? '进行中' : '等待中'}`"
        >
          <span class="qp-num">{{ step.index }}</span>
        </div>
      </div>
    </div>

    <!-- 进度概览 -->
    <div class="progress-grid" v-if="report">
      <div class="prog-item">
        <div class="prog-num">{{ report.total_questions ?? 0 }}</div>
        <div class="prog-lbl">评测题数</div>
      </div>
      <div class="prog-item">
        <div class="prog-num">{{ conversations.length }}</div>
        <div class="prog-lbl">对话轮次</div>
      </div>
      <div class="prog-item">
        <div class="prog-num">{{ totalTokens }}</div>
        <div class="prog-lbl">Token 总量</div>
      </div>
    </div>

    <!-- 对话轨迹 / 拓扑图(切换) -->
    <div class="trajectory-header">
      <h4 class="section-title">对话轨迹</h4>
      <el-radio-group v-model="viewMode" size="small">
        <el-radio-button value="timeline">时间线</el-radio-button>
        <el-radio-button value="graph">拓扑图</el-radio-button>
        <el-radio-button value="terminal">终端</el-radio-button>
      </el-radio-group>
    </div>
    <!-- 时间线视图 -->
    <template v-if="viewMode === 'timeline'">
      <el-empty v-if="!conversations.length" description="暂无对话记录(任务运行后产生)" :image-size="60" />
      <el-timeline v-else>
        <el-timeline-item
          v-for="conv in conversations"
          :key="conv.id"
          :timestamp="`第 ${conv.turn} 轮 · in ${conv.token_in} / out ${conv.token_out}`"
          placement="top"
          type="primary"
        >
          <div class="msg msg-user" v-for="(m, i) in conv.messages" :key="i">
            <strong>{{ m.role === 'user' ? '📝 出题' : '🤖 Agent' }}</strong>
            <pre>{{ m.content }}</pre>
        </div>
      </el-timeline-item>
    </el-timeline>
    </template>

    <!-- 拓扑图视图 -->
    <template v-else-if="viewMode === 'graph'">
      <el-empty v-if="!conversations.length" description="暂无对话记录" :image-size="60" />
      <TrajectoryGraph v-else :conversations="conversations" />
    </template>

    <!-- 终端视图 -->
    <template v-else>
      <XtermTerminal :conversations="conversations" />
    </template>
  </div>
</template>

<style scoped>
.run-view { padding: 4px 0; }
.status-bar { display: flex; align-items: center; gap: 12px; margin-bottom: 16px; }
.polling-hint { display: flex; align-items: center; gap: 4px; color: #e6a23c; font-size: 13px; }
.progress-grid { display: flex; gap: 24px; margin-bottom: 20px; padding: 16px; background: #f5f7fa; border-radius: 8px; }

/* 逐题进度 */
.question-progress { margin-bottom: 20px; padding: 16px; background: #fff; border: 1px solid #ebeef5; border-radius: 8px; }
.qp-header { display: flex; align-items: center; gap: 8px; font-size: 13px; color: #606266; margin-bottom: 12px; }
.qp-steps { display: flex; gap: 6px; flex-wrap: wrap; }
.qp-step {
  width: 32px; height: 32px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  font-size: 13px; font-weight: 600; transition: all 0.2s;
}
.qp-step.done { background: #67c23a; color: #fff; }
.qp-step.running { background: #e6a23c; color: #fff; animation: pulse 1.2s infinite; }
.qp-step.pending { background: #f5f7fa; color: #c0c4cc; border: 1px dashed #dcdfe6; }
@keyframes pulse {
  0%, 100% { transform: scale(1); }
  50% { transform: scale(1.12); }
}
.prog-item { text-align: center; }
.prog-num { font-size: 24px; font-weight: 700; color: #409eff; }
.prog-lbl { font-size: 12px; color: #999; margin-top: 2px; }
.section-title { margin: 16px 0 12px; font-size: 15px; color: #303133; }
.msg { margin-bottom: 8px; }
.msg strong { font-size: 13px; color: #606266; }
.msg pre {
  margin: 4px 0 12px; padding: 10px; background: #fafafa;
  border-radius: 6px; font-size: 13px; white-space: pre-wrap;
  max-height: 200px; overflow-y: auto; border-left: 3px solid #dcdfe6;
}
.msg-user pre { border-left-color: #409eff; }
</style>
