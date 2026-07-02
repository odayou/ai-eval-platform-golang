<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

const datasets = ref<any[]>([])
const dialog = ref(false)
const form = ref({ task_id: '', format: 'both', scene: '' })

onMounted(load)
async function load() {
  try { datasets.value = await api.get('/finetune/datasets') } catch { datasets.value = [] }
}
async function exportDS() {
  try {
    const res = await api.post('/finetune/export', form.value)
    ElMessage.success(`已导出: ${res.sft_pairs} SFT + ${res.dpo_pairs} DPO`)
    dialog.value = false
    await load()
  } catch (e: any) { ElMessage.error(e.message) }
}
</script>

<template>
  <div class="page">
    <div class="header">
      <h2>🎓 微调数据集</h2>
      <el-button type="primary" @click="dialog = true">从 Bad Case 导出</el-button>
    </div>
    <p class="desc">评测 Bad Case → 归因分析 → 自动生成 SFT/DPO 训练样本 → 导出</p>

    <el-table :data="datasets" style="width: 100%">
      <el-table-column prop="Name" label="数据集" />
      <el-table-column prop="Format" label="格式" width="80" />
      <el-table-column prop="BadCases" label="Bad Cases" width="100" />
      <el-table-column prop="TotalPairs" label="样本对" width="80" />
      <el-table-column prop="Status" label="状态" width="80">
        <template #default="{ row }">
          <el-tag size="small" :type="row.Status === 'exported' ? 'success' : 'info'">{{ row.Status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="CreatedAt" label="创建时间" />
    </el-table>

    <el-dialog v-model="dialog" title="从 Bad Case 导出训练集" width="440px">
      <el-form label-width="90px">
        <el-form-item label="任务 ID"><el-input v-model="form.task_id" placeholder="留空导出全部" /></el-form-item>
        <el-form-item label="场景"><el-input v-model="form.scene" placeholder="留空用全部" /></el-form-item>
        <el-form-item label="格式">
          <el-select v-model="form.format">
            <el-option label="SFT(监督微调)" value="sft" />
            <el-option label="DPO(偏好优化)" value="dpo" />
            <el-option label="两者都要" value="both" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" @click="exportDS">导出</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page { max-width: 900px; margin: 0 auto; padding: 20px; }
.header { display: flex; justify-content: space-between; align-items: center; }
.desc { color: #909399; margin-bottom: 20px; }
</style>
