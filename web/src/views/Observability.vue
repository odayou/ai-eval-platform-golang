<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import api from '../utils/api'

const stats = ref<any>({})
const alerts = ref<any[]>([])
const selectedAgent = ref('')
let refreshTimer: any = null

onMounted(() => {
  load()
  refreshTimer = setInterval(load, 30000)
})
onUnmounted(() => { if (refreshTimer) clearInterval(refreshTimer) })

async function load() {
  try {
    const res = await api.get('/observe/dashboard', { params: { agent_id: selectedAgent.value } })
    stats.value = res.stats || {}
    alerts.value = res.alerts || []
  } catch {
    // 加载失败静默(保留上一次数据),30s 后自动重试
  }
}
</script>

<template>
  <div class="page">
    <h2>📊 Agent 生产监控</h2>
    <p class="desc">实时指标:token/成本/延迟/错误率 + 异常检测 + 告警(30s 自动刷新)</p>

    <el-row :gutter="16" style="margin-bottom:20px">
      <el-col :span="5">
        <el-card shadow="hover"><div class="metric"><div class="num">{{ stats.TotalTokens?.toLocaleString() || 0 }}</div><div class="label">总 Token</div></div></el-card>
      </el-col>
      <el-col :span="5">
        <el-card shadow="hover"><div class="metric"><div class="num">${{ stats.TotalCost?.toFixed(4) || '0.0000' }}</div><div class="label">总成本</div></div></el-card>
      </el-col>
      <el-col :span="5">
        <el-card shadow="hover"><div class="metric"><div class="num">{{ stats.AvgLatency?.toFixed(0) || 0 }}ms</div><div class="label">平均延迟</div></div></el-card>
      </el-col>
      <el-col :span="5">
        <el-card shadow="hover"><div class="metric"><div class="num" :style="{color: (stats.TotalErrors||0) > 0 ? '#f56c6c' : '#67c23a'}">{{ stats.TotalErrors || 0 }}</div><div class="label">错误数</div></div></el-card>
      </el-col>
      <el-col :span="4">
        <el-card shadow="hover"><div class="metric"><div class="num">{{ stats.TotalEvents || 0 }}</div><div class="label">事件数</div></div></el-card>
      </el-col>
    </el-row>

    <el-card shadow="never">
      <template #header>
        <div style="display:flex;justify-content:space-between;align-items:center">
          <span>🔔 告警事件</span>
          <el-tag v-if="alerts.length" type="danger" size="small">{{ alerts.length }} 条告警</el-tag>
        </div>
      </template>
      <el-table :data="alerts" size="small" style="width:100%">
        <el-table-column label="级别" width="80">
          <template #default="{ row }">
            <el-tag size="small" :type="row.Message?.includes('critical') ? 'danger' : 'warning'">
              {{ row.Message?.includes('critical') ? '严重' : '警告' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="Message" label="告警内容" show-overflow-tooltip />
        <el-table-column prop="Value" label="实际值" width="100" />
        <el-table-column prop="Threshold" label="阈值" width="80" />
        <el-table-column prop="TriggeredAt" label="时间" width="180" />
      </el-table>
      <el-empty v-if="!alerts.length" description="暂无告警 ✓" :image-size="50" />
    </el-card>
  </div>
</template>

<style scoped>
.page { max-width: 900px; margin: 0 auto; padding: 20px; }
.desc { color: #909399; margin-bottom: 20px; }
.metric { text-align: center; }
.num { font-size: 24px; font-weight: 700; color: #409eff; }
.label { font-size: 13px; color: #909399; margin-top: 4px; }
</style>
