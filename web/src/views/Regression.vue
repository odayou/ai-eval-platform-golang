<script setup lang="ts">
// Regression.vue — 版本演进性能 Diff 与回归看板(MRD 9.4)。
//
// 功能:
//   ① 基于已有 task 快速对比(两个已跑完的任务)
//   ② Agent 级回归对比(基线 vs 目标模型,自动跑)
//   ③ 结果展示:综合分 diff + 维度红绿变化 + 逐题 pass/fail 变化表
//   ④ 回归漏洞高亮(原通过→现失败) + 改进标记(原失败→现通过)
import { ref, computed } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'
import BaseChart from '../components/BaseChart.vue'

// 字典
const tasks = ref<any[]>([])
const agents = ref<any[]>([])
const models = ref<any[]>([])
const ruleSets = ref<any[]>([])

// ── 模式切换 ──
type Mode = 'tasks' | 'agents'
const mode = ref<Mode>('tasks')

// ── 模式 1: 基于 task ──
const taskForm = ref({ base_task_id: '', target_task_id: '' })

// ── 模式 2: 基于 Agent ──
const agentForm = ref({
  base_agent_id: '',
  target_models: [] as string[],
  rule_set_id: 'rs-fullstack',
  question_count: 5,
  scene: 'agent',
})

const loading = ref(false)
const result = ref<any>(null)
const agentResults = ref<any[]>([])

// ── 便捷 computed ──
const overallColor = computed(() => {
  const d = result.value?.overall_diff ?? 0
  if (d > 1) return '#67c23a'
  if (d < -1) return '#f56c6c'
  return '#909399'
})

const overallIcon = computed(() => {
  const d = result.value?.overall_diff ?? 0
  if (d > 1) return '↑'
  if (d < -1) return '↓'
  return '→'
})

// 维度 bar 图配置
const dimBarOption = computed(() => {
  const diffs = result.value?.dimension_diffs || []
  if (!diffs.length) return {}
  const labels = diffs.map((d: any) => d.label)
  const deltas = diffs.map((d: any) => d.delta)
  return {
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    grid: { left: 80, right: 30, top: 10, bottom: 20 },
    xAxis: {
      type: 'value',
      name: '分数变化',
      axisLabel: { formatter: (v: number) => (v >= 0 ? '+' : '') + v.toFixed(1) },
    },
    yAxis: { type: 'category', data: labels.reverse(), axisLabel: { fontSize: 12 } },
    series: [{
      type: 'bar',
      data: [...deltas].reverse().map((v: number) => ({
        value: v,
        itemStyle: { color: v >= 0 ? '#67c23a' : '#f56c6c' },
      })),
      barMaxWidth: 28,
    }],
  } as any
})

// ── 加载字典 ──
async function loadDict() {
  const [t, a, m, r] = await Promise.all([
    api.get('/tasks'),
    api.get('/agents'),
    api.get('/models'),
    api.get('/rule-sets'),
  ])
  // 只显示已完成的任务
  tasks.value = (t || []).filter((x: any) => x.status === 'done')
  agents.value = a
  models.value = m
  ruleSets.value = r
}

// ── 模式 1: Task 对比 ──
async function runTaskCompare() {
  if (!taskForm.value.base_task_id || !taskForm.value.target_task_id) {
    ElMessage.warning('请选择两个已完成的任务')
    return
  }
  loading.value = true
  result.value = null
  try {
    result.value = await api.post('/regression/compare', taskForm.value)
    ElMessage.success('回归对比完成')
  } catch (e: any) {
    ElMessage.error(e.message || '对比失败')
  } finally {
    loading.value = false
  }
}

// ── 模式 2: Agent 对比 ──
async function runAgentCompare() {
  if (!agentForm.value.base_agent_id) {
    ElMessage.warning('请选择基线 Agent')
    return
  }
  if (!agentForm.value.target_models.length) {
    ElMessage.warning('请至少选择一个目标模型')
    return
  }
  loading.value = true
  agentResults.value = []
  try {
    const res = await api.post('/regression/agent-compare', agentForm.value)
    agentResults.value = res.results || []
    ElMessage.success(`完成 ${agentResults.value.length} 组对比`)
  } catch (e: any) {
    ElMessage.error(e.message || '对比失败')
  } finally {
    loading.value = false
  }
}

