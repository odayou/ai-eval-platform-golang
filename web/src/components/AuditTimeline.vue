<script setup lang="ts">
/**
 * AuditTimeline.vue — 白盒审计时间线(可复用组件)。
 *
 * 接收一组审计日志,按 seq 渲染时间线,每条可展开看完整输入/输出/思考/调用细节。
 * 三类事件用不同图标/颜色区分:🤖 LLM / 🔧 工具 / 📡 事件。
 */
import { ref, computed } from 'vue'
import api from '../utils/api'

interface AuditLog {
  id: string; task_id: string; turn: number; kind: string; seq: number
  caller: string; provider: string; model: string; base_url: string; api_format: string
  system_prompt: string; input_text: string; output_text: string; reasoning: string
  tool_name: string; token_in: number; token_out: number; latency_ms: number
  http_status: number; attempt: number; is_mock: boolean; is_error: boolean
  error_text: string; created_at: string
}

const props = defineProps<{ taskId: string }>()

const logs = ref<AuditLog[]>([])
const loading = ref(false)
const filterKind = ref('') // '' | llm | tool | event
const filterCaller = ref('')
const expandedId = ref<string>('')
const expandedReasoning = ref<Set<string>>(new Set())
const renderLimit = ref(200)

function toggleExpandReasoning(id: string) {
  if (expandedReasoning.value.has(id)) {
    expandedReasoning.value.delete(id)
  } else {
    expandedReasoning.value.add(id)
  }
  expandedReasoning.value = new Set(expandedReasoning.value) // 触发响应式
} // 虚拟截断:默认只渲染前 200 条,防大列表卡顿

const filtered = computed(() => {
  let r = logs.value
  if (filterKind.value) r = r.filter((l) => l.kind === filterKind.value)
  if (filterCaller.value) r = r.filter((l) => l.caller.includes(filterCaller.value))
  return r
})

// 实际渲染的记录(截断到 renderLimit,避免 DOM 过多卡顿)
const rendered = computed(() => filtered.value.slice(0, renderLimit.value))

const callerOptions = computed(() => {
  const s = new Set<string>()
  logs.value.forEach((l) => { if (l.caller) s.add(l.caller) })
  return Array.from(s).sort()
})

const stats = computed(() => ({
  total: logs.value.length,
  llm: logs.value.filter((l) => l.kind === 'llm').length,
  tool: logs.value.filter((l) => l.kind === 'tool').length,
  event: logs.value.filter((l) => l.kind === 'event').length,
  error: logs.value.filter((l) => l.is_error).length,
  mock: logs.value.filter((l) => l.is_mock).length,
}))

async function load() {
  loading.value = true
  try {
    logs.value = await api.get(`/tasks/${props.taskId}/audit`)
  } catch { logs.value = [] }
  finally { loading.value = false }
}

function toggle(id: string) {
  expandedId.value = expandedId.value === id ? '' : id
}

function kindMeta(kind: string) {
  switch (kind) {
    case 'llm': return { icon: '🤖', label: 'LLM', color: '#409eff', bg: '#ecf5ff' }
    case 'tool': return { icon: '🔧', label: '工具', color: '#e6a23c', bg: '#fdf6ec' }
    case 'event': return { icon: '📡', label: '事件', color: '#67c23a', bg: '#f0f9eb' }
    default: return { icon: '•', label: kind, color: '#909399', bg: '#f4f4f5' }
  }
}

function callerLabel(caller: string) {
  const map: Record<string, string> = {
    agent: '被测Agent', judge: '判官', router: '路由',
    compaction: '压缩', guardian: '守卫', probe: '探活', hook: '钩子',
    terminal: '终端', testgen: '出题', question_gen: '出题',
    roundtable_leader: '圆桌主导',
  }
  if (map[caller]) return map[caller]
  if (caller.startsWith('expert_')) return caller.replace('expert_', '') + '专家'
  if (caller.startsWith('agent_tools')) return 'Agent+工具'
  if (caller.startsWith('event_')) return caller.replace('event_', '')
  return caller
}

function preview(text: string, n = 120) {
  if (!text) return ''
  const r = [...text]
  return r.length > n ? r.slice(0, n).join('') + '...' : text
}

defineExpose({ load })
load()
</script>

