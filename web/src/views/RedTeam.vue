<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

const scans = ref<any[]>([])
const dialog = ref(false)
const form = ref({ agent_id: '', categories: ['injection', 'leakage', 'jailbreak'] as string[] })
const allCategories = [
  { label: 'Prompt 注入', value: 'injection', desc: '20 个 payload' },
  { label: '数据泄露', value: 'leakage', desc: '20 个 payload' },
  { label: '越狱', value: 'jailbreak', desc: '20 个 payload' },
  { label: '权限绕过', value: 'privilege', desc: '20 个 payload' },
  { label: '指令投毒', value: 'poison', desc: '20 个 payload' },
  { label: '侧信道', value: 'side_channel', desc: '20 个 payload' },
]

// 收集所有轮询定时器,组件卸载时统一清理(防泄漏)
const activeTimers = ref<number[]>([])
onMounted(() => {})
onUnmounted(() => {
  activeTimers.value.forEach((t) => clearInterval(t))
  activeTimers.value = []
})

async function startScan() {
  try {
    const res = await api.post('/redteam/scan', form.value)
    ElMessage.success('安全扫描已启动(6 类×20 payload=120 个攻击测试)')
    scans.value.unshift({ id: res.id, status: 'scanning', agent_id: form.value.agent_id, cvss: null, findings: [] })
    dialog.value = false
    // 轮询刷新状态
    const timer = setInterval(async () => {
      try {
        const detail = await api.get(`/redteam/scans/${res.id}`)
        const idx = scans.value.findIndex(s => s.id === res.id)
        if (idx >= 0) {
          scans.value[idx] = detail
          // 终态(completed 或 failed)停止轮询
          if (detail.status === 'completed' || detail.status === 'failed') {
            clearInterval(timer)
            activeTimers.value = activeTimers.value.filter((t) => t !== timer)
          }
        }
      } catch {
        // 连续失败也停止轮询,避免无限请求
      }
    }, 5000)
    activeTimers.value.push(timer as unknown as number)
  } catch (e: any) { ElMessage.error(e.message) }
}
function cvssColor(cvss: number | null): string {
  if (cvss === null) return '#909399'
  if (cvss >= 7) return '#f56c6c'
  if (cvss >= 4) return '#e6a23c'
  return '#67c23a'
}
function cvssLevel(cvss: number | null): string {
  if (cvss === null) return '待评'
  if (cvss >= 9) return '极危'
  if (cvss >= 7) return '高危'
  if (cvss >= 4) return '中危'
  if (cvss > 0) return '低危'
  return '安全'
}
</script>

<template>
  <div class="page">
    <div class="header">
      <h2>🛡️ 安全红队测试</h2>
      <el-button type="danger" @click="dialog = true">启动安全扫描</el-button>
    </div>
    <p class="desc">6 类安全测试(120 个攻击 payload):注入/泄露/越狱/权限/投毒/侧信道 → CVSS 风险评分 → 修复建议</p>

    <el-card v-for="s in scans" :key="s.id" shadow="hover" style="margin-bottom:16px">
      <div style="display:flex;justify-content:space-between;align-items:center">
        <div>
          <strong>扫描 {{ s.id }}</strong>
          <el-tag :type="s.status === 'completed' ? 'success' : s.status === 'scanning' ? 'warning' : 'danger'" size="small" style="margin-left:8px">
            {{ s.status === 'completed' ? '完成' : s.status === 'scanning' ? '扫描中...' : s.status }}
          </el-tag>
        </div>
        <div v-if="s.CVSS !== undefined && s.CVSS > 0" style="text-align:center">
          <div :style="{color: cvssColor(s.CVSS), fontSize: '32px', fontWeight: 700}">{{ s.CVSS?.toFixed(1) }}</div>
          <div :style="{color: cvssColor(s.CVSS)}">{{ cvssLevel(s.CVSS) }}</div>
        </div>
      </div>
      <div v-if="s.Report" style="margin-top:12px;color:#606266;font-size:13px;white-space:pre-wrap;max-height:400px;overflow-y:auto">
        {{ s.Report.slice(0, 1000) }}
      </div>
    </el-card>

    <el-dialog v-model="dialog" title="启动安全扫描" width="520px">
      <el-alert type="warning" :closable="false" style="margin-bottom:16px">
        将对 Agent 发送 120 个攻击 payload(6 类×20),检测安全漏洞并生成 CVSS 报告。
      </el-alert>
      <el-form label-width="90px">
        <el-form-item label="Agent ID"><el-input v-model="form.agent_id" placeholder="被测 Agent 的 ID" /></el-form-item>
        <el-form-item label="测试类型">
          <el-checkbox-group v-model="form.categories">
            <el-checkbox v-for="c in allCategories" :key="c.value" :label="c.value">
              {{ c.label }} <span style="color:#909399;font-size:12px">({{ c.desc }})</span>
            </el-checkbox>
          </el-checkbox-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="danger" @click="startScan">启动扫描(120 个攻击)</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page { max-width: 900px; margin: 0 auto; padding: 20px; }
.header { display: flex; justify-content: space-between; align-items: center; }
.desc { color: #909399; margin-bottom: 20px; }
</style>
