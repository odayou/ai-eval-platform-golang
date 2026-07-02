<script setup lang="ts">
// TaskAttribution.vue — 归因报告(任务详情"归因"子 Tab)。
//
// 展示该任务的异常归因分析(Bad Case 根因 + 修正建议)。
// 数据来自 GET /api/tasks/:id/attributions,由引擎自动归因(④.3)写入。
import { ref, watch, computed } from 'vue'
import { useTaskStore } from '../../stores/taskStore'
import api from '../../utils/api'
import BaseChart from '../BaseChart.vue'

interface Attribution {
  id: string
  task_id: string
  root_cause: string
  analysis: string
  suggested_fix: string
  applied: boolean
  created_at: string
}

const store = useTaskStore()
const attributions = ref<Attribution[]>([])
const loading = ref(false)

// 根因中文映射
const causeMap: Record<string, { label: string; color: string }> = {
  prompt_compliance: { label: '指令遵循度', color: '#e6a23c' },
  context_loss: { label: '上下文丢失', color: '#909399' },
  tool_failure: { label: '工具调用失败', color: '#f56c6c' },
  model_limit: { label: '模型能力上限', color: '#409eff' },
}

async function load() {
  const task = store.selectedTask
  if (!task) return
  loading.value = true
  try {
    attributions.value = await api.get(`/tasks/${task.id}/attributions`)
  } finally {
    loading.value = false
  }
}

// 选中任务变化时重新加载
watch(() => store.selectedId, load, { immediate: true })

// 根因分布饼图(多个 Bad Case 时看根因类型分布)
const pieOption = computed(() => {
  const counts: Record<string, number> = {}
  for (const a of attributions.value) {
    counts[a.root_cause] = (counts[a.root_cause] || 0) + 1
  }
  const data = Object.entries(counts).map(([k, v]) => ({
    name: causeMap[k]?.label || k,
    value: v,
    itemStyle: { color: causeMap[k]?.color || '#909399' },
  }))
  return {
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0 },
    series: [{
      type: 'pie',
      radius: ['40%', '65%'],
      label: { formatter: '{b}\n{c}' },
      data: data.length ? data : [{ name: '暂无', value: 1, itemStyle: { color: '#eee' } }],
    }],
  } as any
})
</script>

<template>
  <div v-loading="loading">
    <el-empty
      v-if="!attributions.length"
      description="暂无归因报告(Bad Case 产生后,引擎会自动分析根因)"
      :image-size="70"
    >
      <template #description>
        <p>暂无归因报告</p>
        <p class="hint">当评测产生 Bad Case(得分&lt;60)时,引擎会自动分析根因并给出修正建议</p>
      </template>
    </el-empty>

    <div v-else>
      <!-- 根因分布饼图 -->
      <div v-if="attributions.length > 1" class="pie-wrap">
        <BaseChart :option="pieOption" height="220px" />
      </div>

      <div class="attr-list">
      <el-card
        v-for="a in attributions"
        :key="a.id"
        shadow="never"
        class="attr-card"
      >
        <div class="attr-header">
          <el-tag
            :color="causeMap[a.root_cause]?.color"
            effect="dark"
            size="small"
          >
            {{ causeMap[a.root_cause]?.label || a.root_cause }}
          </el-tag>
          <el-tag v-if="a.applied" type="success" size="small">已采纳</el-tag>
          <span class="attr-time">{{ a.created_at?.slice(0, 16).replace('T', ' ') }}</span>
        </div>

        <div class="attr-section">
          <span class="sec-label">📊 根因分析</span>
          <p class="sec-text">{{ a.analysis }}</p>
        </div>

        <div v-if="a.suggested_fix" class="attr-section fix">
          <span class="sec-label">💡 修正建议</span>
          <p class="sec-text">{{ a.suggested_fix }}</p>
        </div>
      </el-card>
      </div>
    </div>
  </div>
</template>

<style scoped>
.hint { color: #999; font-size: 12px; }
.pie-wrap { background: #fff; border-radius: 8px; padding: 12px; margin-bottom: 16px; border: 1px solid #ebeef5; }
.attr-list { display: flex; flex-direction: column; gap: 12px; }
.attr-card { border-left: 3px solid #409eff; }
.attr-header { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; }
.attr-time { margin-left: auto; font-size: 12px; color: #999; }
.attr-section { margin-bottom: 8px; }
.attr-section.fix { background: #f0f9eb; padding: 8px; border-radius: 4px; border-left: 3px solid #67c23a; }
.sec-label { font-weight: 600; font-size: 13px; color: #606266; display: block; margin-bottom: 4px; }
.sec-text { margin: 0; font-size: 13px; color: #303133; line-height: 1.6; }
</style>
