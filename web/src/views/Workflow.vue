<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

const workflows = ref<any[]>([])
const dialog = ref(false)
const dagText = ref(`{
  "nodes": [
    {"id": "research", "agent_id": "agent-xxx", "prompt": "调研这个技术方案的可行性", "depends_on": []},
    {"id": "implement", "agent_id": "agent-yyy", "prompt": "根据调研结果实现代码", "depends_on": ["research"]},
    {"id": "review", "agent_id": "agent-zzz", "prompt": "审查代码质量", "depends_on": ["implement"]}
  ]
}`)
const form = ref({ name: '', description: '' })

onMounted(load)
async function load() {
  try { workflows.value = await api.get('/workflows') } catch { workflows.value = [] }
}
async function create() {
  try {
    await api.post('/workflows', {
      name: form.value.name,
      description: form.value.description,
      dag: JSON.parse(dagText.value),
    })
    ElMessage.success('工作流已创建')
    dialog.value = false
    await load()
  } catch (e: any) { ElMessage.error(e.message) }
}
async function run(id: string) {
  try {
    const res = await api.post(`/workflows/${id}/run`, {})
    ElMessage.success(`工作流已启动(${res.nodes} 个节点)`)
  } catch (e: any) { ElMessage.error(e.message) }
}
</script>

<template>
  <div class="page">
    <div class="header">
      <h2>🔀 多 Agent 编排</h2>
      <el-button type="primary" @click="dialog = true">新建工作流</el-button>
    </div>
    <p class="desc">DAG 工作流:多 Agent 协作 → 独立 harness → 中间结果传递 → 汇总</p>

    <el-card v-for="w in workflows" :key="w.ID" shadow="hover" style="margin-bottom:12px">
      <div style="display:flex;justify-content:space-between;align-items:center">
        <div>
          <strong>{{ w.Name }}</strong>
          <el-tag size="small" :type="w.Status === 'active' ? 'success' : 'info'" style="margin-left:8px">{{ w.Status }}</el-tag>
          <div v-if="w.Desc" style="color:#909399;font-size:13px;margin-top:4px">{{ w.Desc }}</div>
        </div>
        <el-button type="primary" size="small" @click="run(w.ID)">运行</el-button>
      </div>
    </el-card>

    <el-dialog v-model="dialog" title="新建工作流" width="640px">
      <el-form label-width="80px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="form.description" /></el-form-item>
        <el-form-item label="DAG 定义">
          <el-input v-model="dagText" type="textarea" :rows="12" style="font-family:monospace;font-size:12px" />
        </el-form-item>
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
