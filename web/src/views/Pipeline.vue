<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

const pipelines = ref<any[]>([])
const runs = ref<Record<string, any[]>>({})
const dialog = ref(false)
const form = ref({ name: '', agent_id: '', scene: 'agent', min_score: 60, max_cost: 1.0, trigger_type: 'manual' })

onMounted(load)
async function load() {
  try { pipelines.value = await api.get('/pipelines') } catch { pipelines.value = [] }
  for (const p of pipelines.value) {
    try { runs.value[p.ID] = await api.get(`/pipelines/${p.ID}/runs`) } catch {}
  }
}
async function create() {
  try {
    await api.post('/pipelines', form.value)
    ElMessage.success('管线已创建')
    dialog.value = false
    await load()
  } catch (e: any) { ElMessage.error(e.message) }
}
async function trigger(id: string) {
  try {
    await api.post(`/pipelines/${id}/trigger`, {})
    ElMessage.success('管线已触发(异步执行)')
    setTimeout(load, 3000)
  } catch (e: any) { ElMessage.error(e.message) }
}
</script>

<template>
  <div class="page">
    <div class="header">
      <h2>🔄 CI/CD 管线</h2>
      <el-button type="primary" @click="dialog = true">新建管线</el-button>
    </div>
    <p class="desc">持续集成:自动评测 → 三维度评分(正确性/安全/性能) → gate 判定 → 退化检测</p>

    <el-card v-for="p in pipelines" :key="p.ID" shadow="hover" style="margin-bottom:16px">
      <div style="display:flex;justify-content:space-between;align-items:center">
        <div>
          <strong>{{ p.Name }}</strong>
          <el-tag size="small" style="margin-left:8px">{{ p.TriggerType }}</el-tag>
          <el-tag size="small" type="info" style="margin-left:4px">{{ p.Scene }}</el-tag>
        </div>
        <el-button type="primary" size="small" @click="trigger(p.ID)">触发</el-button>
      </div>
      <el-table v-if="runs[p.ID]?.length" :data="runs[p.ID]" size="small" style="margin-top:12px" max-height="200">
        <el-table-column prop="Status" label="状态" width="80">
          <template #default="{ row }">
            <el-tag :type="row.Status === 'passed' ? 'success' : row.Status === 'failed' ? 'danger' : 'warning'" size="small">
              {{ row.Status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="ScoreC" label="正确性" width="70" />
        <el-table-column prop="ScoreS" label="安全" width="60" />
        <el-table-column prop="ScoreP" label="性能" width="60" />
        <el-table-column prop="Cost" label="成本$" width="70" />
        <el-table-column prop="StartedAt" label="时间" />
      </el-table>
    </el-card>

    <el-dialog v-model="dialog" title="新建 CI/CD 管线" width="480px">
      <el-form label-width="100px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="Agent ID"><el-input v-model="form.agent_id" /></el-form-item>
        <el-form-item label="场景"><el-input v-model="form.scene" /></el-form-item>
        <el-form-item label="最低均分"><el-input-number v-model="form.min_score" :min="0" :max="100" /></el-form-item>
        <el-form-item label="成本上限$"><el-input-number v-model="form.max_cost" :min="0" :step="0.1" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" @click="create">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page { max-width: 900px; margin: 0 auto; padding: 20px; }
.header { display: flex; justify-content: space-between; align-items: center; }
.desc { color: #909399; margin-bottom: 20px; }
</style>