<template>
  <div class="audit-timeline">
    <!-- 统计 + 过滤 -->
    <div class="audit-toolbar">
      <div class="audit-stats">
        <span class="stat-chip" :class="{ active: filterKind === '' }" @click="filterKind = ''">全部 {{ stats.total }}</span>
        <span class="stat-chip llm" :class="{ active: filterKind === 'llm' }" @click="filterKind = 'llm'">🤖 {{ stats.llm }}</span>
        <span class="stat-chip tool" :class="{ active: filterKind === 'tool' }" @click="filterKind = 'tool'">🔧 {{ stats.tool }}</span>
        <span class="stat-chip event" :class="{ active: filterKind === 'event' }" @click="filterKind = 'event'">📡 {{ stats.event }}</span>
        <span v-if="stats.error" class="stat-chip err">❌ 错误 {{ stats.error }}</span>
        <span v-if="stats.mock" class="stat-chip mock">🎭 mock {{ stats.mock }}</span>
      </div>
      <el-select v-model="filterCaller" placeholder="按调用者筛选" clearable size="small" style="width:200px">
        <el-option v-for="c in callerOptions" :key="c" :label="callerLabel(c)" :value="c" />
      </el-select>
    </div>

    <el-empty v-if="!filtered.length && !loading" description="暂无审计记录(跑一次评测任务后才有)" :image-size="60" />

    <div class="timeline" v-loading="loading">
      <div
        v-for="log in rendered"
        :key="log.id"
        class="tl-item"
        :class="{ expanded: expandedId === log.id }"
      >
        <div class="tl-head" @click="toggle(log.id)">
          <span class="tl-seq">#{{ log.seq }}</span>
          <span class="tl-icon" :style="{ background: kindMeta(log.kind).bg, color: kindMeta(log.kind).color }">
            {{ kindMeta(log.kind).icon }}
          </span>
          <span class="tl-caller">
            <el-tag size="small" :color="kindMeta(log.kind).bg" effect="plain">{{ callerLabel(log.caller) }}</el-tag>
          </span>
          <span v-if="log.turn" class="tl-turn">题{{ log.turn }}</span>
          <span v-if="log.model" class="tl-model">{{ log.model }}</span>
          <span class="tl-preview">{{ preview(log.kind === 'tool' ? log.tool_name : log.output_text || log.error_text || log.input_text) }}</span>
          <span class="tl-meta">
            <span v-if="log.reasoning" class="badge reasoning-badge">🧠{{ log.reasoning.length }}</span>
            <span v-if="log.latency_ms" class="badge">{{ log.latency_ms }}ms</span>
            <span v-if="log.token_in || log.token_out" class="badge">↑{{ log.token_in }} ↓{{ log.token_out }}</span>
            <span v-if="log.is_mock" class="badge mock">mock</span>
            <span v-if="log.is_error" class="badge err">错误</span>
          </span>
        </div>

        <!-- 展开详情 -->
        <div v-if="expandedId === log.id" class="tl-detail">
          <!-- LLM 调用详情 -->
          <template v-if="log.kind === 'llm'">
            <div class="detail-row" v-if="log.model">
              <span class="dl">模型</span>
              <span>{{ log.model }} <el-tag size="small" type="info" effect="plain">{{ log.provider }} / {{ log.api_format || '自动' }}</el-tag></span>
            </div>
            <div class="detail-row" v-if="log.base_url">
              <span class="dl">端点</span><span class="mono">{{ log.base_url }}</span>
            </div>
            <div class="detail-block" v-if="log.system_prompt">
              <div class="db-title">System Prompt</div>
              <pre>{{ log.system_prompt }}</pre>
            </div>
            <div class="detail-block">
              <div class="db-title">输入 (Prompt)</div>
              <pre>{{ log.input_text || '(空)' }}</pre>
            </div>
            <div class="detail-block" v-if="log.reasoning">
              <div class="db-title reasoning">
                🧠 思考过程 ({{ log.reasoning.length }} 字符)
                <span v-if="log.reasoning.length > 200" class="expand-hint" @click="toggleExpandReasoning(log.id)">
                  {{ expandedReasoning.has(log.id) ? '收起' : '展开全部' }}
                </span>
              </div>
              <pre
                class="reasoning-block"
                :class="{ collapsed: log.reasoning.length > 200 && !expandedReasoning.has(log.id) }"
                @click="toggleExpandReasoning(log.id)"
              >{{ log.reasoning }}</pre>
            </div>
            <div class="detail-block">
              <div class="db-title" :class="{ err: log.is_error }">输出 {{ log.is_error ? '(失败)' : '' }}</div>
              <pre v-if="log.output_text">{{ log.output_text }}</pre>
              <pre v-if="log.is_error" class="err-text">{{ log.error_text }}</pre>
              <el-empty v-if="!log.output_text && !log.is_error" description="(无输出)" :image-size="40" />
            </div>
          </template>

          <!-- 工具调用详情 -->
          <template v-else-if="log.kind === 'tool'">
            <div class="detail-row">
              <span class="dl">工具</span><span>{{ log.tool_name }}</span>
            </div>
            <div class="detail-block">
              <div class="db-title">入参</div>
              <pre>{{ log.input_text || '(空)' }}</pre>
            </div>
            <div class="detail-block">
              <div class="db-title" :class="{ err: log.is_error }">出参 {{ log.is_error ? '(失败)' : '' }}</div>
              <pre v-if="log.output_text">{{ log.output_text }}</pre>
              <pre v-if="log.is_error" class="err-text">{{ log.error_text }}</pre>
            </div>
          </template>

          <!-- 事件详情 -->
          <template v-else>
            <div class="detail-block">
              <div class="db-title">事件数据</div>
              <pre>{{ log.input_text }}</pre>
            </div>
          </template>
        </div>
      </div>
      <!-- 加载更多(过滤后总数 > 已渲染数) -->
      <div v-if="filtered.length > rendered.length" class="load-more" @click="renderLimit += 200">
        还有 {{ filtered.length - rendered.length }} 条,点击加载更多...
      </div>
    </div>
  </div>
