<script setup lang="ts">
/**
 * Dashboard.vue — 数据看板页(MRD 11.5)。
 *
 * 三区布局:
 *   ① 指标卡片横排(总任务/状态分布/Agent/题库/规则数)— 来自 /api/stats/dashboard
 *   ② 任务状态饼图(ECharts)
 *   ③ 最近任务列表(快速跳转)
 *
 * Pass@1 趋势/帕累托散点需 ②引擎产出数据后补全,现阶段先展示可聚合的部分。
 */
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import api from '../utils/api'
import BaseChart from '../components/BaseChart.vue'

const router = useRouter()
const stats = ref<any>({})
const recentTasks = ref<any[]>([])
const badCases = ref<any[]>([])
const loading = ref(false)
const tasks = ref<any[]>([])

// ── 筛选器 ──
const dateRange = ref<[Date, Date] | null>(null)
const sceneFilter = ref('')
const scenes = [
  { value: '', label: '全部场景' },
  { value: 'agent', label: 'Agent 评测' },
  { value: 'model', label: '大模型评测' },
  { value: 'competitor', label: '竞品报告' },
  { value: 'prd', label: 'PRD 评测' },
  { value: 'product', label: 'AI 产品评测' },
]

// ── 时间范围过滤后的任务列表(dateRange + scene 都生效) ──
const filteredTasks = computed(() => {
  let list = tasks.value.filter((t: any) => t.status === 'done' && t.result)
  if (sceneFilter.value) {
    list = list.filter((t: any) => t.scene === sceneFilter.value)
  }
  if (dateRange.value) {
    const start = new Date(dateRange.value[0]).getTime()
    const end = new Date(dateRange.value[1]).getTime() + 86400000 // 含当天
    list = list.filter((t: any) => {
      const ts = new Date(t.created_at).getTime()
      return ts >= start && ts <= end
    })
  }
  return list
})

// ── 帕累托散点数据(基于 filteredTasks,dateRange+scene 生效) ──
const paretoTasks = computed(() => {
  return filteredTasks.value.map((t: any) => {
    let result = null
    try { result = JSON.parse(t.result) } catch {}
    return {
      name: t.name,
      score: result?.overall || 0,
      cost: result?.breaker?.est_cost_usd || 0,
      tokens: result?.breaker?.total_tokens || 0,
      scene: t.scene,
      created_at: t.created_at,
    }
  })
})

const paretoScatterOption = computed(() => {
  const data = paretoTasks.value
  if (!data.length) return {}
  return {
    tooltip: {
      trigger: 'item',
      formatter: (p: any) => `${p.data[3]}<br/>分数: ${p.data[1]}<br/>费用: $${p.data[0]}<br/>Token: ${p.data[2]}`,
    },
    xAxis: { name: '费用 ($)', type: 'value', scale: true },
    yAxis: { name: '综合分数', type: 'value', min: 0, max: 100 },
    series: [{
      type: 'scatter',
      symbolSize: 16,
      data: data.map((d: any) => [Number((d.cost || 0).toFixed(4)), d.score, d.tokens, d.name]),
      label: { show: true, formatter: (p: any) => p.data[3].slice(0, 12), position: 'right', fontSize: 10 },
      itemStyle: { color: '#409eff' },
    }],
  } as any
})

// 状态中文映射
const statusMap: Record<string, { label: string; color: string }> = {
  pending: { label: '待运行', color: '#909399' },
  running: { label: '运行中', color: '#e6a23c' },
  done: { label: '已完成', color: '#67c23a' },
  failed: { label: '失败', color: '#f56c6c' },
  blown: { label: '已熔断', color: '#f56c6c' },
}

// 指标卡片配置
const cards = computed(() => [
  { label: '总任务数', value: stats.value.total_tasks ?? 0, icon: 'Monitor', color: '#409eff' },
  { label: '平均综合分', value: stats.value.avg_score ?? 0, icon: 'TrendCharts', color: '#67c23a' },
  { label: 'Agent 数', value: stats.value.agent_count ?? 0, icon: 'Cpu', color: '#409eff' },
  { label: '题库数量', value: stats.value.question_count ?? 0, icon: 'Document', color: '#e6a23c' },
  { label: 'Bad Case', value: stats.value.bad_case_total ?? 0, icon: 'Warning', color: '#f56c6c' },
])

// 综合分趋势折线图(基于 filteredTasks,dateRange+scene 生效)
const trendOption = computed(() => {
  // 用过滤后的任务重新计算趋势(原 trend.value 是后端全量数据,dateRange 不生效)
  const data = filteredTasks.value.map((t: any) => {
    let overall = 0
    try { overall = JSON.parse(t.result)?.overall || 0 } catch {}
    return { name: t.name?.slice(0, 12), score: overall }
  }).reverse() // 按时间正序(旧的在左)
  return {
    tooltip: { trigger: 'axis', formatter: (p: any) => `${p[0].name}<br/>综合分: ${p[0].value}` },
    xAxis: {
      type: 'category',
      data: data.map((d: any) => d.name?.slice(0, 10) || ''),
      axisLabel: { rotate: 30, fontSize: 10 },
    },
    yAxis: { type: 'value', min: 0, max: 100, name: '分数' },
    series: [{
      type: 'line',
      data: data.map((d: any) => d.score),
      smooth: true,
      areaStyle: { color: 'rgba(64,158,255,0.2)' },
      lineStyle: { color: '#409eff', width: 2 },
      itemStyle: { color: '#409eff' },
      markLine: {
        silent: true,
        data: [{ yAxis: 60, lineStyle: { color: '#f56c6c', type: 'dashed' }, label: { formatter: '及格线' } }],
      },
    }],
  } as any
})

