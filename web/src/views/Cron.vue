<script setup lang="ts">
// Cron.vue — 定时任务管理页(MRD 10.4 / ⑤ cron)。
//
// 功能:
//   ① 顶部:调度器运行统计(worker 队列/运行/完成数)
//   ② 定时任务列表(名称/Cron 表达式/Agent/规则集/状态/下次执行)
//   ③ 创建定时任务对话框
//   ④ 启用/暂停/删除
//
// Cron 表达式(5 字段:分 时 日 月 周)示例:
//   "0 2 * * *"    每天 02:00
//   "*/30 * * * *" 每 30 分钟
//   "0 */2 * * *"  每 2 小时
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../utils/api'

interface CronJob {
  id: string
  name: string
  expr: string
  agent_id: string
  rule_set_id: string
  scene: string
  enabled: boolean
  last_run: string
  next_run: string
  last_status: string
  run_count: number
}

interface SchedulerStats {
  queued: number
  running: number
  completed: number
  failed: number
  retried: number
}

const jobs = ref<CronJob[]>([])
const stats = ref<SchedulerStats>({ queued: 0, running: 0, completed: 0, failed: 0, retried: 0 })
const loading = ref(false)
const createVisible = ref(false)

// 创建表单
const form = ref({ name: '', expr: '0 2 * * *', agent_id: '', rule_set_id: '', scene: 'agent' })
// 字典(Agent/规则集下拉)
const agents = ref<any[]>([])
const ruleSets = ref<any[]>([])

async function loadJobs() {
  loading.value = true
  try {
    jobs.value = await api.get('/cron-jobs')
  } finally {
    loading.value = false
  }
}

async function loadStats() {
  stats.value = await api.get('/scheduler/stats')
}

async function loadDict() {
  const [a, r] = await Promise.all([api.get('/agents'), api.get('/rule-sets')])
  agents.value = a
  ruleSets.value = r
}

async function submitCreate() {
  if (!form.value.name.trim() || !form.value.expr.trim()) {
    ElMessage.warning('请填写任务名称和 Cron 表达式')
    return
  }
  try {
    await api.post('/cron-jobs', form.value)
    ElMessage.success('定时任务已创建')
    createVisible.value = false
    form.value = { name: '', expr: '0 2 * * *', agent_id: '', rule_set_id: '', scene: 'agent' }
    await loadJobs()
  } catch (e: any) {
    ElMessage.error(e.message || '创建失败')
  }
}

async function toggle(job: CronJob) {
  try {
    await api.put(`/cron-jobs/${job.id}/toggle`, { enabled: !job.enabled })
    job.enabled = !job.enabled
    ElMessage.success(job.enabled ? '已启用' : '已暂停')
  } catch (e: any) {
    ElMessage.error(e.message || '操作失败')
  }
}

async function remove(job: CronJob) {
  try {
    await ElMessageBox.confirm(`确定删除定时任务"${job.name}"?`, '删除', { type: 'warning' })
    await api.delete(`/cron-jobs/${job.id}`)
    await loadJobs()
  } catch (e: any) {
    if (e !== 'cancel') ElMessage.error(e.message || '删除失败')
  }
}

// 格式化时间(去掉时区后缀)
function fmtTime(t: string): string {
  if (!t || t.startsWith('0001')) return '-'
  return t.slice(0, 19).replace('T', ' ')
}

// 常用 Cron 预设(供用户快速选)
const cronPresets = [
  { label: '每小时', expr: '0 * * * *' },
  { label: '每 2 小时', expr: '0 */2 * * *' },
  { label: '每天 02:00', expr: '0 2 * * *' },
  { label: '每天 08:00', expr: '0 8 * * *' },
  { label: '每 30 分钟', expr: '*/30 * * * *' },
]

function usePreset(expr: string) {
  form.value.expr = expr
}

onMounted(async () => {
  await Promise.all([loadJobs(), loadStats(), loadDict()])
})
</script>

