<script setup lang="ts">
/**
 * Leaderboard.vue — 多 Agent 综合评测排行榜 (MRD §12.1 MVP)
 *
 * 功能:
 *   ① 按场景筛选
 *   ② 展示排名/Agent/模型/综合分/维度雷达图/通过率
 *   ③ 排名变化标记
 */
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import api from '../utils/api'
import BaseChart from '../components/BaseChart.vue'

const router = useRouter()
const loading = ref(false)
const scene = ref('agent')
const entries = ref<any[]>([])

const scenes = [
  { value: 'agent', label: 'Agent 评测' },
  { value: 'model', label: '大模型评测' },
  { value: 'competitor', label: '竞品报告' },
  { value: 'prd', label: 'PRD 评测' },
  { value: 'product', label: 'AI 产品评测' },
]

async function load() {
  loading.value = true
  try {
    const res = await api.get('/stats/leaderboard', { params: { scene: scene.value } })
    entries.value = res.entries || []
  } catch (e: any) {
    entries.value = []
  } finally {
    loading.value = false
  }
}

// 前3名的奖牌
function medal(rank: number): string {
  return rank === 1 ? '🥇' : rank === 2 ? '🥈' : rank === 3 ? '🥉' : ''
}

// 分数颜色
function scoreColor(s: number): string {
  if (s >= 85) return '#67c23a'
  if (s >= 70) return '#409eff'
  if (s >= 60) return '#e6a23c'
  return '#f56c6c'
}

// 维度中文
const dimLabel = (k: string): string => ({
  security: '安全', performance: '性能', architecture: '架构',
  correctness: '正确性', product: '产品',
} as Record<string, string>)[k] || k

// 所有维度(取第一个有数据的)
const allDims = computed(() => {
  if (!entries.value.length) return []
  const dims = new Set<string>()
  for (const e of entries.value) {
    if (e.dimensions) Object.keys(e.dimensions).forEach(d => dims.add(d))
  }
  return [...dims]
})

// top-5 雷达叠加
const top5RadarOption = computed(() => {
  const top5 = entries.value.slice(0, 5)
  const dims = allDims.value
  if (!dims.length || !top5.length) return {}
  const colors = ['#409eff', '#67c23a', '#e6a23c', '#f56c6c', '#9c27b0']
  return {
    tooltip: {},
    legend: { bottom: 0, data: top5.map(e => e.agent_name) },
    radar: {
      indicator: dims.map(d => ({ name: dimLabel(d), max: 100 })),
      shape: 'polygon',
      splitArea: { areaStyle: { color: ['#fafafa', '#f0f2f5'] } },
    },
    series: [{
      type: 'radar',
      data: top5.map((e, i) => ({
        value: dims.map(d => e.dimensions?.[d] ?? 0),
        name: e.agent_name,
        areaStyle: { color: colors[i], opacity: 0.15 },
        lineStyle: { color: colors[i] },
      })),
    }],
  } as any
})

onMounted(load)
</script>

<template>
  <div v-loading="loading">
    <div class="page-header">
      <h2 class="page-title">🏆 Agent 综合评测排行榜</h2>
      <div class="filter-bar">
        <el-select v-model="scene" @change="load" style="width:160px" size="small">
          <el-option v-for="s in scenes" :key="s.value" :label="s.label" :value="s.value" />
        </el-select>
        <el-button size="small" @click="load" :loading="loading">刷新</el-button>
      </div>
    </div>

    <!-- Top-5 雷达叠加对比 -->
    <el-card v-if="top5RadarOption && Object.keys(top5RadarOption).length" shadow="never" class="chart-card">
      <template #header>Top 5 维度对比雷达图</template>
      <BaseChart :option="top5RadarOption" height="320px" />
    </el-card>

    <!-- 排行榜表格 -->
    <el-card shadow="never">
      <template #header>
        <span>排名 ({{ entries.length }})</span>
      </template>
      <el-empty v-if="!entries.length" description="暂无已完成的评测任务(引擎运行后产生)" :image-size="60" />
      <el-table v-else :data="entries" stripe size="small">
        <el-table-column label="排名" width="80">
          <template #default="{ row }">
            <span class="rank-num" :style="{ color: scoreColor(row.overall) }">
              {{ medal(row.rank) }} {{ row.rank }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="Agent" min-width="160">
          <template #default="{ row }">
            <strong>{{ row.agent_name }}</strong>
            <el-tag size="small" type="info" style="margin-left:6px">{{ row.model }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="综合分" width="100">
          <template #default="{ row }">
            <span :style="{ color: scoreColor(row.overall), fontWeight: 700, fontSize: '16px' }">
              {{ row.overall?.toFixed(1) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="Bad Case" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.bad_cases" type="danger" size="small">{{ row.bad_cases }}</el-tag>
            <span v-else style="color:#999">0</span>
          </template>
        </el-table-column>
        <el-table-column label="题数" width="60" prop="total_questions" />
        <!-- 维度列 -->
        <el-table-column v-for="dim in allDims" :key="dim" :label="dimLabel(dim)" width="80">
          <template #default="{ row }">
            <span :style="{ color: scoreColor(row.dimensions?.[dim] || 0) }">
              {{ row.dimensions?.[dim] ?? '-' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="评测时间" width="160">
          <template #default="{ row }">{{ row.completed_at?.slice(0, 19).replace('T', ' ') }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100">
          <template #default>
            <el-button size="small" text type="primary" @click="router.push('/')">查看</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.page-title { font-size: 20px; color: #1a1a2e; margin: 0; }
.filter-bar { display: flex; gap: 8px; align-items: center; }
.chart-card { margin-bottom: 16px; }
.rank-num { font-size: 15px; font-weight: 700; }
</style>