</template>

<style scoped>
.audit-timeline { }
.load-more { text-align: center; padding: 12px; color: #409eff; cursor: pointer; font-size: 13px; border: 1px dashed #c6e2ff; border-radius: 6px; margin-top: 8px; }
.load-more:hover { background: #ecf5ff; }
.audit-toolbar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; flex-wrap: wrap; gap: 8px; }
.audit-stats { display: flex; gap: 6px; flex-wrap: wrap; }
.stat-chip { padding: 3px 10px; border-radius: 12px; font-size: 12px; cursor: pointer; background: #f4f4f5; color: #606266; transition: all .2s; }
.stat-chip:hover { opacity: .8; }
.stat-chip.active { background: #409eff; color: #fff; }
.stat-chip.llm.active { background: #409eff; }
.stat-chip.tool.active { background: #e6a23c; }
.stat-chip.event.active { background: #67c23a; }
.stat-chip.err { background: #fef0f0; color: #f56c6c; }
.stat-chip.mock { background: #f4f4f5; color: #909399; }
.timeline { max-height: calc(100vh - 220px); overflow-y: auto; }
.tl-item { border: 1px solid #ebeef5; border-radius: 6px; margin-bottom: 6px; overflow: hidden; }
.tl-item.expanded { border-color: #c6e2ff; }
.tl-head { display: flex; align-items: center; gap: 8px; padding: 8px 12px; cursor: pointer; font-size: 13px; }
.tl-head:hover { background: #f5f7fa; }
.tl-seq { color: #c0c4cc; font-size: 11px; min-width: 32px; }
.tl-icon { width: 24px; height: 24px; border-radius: 6px; display: flex; align-items: center; justify-content: center; font-size: 13px; flex-shrink: 0; }
.tl-caller { flex-shrink: 0; }
.tl-turn { font-size: 11px; color: #909399; background: #f4f4f5; padding: 1px 6px; border-radius: 4px; flex-shrink: 0; }
.tl-model { font-size: 11px; color: #409eff; flex-shrink: 0; max-width: 140px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tl-preview { flex: 1; color: #606266; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; }
.tl-meta { display: flex; gap: 4px; flex-shrink: 0; }
.badge { font-size: 10px; padding: 1px 5px; border-radius: 3px; background: #f4f4f5; color: #909399; }
.badge.mock { background: #fdf6ec; color: #e6a23c; }
.badge.err { background: #fef0f0; color: #f56c6c; }
.badge.reasoning-badge { background: #f3e8ff; color: #7c3aed; font-weight: 600; }
.tl-detail { padding: 0 12px 12px; border-top: 1px dashed #ebeef5; }
.detail-row { display: flex; gap: 12px; padding: 6px 0; font-size: 13px; border-bottom: 1px solid #fafafa; }
.dl { color: #909399; min-width: 60px; }
.mono { font-family: monospace; font-size: 12px; }
.detail-block { margin-top: 10px; }
.db-title { font-size: 12px; color: #909399; margin-bottom: 4px; font-weight: 600; }
.db-title.reasoning { color: #722ed1; }
.db-title.err { color: #f56c6c; }
pre { background: #1e1e2e; color: #e0e0e0; padding: 10px; border-radius: 4px; font-size: 12px; line-height: 1.5; white-space: pre-wrap; word-break: break-word; max-height: 400px; overflow-y: auto; margin: 0; }
pre.reasoning-block { background: #2d1b4e; color: #d3adf7; border-left: 3px solid #722ed1; }
pre.reasoning-block.collapsed { max-height: 120px; cursor: pointer; }
.expand-hint { float: right; font-size: 12px; color: #7c3aed; cursor: pointer; font-weight: normal; }
.expand-hint:hover { text-decoration: underline; }
pre.err-text { background: #2d1414; color: #ffa39e; }
</style>
