<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

const leaderboard = ref<any[]>([])
const dialog = ref(false)
const form = ref({ agent_id: '', developer: '', scene: 'agent' })

onMounted(load)
async function load() {
  try { leaderboard.value = await api.get('/arena/leaderboard') } catch { leaderboard.value = [] }
}
async function submit() {
  try {
    await api.post('/arena/submit', form.value)
    ElMessage.success('已提交,正在评测...')
    dialog.value = false
    setTimeout(load, 5000)
  } catch (e: any) { ElMessage.error(e.message) }
}
</script>

<template>
  <div class="page">
    <div class="header">
      <h2>🏆 Agent 对战排行榜</h2>
      <el-button type="primary" @click="dialog = true">提交 Agent</el-button>
    </div>
    <p class="desc">多维度评分(正确性/安全/性能/成本) + ELO 评分 + 对战排名</p>

    <el-table :data="leaderboard" style="width: 100%">
      <el-table-column type="index" label="排名" width="60" />
      <el-table-column prop="Name" label="Agent" />
      <el-table-column prop="Dev" label="开发者" width="120" />
      <el-table-column prop="Elo" label="ELO" width="80" sortable />
      <el-table-column label="战绩" width="120">
        <template #default="{ row }">{{ row.Won }}胜 {{ row.Lost }}负</template>
      </el-table-column>
      <el-table-column prop="Corr" label="正确性" width="70" />
      <el-table-column prop="Sec" label="安全" width="60" />
      <el-table-column prop="Perf" label="性能" width="60" />
      <el-table-column prop="Cost" label="成本$" width="70" />
    </el-table>

    <el-dialog v-model="dialog" title="提交到对战" width="420px">
      <el-form label-width="90px">
        <el-form-item label="Agent ID"><el-input v-model="form.agent_id" /></el-form-item>
        <el-form-item label="开发者"><el-input v-model="form.developer" /></el-form-item>
        <el-form-item label="场景"><el-input v-model="form.scene" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" @click="submit">提交</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page { max-width: 900px; margin: 0 auto; padding: 20px; }
.header { display: flex; justify-content: space-between; align-items: center; }
.desc { color: #909399; margin-bottom: 20px; }
</style>