// ── 格式化 ──
const diffSign = (v: number) => (v >= 0 ? '+' : '') + (v ?? 0).toFixed(1)
const statusTag = (s: string) => {
  const map: Record<string, { type: string; label: string }> = {
    'pass→pass':  { type: 'info',    label: '→ 稳定通过' },
    'fail→fail':  { type: 'warning', label: '→ 仍然失败' },
    'pass→fail':  { type: 'danger',  label: '⚠ 回归漏洞' },
    'fail→pass':  { type: 'success', label: '✅ 新修复' },
  }
  return map[s] || { type: 'info', label: s }
}

// 初始化
loadDict()
</script>

<template>
  <div>
    <h2 class="page-title">版本演进回归看板 (Git Diff 风格)</h2>

    <!-- 模式切换 -->
    <el-radio-group v-model="mode" class="mode-switch">
      <el-radio-button value="tasks">基于已有任务对比</el-radio-button>
      <el-radio-button value="agents">Agent 回归对比(自动跑)</el-radio-button>
    </el-radio-group>

    <!-- ═══ 模式 1: Task 对比 ═══ -->
    <el-card v-if="mode === 'tasks'" shadow="never" class="config-card">
      <el-form label-width="100px" inline>
        <el-form-item label="基线任务(V1)">
          <el-select v-model="taskForm.base_task_id" filterable placeholder="选已完成的任务" style="width: 300px">
            <el-option
              v-for="t in tasks"
              :key="t.id"
              :label="`${t.name} (${t.scene}, ${t.status})`"
              :value="t.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="对比任务(V2)">
          <el-select v-model="taskForm.target_task_id" filterable placeholder="选已完成的任务" style="width: 300px">
            <el-option
              v-for="t in tasks"
              :key="t.id"
              :label="`${t.name} (${t.scene}, ${t.status})`"
              :value="t.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" @click="runTaskCompare">
            <el-icon><TrendCharts /></el-icon>&nbsp;运行回归对比
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- ═══ 模式 2: Agent 对比 ═══ -->
    <el-card v-else shadow="never" class="config-card">
      <el-form label-width="100px" inline>
        <el-form-item label="基线 Agent">
          <el-select v-model="agentForm.base_agent_id" filterable placeholder="选基线 Agent" style="width: 200px">
            <el-option v-for="a in agents" :key="a.id" :label="`${a.name} (${a.model})`" :value="a.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="目标模型">
          <el-select v-model="agentForm.target_models" multiple filterable placeholder="与哪些模型对比" style="width: 280px">
            <el-option v-for="m in models" :key="m.id" :label="m.name" :value="m.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="场景">
          <el-select v-model="agentForm.scene" style="width: 120px">
            <el-option label="Agent" value="agent" />
            <el-option label="大模型" value="model" />
            <el-option label="竞品报告" value="competitor" />
            <el-option label="PRD" value="prd" />
            <el-option label="AI产品" value="product" />
          </el-select>
        </el-form-item>
        <el-form-item label="题数">
          <el-input-number v-model="agentForm.question_count" :min="1" :max="20" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" @click="runAgentCompare">
            <el-icon><TrendCharts /></el-icon>&nbsp;运行回归对比
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- ═══ 结果区(单结果) ═══ -->
    <div v-if="result" v-loading="loading">
      <!-- 总结卡 -->
      <el-alert :title="result.summary" type="info" show-icon :closable="false" class="reco-alert" />

      <!-- 核心指标 diff 卡 -->
      <el-row :gutter="16" class="conclusion-row">
        <el-col :span="6">
          <el-card shadow="hover" class="conclusion-card">
            <div class="c-label">综合分变化</div>
            <div class="c-value" :style="{ color: overallColor }">
              {{ overallIcon }} {{ diffSign(result.overall_diff) }}
            </div>
            <div class="c-hint">
              {{ result.base_label }} ({{ result.base_task?.overall }}) →
              {{ result.target_label }} ({{ result.target_task?.overall }})
            </div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="conclusion-card">
            <div class="c-label">回归漏洞</div>
            <div class="c-value" :style="{ color: result.regression_count > 0 ? '#f56c6c' : '#67c23a' }">
              {{ result.regression_count }}
            </div>
            <div class="c-hint">原通过→现失败</div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="conclusion-card">
            <div class="c-label">新修复</div>
            <div class="c-value" :style="{ color: result.improvement_count > 0 ? '#67c23a' : '#909399' }">
              {{ result.improvement_count }}
            </div>
            <div class="c-hint">原失败→现通过</div>
          </el-card>
        </el-col>
        <el-col :span="6">
          <el-card shadow="hover" class="conclusion-card">
            <div class="c-label">总题数</div>
            <div class="c-value">{{ result.question_diffs?.length || 0 }}</div>
            <div class="c-hint">参与对比</div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 维度变化图 + 表 -->
      <el-row :gutter="16" class="chart-row">
        <el-col :span="12">
          <el-card shadow="never">
            <template #header>维度分数变化(绿↑提升 / 红↓劣化)</template>
            <BaseChart :option="dimBarOption" height="320px" />
          </el-card>
        </el-col>
        <el-col :span="12">
          <el-card shadow="never">
            <template #header>维度明细</template>
            <el-table :data="result.dimension_diffs" stripe size="small" max-height="300">
              <el-table-column label="维度">
                <template #default="{ row }">{{ row.label }}</template>
              </el-table-column>
              <el-table-column label="基线" width="70">
                <template #default="{ row }">{{ row.base_score?.toFixed(1) ?? '-' }}</template>
              </el-table-column>
              <el-table-column label="目标" width="70">
                <template #default="{ row }">{{ row.target_score?.toFixed(1) ?? '-' }}</template>
              </el-table-column>
              <el-table-column label="变化" width="90">
                <template #default="{ row }">
                  <span :style="{ color: row.delta > 0 ? '#67c23a' : row.delta < 0 ? '#f56c6c' : '#909399', fontWeight: 700 }">
                    {{ row.direction === 'up' ? '↑' : row.direction === 'down' ? '↓' : '→' }}
                    {{ diffSign(row.delta) }}
                  </span>
                </template>
              </el-table-column>
            </el-table>
          </el-card>
        </el-col>
      </el-row>

      <!-- 逐题变化表 -->
      <el-card shadow="never" class="detail-card">
        <template #header>
          逐题变化明细
          <el-tag v-if="result.regression_count > 0" type="danger" size="small" class="header-tag">
            {{ result.regression_count }} 个回归漏洞
          </el-tag>
          <el-tag v-if="result.improvement_count > 0" type="success" size="small" class="header-tag">
            {{ result.improvement_count }} 个改进
          </el-tag>
        </template>
        <el-table :data="result.question_diffs" stripe size="small" max-height="500">
          <el-table-column type="index" width="50" />
          <el-table-column prop="title" label="题目" min-width="200" show-overflow-tooltip />
          <el-table-column label="基线分" width="80">
            <template #default="{ row }">{{ row.base_score?.toFixed(1) ?? '-' }}</template>
          </el-table-column>
          <el-table-column label="目标分" width="80">
            <template #default="{ row }">{{ row.target_score?.toFixed(1) ?? '-' }}</template>
          </el-table-column>
          <el-table-column label="变化" width="80">
            <template #default="{ row }">
              <span :style="{ color: row.delta > 0 ? '#67c23a' : row.delta < 0 ? '#f56c6c' : '#909399', fontWeight: 700 }">
                {{ diffSign(row.delta) }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="状态变化" width="130">
            <template #default="{ row }">
              <el-tag :type="statusTag(row.status_change).type" size="small" effect="dark">
                {{ statusTag(row.status_change).label }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="difficulty" label="难度" width="70">
            <template #default="{ row }">
              <el-tag v-if="row.difficulty" size="small" type="info">{{ row.difficulty }}</el-tag>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <!-- 沙箱 + 费用对比 -->
      <el-row :gutter="16">
        <el-col :span="12">
          <el-card v-if="result.base_task?.sandbox" shadow="never" class="detail-card">
            <template #header>沙箱指标对比</template>
            <el-descriptions :column="1" size="small" border>
              <el-descriptions-item label="编译成功率">
                {{ result.base_task.sandbox.pass_rate }}% →
                <span :style="{ color: (result.target_task?.sandbox?.pass_rate ?? 0) >= (result.base_task.sandbox.pass_rate ?? 0) ? '#67c23a' : '#f56c6c' }">
                  {{ result.target_task?.sandbox?.pass_rate ?? '-' }}%
                </span>
              </el-descriptions-item>
              <el-descriptions-item label="Lint 错误数">
                {{ result.base_task.sandbox.lint_total }} →
                <span :style="{ color: (result.target_task?.sandbox?.lint_total ?? 999) <= (result.base_task.sandbox.lint_total ?? 0) ? '#67c23a' : '#f56c6c' }">
                  {{ result.target_task?.sandbox?.lint_total ?? '-' }}
                </span>
              </el-descriptions-item>
            </el-descriptions>
          </el-card>
        </el-col>
        <el-col :span="12">
          <el-card v-if="result.base_task?.breaker" shadow="never" class="detail-card">
            <template #header>费用对比(断路器)</template>
            <el-descriptions :column="1" size="small" border>
              <el-descriptions-item label="Token 消耗">
                {{ result.base_task.breaker.total_tokens }} →
                {{ result.target_task?.breaker?.total_tokens ?? '-' }}
              </el-descriptions-item>
              <el-descriptions-item label="估算费用">
                ${{ result.base_task.breaker.est_cost_usd }} →
                ${{ result.target_task?.breaker?.est_cost_usd ?? '-' }}
              </el-descriptions-item>
            </el-descriptions>
          </el-card>
        </el-col>
      </el-row>
    </div>

    <!-- ═══ 结果区(Agent 多结果) ═══ -->
    <div v-if="agentResults.length" v-loading="loading">
      <el-card
        v-for="(r, i) in agentResults"
        :key="i"
        shadow="never"
        class="detail-card agent-result-card"
      >
        <template #header>
          <span class="agent-result-title">
            对比 #{{ Number(i) + 1 }}: {{ r.base_label }} → {{ r.target_label }}
          </span>
        </template>
        <el-alert :title="r.summary" type="info" show-icon :closable="false" class="reco-alert" />

        <el-row :gutter="16">
          <el-col :span="8">
            <div class="mini-stat">
              <div class="mini-label">综合分变化</div>
              <div class="mini-value" :style="{ color: r.overall_diff > 0 ? '#67c23a' : r.overall_diff < 0 ? '#f56c6c' : '#909399' }">
                {{ diffSign(r.overall_diff) }}
              </div>
            </div>
          </el-col>
          <el-col :span="8">
            <div class="mini-stat">
              <div class="mini-label">⚠ 回归漏洞</div>
              <div class="mini-value" :style="{ color: r.regression_count > 0 ? '#f56c6c' : '#67c23a' }">{{ r.regression_count }}</div>
            </div>
          </el-col>
          <el-col :span="8">
            <div class="mini-stat">
              <div class="mini-label">✅ 新修复</div>
              <div class="mini-value" :style="{ color: r.improvement_count > 0 ? '#67c23a' : '#909399' }">{{ r.improvement_count }}</div>
            </div>
          </el-col>
        </el-row>
      </el-card>
    </div>

    <!-- 空状态 -->
    <el-empty
      v-if="!loading && !result && !agentResults.length"
      description="选择基线版本和目标版本,运行回归对比,像 Git Diff 一样查看指标变化"
      :image-size="100"
    />
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 16px; }
.mode-switch { margin-bottom: 16px; }
.config-card { margin-bottom: 16px; }
.reco-alert { margin-bottom: 16px; }
.conclusion-row { margin-bottom: 16px; }
.conclusion-card { text-align: center; }
.c-label { font-size: 13px; color: #909399; }
.c-value { font-size: 28px; font-weight: 700; color: #409eff; margin: 6px 0 2px; }
.c-hint { font-size: 12px; color: #c0c4cc; }
.chart-row { margin-bottom: 16px; }
.detail-card { margin-bottom: 16px; }
.header-tag { margin-left: 8px; }
.agent-result-card { border-left: 3px solid var(--el-color-primary); }
.agent-result-title { font-weight: 600; font-size: 15px; }
.mini-stat { text-align: center; padding: 16px; background: #fafafa; border-radius: 8px; margin-bottom: 8px; }
.mini-label { font-size: 13px; color: #909399; margin-bottom: 4px; }
.mini-value { font-size: 24px; font-weight: 700; }
</style>