// 饼图配置:任务状态分布
const pieOption = computed(() => {
  const dist = stats.value.status_distribution || {}
  const data = Object.entries(dist).map(([k, v]) => ({
    name: statusMap[k]?.label || k,
    value: v as number,
    itemStyle: { color: statusMap[k]?.color || '#ccc' },
  }))
  return {
    tooltip: { trigger: 'item' },
    legend: { bottom: 0 },
    series: [{
      type: 'pie',
      radius: ['45%', '70%'],
      avoidLabelOverlap: true,
      label: { show: true, formatter: '{b}\n{c}' },
      data: data.length ? data : [{ name: '暂无数据', value: 1, itemStyle: { color: '#eee' } }],
    }],
  } as any
})

async function load() {
  loading.value = true
  try {
    const [s, ts, bc] = await Promise.all([
      api.get('/stats/dashboard'),
      api.get('/tasks'),
      api.get('/bad-cases', { params: { status: 'pending', limit: '10' } }),
    ])
    stats.value = s
    tasks.value = ts as any[]
    recentTasks.value = (ts as any[]).slice(0, 5)
    badCases.value = bc as any[]
  } finally {
    loading.value = false
  }
}

function gotoTask(id: string) {
  router.push({ path: '/', query: { task: id } })
}

onMounted(load)
</script>

<template>
  <div v-loading="loading">
    <div class="page-header">
      <h2 class="page-title" style="margin:0">数据看板</h2>
      <div class="filter-bar">
        <el-select v-model="sceneFilter" placeholder="场景筛选" size="small" style="width:140px" clearable @change="load">
          <el-option v-for="s in scenes" :key="s.value" :label="s.label" :value="s.value" />
        </el-select>
        <el-date-picker
          v-model="dateRange"
          type="daterange"
          range-separator="至"
          start-placeholder="开始日期"
          end-placeholder="结束日期"
          size="small"
          style="width:240px"
          @change="load"
        />
      </div>
    </div>

    <!-- 第一区:指标卡片 -->
    <el-row :gutter="16" class="cards-row">
      <el-col :span="4" v-for="c in cards" :key="c.label">
        <el-card shadow="hover" class="stat-card">
          <el-icon :size="26" :color="c.color"><component :is="c.icon" /></el-icon>
          <div class="stat-value">{{ c.value }}</div>
          <div class="stat-label">{{ c.label }}</div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 第二区:图表 -->
    <el-row :gutter="16" class="charts-row">
      <el-col :span="10">
        <el-card shadow="never">
          <template #header>任务状态分布</template>
          <BaseChart :option="pieOption" height="280px" />
        </el-card>
      </el-col>
      <el-col :span="14">
        <el-card shadow="never">
          <template #header>最近任务</template>
          <el-empty v-if="!recentTasks.length" description="暂无任务" :image-size="50" />
          <el-table v-else :data="recentTasks" size="small" @row-click="(r:any) => gotoTask(r.id)">
            <el-table-column prop="name" label="任务名" min-width="160" />
            <el-table-column label="状态" width="90">
              <template #default="{ row }">
                <el-tag size="small" :type="row.status === 'done' ? 'success' : row.status === 'running' ? 'warning' : 'info'">
                  {{ statusMap[row.status]?.label || row.status }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="时间" width="150">
              <template #default="{ row }">{{ row.created_at?.slice(0,16).replace('T',' ') }}</template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>

    <!-- 综合分趋势 + 帕累托散点 -->
    <el-row :gutter="16" class="charts-row">
      <el-col :span="8">
        <el-card shadow="never">
          <template #header>综合分趋势(最近 {{ filteredTasks.length }} 次评测)</template>
          <BaseChart v-if="filteredTasks.length" :option="trendOption" height="260px" />
          <el-empty v-else description="当前筛选条件下暂无已完成的评测" :image-size="50" />
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="never">
          <template #header>💰 性价比帕累托散点 ({{ paretoTasks.length }} 任务)</template>
          <BaseChart v-if="paretoTasks.length" :option="paretoScatterOption" height="260px" />
          <el-empty v-else description="暂无已完成评测(需断路器数据)" :image-size="50" />
        </el-card>
      </el-col>
      <el-col :span="8">
        <el-card shadow="never">
          <template #header>⚠ 待处理 Bad Cases ({{ badCases.length }})</template>
          <el-empty v-if="!badCases.length" description="暂无 Bad Case,评测质量良好" :image-size="50" />
          <el-table v-else :data="badCases.slice(0, 5)" size="small" stripe max-height="280">
            <el-table-column label="分数" width="60">
              <template #default="{ row }"><span style="color:#f56c6c;font-weight:700">{{ (row.score || 0).toFixed(0) }}</span></template>
            </el-table-column>
            <el-table-column label="回答摘要" min-width="180">
              <template #default="{ row }">{{ (row.answer || '').slice(0, 80) }}...</template>
            </el-table-column>
            <el-table-column label="类型" width="80">
              <template #default="{ row }">
                <el-tag size="small" :type="row.feedback_type === 'thumbs_down' ? 'danger' : 'warning'">
                  {{ row.feedback_type === 'thumbs_down' ? '人工踩' : '自动标记' }}
                </el-tag>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 16px; }
.cards-row { margin-bottom: 16px; }
.stat-card { text-align: center; }
.stat-value { font-size: 28px; font-weight: 700; color: #1a1a2e; margin: 6px 0 2px; }
.stat-label { font-size: 13px; color: #999; }
.charts-row { margin-bottom: 16px; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.filter-bar { display: flex; gap: 8px; align-items: center; }
</style>
