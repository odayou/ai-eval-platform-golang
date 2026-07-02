<script setup lang="ts">
/**
 * Standards.vue — 评测标准页(MRD 11.4)。
 *
 * 布局:
 *   左栏(30%):规则集列表 + 新建 + JSON 导入
 *   右栏(70%):两个子 Tab
 *     ① 规则列表:该规则集下的规则(维度、评分方式、权重)
 *     ② Bad Case 反哺记录:待处理 Bad Case,一键确认落库(④阶段填充)
 */
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../utils/api'
import RuleGraph from '../components/RuleGraph.vue'

interface RuleSet { id: string; name: string; scene: string; rule_ids: string; created_at: string }
interface Rule { id: string; name: string; dimension: string; scoring: string; weight: number; content: string }
interface BadCase { id: string; task_id: string; answer: string; score: number; feedback_type: string; status: string }
interface Suggestion { id: string; bad_case_id: string; suggestion: string; status: string; created_at: string }

const ruleSets = ref<RuleSet[]>([])
const allRules = ref<Rule[]>([])
const badCases = ref<BadCase[]>([])
const suggestions = ref<Suggestion[]>([])
const selectedSet = ref<RuleSet | null>(null)
const activeSubTab = ref('rules')

// ── 规则反馈建议管理 ──
const applyingId = ref<string | null>(null)
async function loadSuggestions() {
  suggestions.value = await api.get('/feedback/suggestions')
}
async function applySuggestion(id: string) {
  applyingId.value = id
  try {
    const res = await api.post(`/feedback/suggestions/${id}/apply`, {})
    ElMessage.success(`建议已采纳，新规则 ID: ${res.rule_id?.slice(0, 8)}`)
    await loadSuggestions()
    await loadRules()
  } catch (e: any) {
    ElMessage.error(e.message || '采纳失败')
  } finally {
    applyingId.value = null
  }
}

// 新建规则集
const createVisible = ref(false)
const form = ref({ name: '', scene: '', rule_ids: '' })

// 导入导出
const importVisible = ref(false)
const importJSON = ref('')
const importType = ref<'rule-set' | 'rules'>('rules')

async function exportRuleSet() {
  if (!selectedSet.value) return
  try {
    const data = await api.get(`/rule-sets/${selectedSet.value.id}/export`)
    downloadJSON(data, `rule-set-${selectedSet.value.name}.json`)
    ElMessage.success('规则集已导出')
  } catch (e: any) {
    ElMessage.error('导出失败: ' + e.message)
  }
}

async function exportAllRules() {
  try {
    const data = await api.get('/rules/export')
    downloadJSON(data, 'rules-export.json')
    ElMessage.success('规则已导出')
  } catch (e: any) {
    ElMessage.error('导出失败: ' + e.message)
  }
}

function openImport(type: 'rule-set' | 'rules') {
  importType.value = type
  importJSON.value = ''
  importVisible.value = true
}

async function submitImport() {
  try {
    const data = JSON.parse(importJSON.value)
    if (importType.value === 'rule-set') {
      await api.post('/rule-sets/import', data)
      ElMessage.success('规则集已导入')
      await loadRuleSets()
    } else {
      await api.post('/rules/import', data)
      ElMessage.success('规则已导入')
      await loadRules()
    }
    importVisible.value = false
  } catch (e: any) {
    ElMessage.error('导入失败: ' + (e.message || 'JSON 格式错误'))
  }
}