<template>
  <div>
    <h2 class="page-title">定时任务</h2>

    <!-- 调度器统计 -->
    <el-row :gutter="12" class="stats-row">
      <el-col :span="4"><el-card shadow="hover" class="stat-card"><div class="s-num">{{ stats.queued }}</div><div class="s-lbl">队列等待</div></el-card></el-col>
      <el-col :span="4"><el-card shadow="hover" class="stat-card"><div class="s-num" style="color:#e6a23c">{{ stats.running }}</div><div class="s-lbl">运行中</div></el-card></el-col>
      <el-col :span="4"><el-card shadow="hover" class="stat-card"><div class="s-num" style="color:#67c23a">{{ stats.completed }}</div><div class="s-lbl">已完成</div></el-card></el-col>
      <el-col :span="4"><el-card shadow="hover" class="stat-card"><div class="s-num" style="color:#f56c6c">{{ stats.failed }}</div><div class="s-lbl">失败</div></el-card></el-col>
      <el-col :span="4"><el-card shadow="hover" class="stat-card"><div class="s-num" style="color:#909399">{{ stats.retried }}</div><div class="s-lbl">重试</div></el-card></el-col>
    </el-row>

    <!-- 任务列表 -->
    <el-card shadow="never">
      <template #header>
        <div class="card-head">
          <span>定时任务 ({{ jobs.length }})</span>
          <el-button type="primary" size="small" @click="createVisible = true">
            <el-icon><Plus /></el-icon>&nbsp;新建定时任务
          </el-button>
        </div>
      </template>

      <el-empty v-if="!jobs.length" description="暂无定时任务,创建后平台会按 Cron 自动跑回归评测" :image-size="80" />

      <el-table v-else :data="jobs" stripe v-loading="loading">
        <el-table-column prop="name" label="任务名" min-width="160" />
        <el-table-column label="Cron 表达式" width="140">
          <template #default="{ row }"><code class="cron-expr">{{ row.expr }}</code></template>
        </el-table-column>
        <el-table-column label="场景" width="90">
          <template #default="{ row }"><el-tag size="small">{{ row.scene }}</el-tag></template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small">{{ row.enabled ? '启用' : '暂停' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="下次执行" width="160">
          <template #default="{ row }">{{ fmtTime(row.next_run) }}</template>
        </el-table-column>
        <el-table-column label="上次执行" width="160">
          <template #default="{ row }">{{ fmtTime(row.last_run) }}</template>
        </el-table-column>
        <el-table-column prop="run_count" label="执行次数" width="90" />
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button size="small" :type="row.enabled ? 'warning' : 'success'" text @click="toggle(row)">
              {{ row.enabled ? '暂停' : '启用' }}
            </el-button>
            <el-button size="small" type="danger" text @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 创建对话框 -->
    <el-dialog v-model="createVisible" title="新建定时任务" width="560px">
      <el-form label-width="110px">
        <el-form-item label="任务名称" required>
          <el-input v-model="form.name" placeholder="如:CodeGen 每日回归" />
        </el-form-item>
        <el-form-item label="Cron 表达式" required>
          <el-input v-model="form.expr" placeholder="0 2 * * *(每天 02:00)" />
          <div class="preset-row">
            <el-button v-for="p in cronPresets" :key="p.expr" size="small" text @click="usePreset(p.expr)">{{ p.label }}</el-button>
          </div>
        </el-form-item>
        <el-form-item label="评测场景">
          <el-select v-model="form.scene" style="width:100%">
            <el-option label="Agent 评测" value="agent" />
            <el-option label="大模型评测" value="model" />
            <el-option label="竞品报告" value="competitor" />
            <el-option label="PRD 评测" value="prd" />
            <el-option label="AI 产品评测" value="product" />
          </el-select>
        </el-form-item>
        <el-form-item label="被测 Agent">
          <el-select v-model="form.agent_id" clearable filterable style="width:100%">
            <el-option v-for="a in agents" :key="a.id" :label="a.name" :value="a.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="规则集">
          <el-select v-model="form.rule_set_id" clearable filterable style="width:100%">
            <el-option v-for="r in ruleSets" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 16px; }
.stats-row { margin-bottom: 16px; }
.stat-card { text-align: center; }
.s-num { font-size: 26px; font-weight: 700; color: #409eff; }
.s-lbl { font-size: 12px; color: #999; margin-top: 2px; }
.card-head { display: flex; justify-content: space-between; align-items: center; }
.cron-expr { background: #f5f7fa; padding: 2px 6px; border-radius: 3px; font-size: 12px; }
.preset-row { margin-top: 6px; display: flex; flex-wrap: wrap; gap: 4px; }
</style>