function downloadJSON(data: any, filename: string) {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

function handleFileUpload(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  const reader = new FileReader()
  reader.onload = () => { importJSON.value = reader.result as string }
  reader.readAsText(file)
}

// 规则维度/评分方式映射
const dimMap: Record<string, string> = {
  security: '安全', performance: '性能', architecture: '架构',
  correctness: '正确性', product: '产品',
}
const scoreMap: Record<string, string> = { metric: '硬指标', llm: 'LLM 判官', manual: '人工' }

// 选中规则集所含的规则(从 rule_ids JSON 解析)
const rulesInSet = computed(() => {
  if (!selectedSet.value?.rule_ids) return []
  try {
    const ids: string[] = JSON.parse(selectedSet.value.rule_ids)
    return allRules.value.filter((r) => ids.includes(r.id))
  } catch {
    return []
  }
})

async function loadRuleSets() { ruleSets.value = await api.get('/rule-sets') }
async function loadRules() { allRules.value = await api.get('/rules') }
async function loadBadCases() { badCases.value = await api.get('/bad-cases', { params: { status: 'pending' } }) }

function selectSet(rs: RuleSet) { selectedSet.value = rs }

async function submitCreate() {
  if (!form.value.name.trim()) { ElMessage.warning('请填写规则集名称'); return }
  await api.post('/rule-sets', {
    name: form.value.name,
    scene: form.value.scene,
    rule_ids: form.value.rule_ids || '[]',
  })
  ElMessage.success('规则集已创建')
  createVisible.value = false
  form.value = { name: '', scene: '', rule_ids: '' }
  await loadRuleSets()
}

async function deleteSet(id: string) {
  await ElMessageBox.confirm('确定删除该规则集?', '删除', { type: 'warning' })
  await api.delete(`/rule-sets/${id}`)
  if (selectedSet.value?.id === id) selectedSet.value = null
  await loadRuleSets()
}

const pageLoading = ref(false)
onMounted(async () => {
  pageLoading.value = true
  try {
    await Promise.all([loadRuleSets(), loadRules(), loadBadCases(), loadSuggestions()])
  } catch (e: any) {
    ElMessage.error('标准数据加载失败: ' + (e.message || '网络错误'))
  } finally {
    pageLoading.value = false
  }
})

// 知识图谱
const graphRef = ref<InstanceType<typeof RuleGraph> | null>(null)
function openGraph() { graphRef.value?.open() }
</script>

<template>
  <div v-loading="pageLoading" element-loading-text="加载标准数据...">
    <h2 class="page-title">评测标准</h2>
    <el-row :gutter="16">
      <!-- 左栏:规则集列表 -->
      <el-col :span="7">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              <span>规则集 ({{ ruleSets.length }})</span>
              <div>
                <el-button size="small" @click="openImport('rule-set')">导入</el-button>
                <el-button size="small" @click="exportAllRules">导出规则</el-button>
                <el-button size="small" type="primary" circle @click="createVisible = true">
                  <el-icon><Plus /></el-icon>
                </el-button>
              </div>
            </div>
          </template>
          <el-empty v-if="!ruleSets.length" description="暂无规则集" :image-size="50" />
          <div
            v-for="rs in ruleSets"
            :key="rs.id"
            :class="['rs-item', { active: rs.id === selectedSet?.id }]"
            @click="selectSet(rs)"
          >
            <div class="rs-name">{{ rs.name }}</div>
            <div class="rs-meta">
              <span>{{ rs.scene || '通用' }}</span>
              <el-button type="danger" text size="small" @click.stop="deleteSet(rs.id)">删除</el-button>
            </div>
          </div>
        </el-card>
      </el-col>

      <!-- 右栏:规则集详情 -->
      <el-col :span="17">
        <el-card shadow="never">
          <el-empty v-if="!selectedSet" description="选择一个规则集查看详情" :image-size="80" />
          <template v-else>
            <div class="detail-head">
              <h3>{{ selectedSet.name }}</h3>
              <div>
                <el-button size="small" @click="openGraph">
                  <el-icon><Share /></el-icon>&nbsp;查看规则关系图
                </el-button>
                <el-button size="small" @click="exportRuleSet">导出此规则集</el-button>
              </div>
            </div>
            <el-tabs v-model="activeSubTab">
              <!-- 规则列表 -->
              <el-tab-pane :label="`规则 (${rulesInSet.length})`" name="rules">
                <el-table :data="rulesInSet" stripe size="small">
                  <el-table-column prop="name" label="规则名" min-width="140" />
                  <el-table-column label="维度" width="90">
                    <template #default="{ row }">
                      <el-tag size="small">{{ dimMap[row.dimension] || row.dimension }}</el-tag>
                    </template>
                  </el-table-column>
                  <el-table-column label="评分方式" width="100">
                    <template #default="{ row }">{{ scoreMap[row.scoring] }}</template>
                  </el-table-column>
                  <el-table-column prop="weight" label="权重" width="70" />
                  <el-table-column prop="content" label="规则正文" show-overflow-tooltip min-width="200" />
                </el-table>
              </el-tab-pane>

              <!-- Bad Case 反哺 -->
              <el-tab-pane :label="`Bad Case 反哺 (${badCases.length})`" name="badcases">
                <el-empty
                  v-if="!badCases.length"
                  description="暂无待处理 Bad Case(引擎运行后产生)"
                  :image-size="60"
                />
                <div v-else>
                  <p class="hint">以下为低分回答样本,确认后将沉淀为规则更新建议</p>
                  <div v-for="bc in badCases" :key="bc.id" class="bc-item">
                    <div class="bc-score">{{ bc.score.toFixed(1) }} 分</div>
                    <div class="bc-answer">{{ bc.answer.slice(0, 100) }}...</div>
                  </div>
                </div>
              </el-tab-pane>

              <!-- 规则更新建议 -->
              <el-tab-pane :label="`建议 (${suggestions.length})`" name="suggestions">
                <el-empty v-if="!suggestions.length" description="暂无待处理的规则更新建议(人工踩 Bad Case 后产生)" :image-size="60" />
                <div v-else>
                  <p class="hint">以下为人工反馈触发的规则更新建议，确认后自动写入规则库</p>
                  <div v-for="s in suggestions" :key="s.id" class="suggestion-item">
                    <div class="sug-left">
                      <div class="sug-content">{{ s.suggestion }}</div>
                      <div class="sug-meta">来自 Bad Case: {{ s.bad_case_id?.slice(0, 8) }} · {{ s.created_at?.slice(0, 10) }}</div>
                    </div>
                    <div class="sug-right">
                      <el-tag size="small" type="warning" v-if="s.status === 'pending'">待确认</el-tag>
                      <el-tag size="small" type="success" v-else>已采纳</el-tag>
                      <el-button v-if="s.status === 'pending'" size="small" type="primary" :loading="applyingId === s.id" @click="applySuggestion(s.id)">
                        <el-icon><Check /></el-icon> 确认落库
                      </el-button>
                    </div>
                  </div>
                </div>
              </el-tab-pane>
            </el-tabs>
          </template>
        </el-card>
      </el-col>
    </el-row>

    <!-- 创建规则集 -->
    <el-dialog v-model="createVisible" title="新建规则集" width="480px">
      <el-form label-width="90px">
        <el-form-item label="名称" required><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="场景"><el-input v-model="form.scene" placeholder="agent / model / ..." /></el-form-item>
        <el-form-item label="规则 ID">
          <el-input v-model="form.rule_ids" type="textarea" :rows="2" placeholder='["r-sec-inject","r-sec-leak"]' />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <!-- 导入对话框 -->
    <el-dialog v-model="importVisible" :title="importType === 'rule-set' ? '导入规则集' : '导入规则'" width="600px">
      <el-form label-width="80px">
        <el-form-item label="上传文件">
          <input type="file" accept=".json" @change="handleFileUpload" />
        </el-form-item>
        <el-form-item label="或粘贴 JSON">
          <el-input v-model="importJSON" type="textarea" :rows="10" placeholder='{"version":"1.0","rules":[...]}' />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="importVisible = false">取消</el-button>
        <el-button type="primary" @click="submitImport" :disabled="!importJSON.trim()">导入</el-button>
      </template>
    </el-dialog>

    <!-- 规则知识图谱(全屏) -->
    <RuleGraph ref="graphRef" />
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 16px; }
.card-head { display: flex; justify-content: space-between; align-items: center; }
.rs-item { padding: 10px; border-radius: 6px; cursor: pointer; }
.rs-item:hover { background: #f5f7fa; }
.rs-item.active { background: #ecf5ff; }
.rs-name { font-weight: 500; margin-bottom: 2px; }
.rs-meta { display: flex; justify-content: space-between; align-items: center; font-size: 12px; color: #999; }
.detail-head { margin-bottom: 12px; }
.detail-head h3 { margin: 0; font-size: 18px; }
.hint { color: #999; font-size: 13px; }
.bc-item { padding: 10px; border: 1px solid #f0f0f0; border-radius: 6px; margin-bottom: 8px; }
.bc-score { font-weight: 700; color: #f56c6c; }
.bc-answer { font-size: 13px; color: #666; margin-top: 4px; }
.suggestion-item { display: flex; justify-content: space-between; align-items: center; padding: 12px; border: 1px solid #f0f0f0; border-radius: 6px; margin-bottom: 8px; background: #fff; }
.sug-left { flex: 1; margin-right: 12px; }
.sug-content { font-weight: 500; color: #303133; margin-bottom: 4px; }
.sug-meta { font-size: 12px; color: #999; }
.sug-right { display: flex; align-items: center; gap: 8px; flex-shrink: 0; }
</style>
