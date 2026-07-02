<script setup lang="ts">
/**
 * Questions.vue — 评测题库页。
 *
 * 布局(MRD 11.3):
 *   左侧筛选(25%):场景、难度、来源
 *   右侧列表:题目卡片,点击展开详情(prompt 全文 + 期望输出)
 *   顶部:手动录入、AI 圆桌出题(AI 出题留待 ③ 引擎)
 */
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../utils/api'
import { useDictStore } from '../stores/dictStore'

interface Question {
  id: string; scene: string; title: string; prompt: string
  expected: string; difficulty: string; source: string
  tags: string; rule_set_id: string
  source_repo: string; source_url: string; desensitized: boolean
  created_at: string
}

interface GeneratedQuestion {
  title: string; prompt: string; expected: string; dimension: string; difficulty: string
}

const dict = useDictStore()
const allQuestions = ref<Question[]>([])
const loading = ref(false)
// 筛选条件
const filter = ref({ scene: '', difficulty: '', source: '', keyword: '' })
const expandedId = ref<string | null>(null)
const createVisible = ref(false)
const importLoading = ref(false)
const form = ref({ scene: 'agent', title: '', prompt: '', expected: '', difficulty: 'mid', source: 'manual' })

// ── AI 圆桌出题 ──
const aiGenerateVisible = ref(false)
const aiGenerating = ref(false)
const aiGenForm = ref({ scene: 'agent', rule_set_id: '', count_per_dim: 2 })
const aiGenResult = ref<GeneratedQuestion[]>([])

async function runAIGenerate() {
  if (!aiGenForm.value.rule_set_id) {
    ElMessage.warning('请选择规则集')
    return
  }
  aiGenerating.value = true
  aiGenResult.value = []
  try {
    const res = await api.post('/questions/generate', aiGenForm.value)
    aiGenResult.value = res.questions || []
    ElMessage.success(`AI 圆桌出题完成: 生成 ${res.generated || aiGenResult.value.length} 道题`)
    await load()
  } catch (e: any) {
    ElMessage.error(e.message || '出题失败')
  } finally {
    aiGenerating.value = false
  }
}

function acceptGeneratedQuestions() {
  aiGenerateVisible.value = false
  aiGenResult.value = []
  ElMessage.success('新题目已入库')
}

// 测试卷(V2 7.1)
interface QuestionSet { id: string; name: string; description: string; question_ids: string; tags: string }
const questionSets = ref<QuestionSet[]>([])

// 维度/来源标签映射
const diffMap: Record<string, string> = { entry: '入门', mid: '中级', high: '高级', adversarial: '对抗' }
const srcMap: Record<string, string> = { manual: '手动', ai: 'AI 生成', baseline: '基线库', dynamic: '动态扩题' }
const diffTag: Record<string, string> = { entry: 'success', mid: 'info', high: 'warning', adversarial: 'danger' }

// 拉取题库(带筛选)
async function load() {
  loading.value = true
  try {
    const params: any = {}
    if (filter.value.scene) params.scene = filter.value.scene
    if (filter.value.difficulty) params.difficulty = filter.value.difficulty
    if (filter.value.source) params.source = filter.value.source
    if (filter.value.keyword) params.keyword = filter.value.keyword
    allQuestions.value = await api.get('/questions', { params })
  } finally {
    loading.value = false
  }
}

// 从 GitLab 导入基线题(V2 7.1)
async function importBaseline() {
  importLoading.value = true
  try {
    const res = await api.post('/questions/import-baseline', { repo: 'tiktok-core', count: 3, scene: 'agent' })
    ElMessage.success(res.message || '导入完成')
    await load()
  } catch (e: any) { ElMessage.error(e.message) }
  finally { importLoading.value = false }
}

// 加载测试卷
async function loadQuestionSets() {
  questionSets.value = await api.get('/question-sets')
}

// 创建题目
async function submitCreate() {
  if (!form.value.title.trim() || !form.value.prompt.trim()) {
    ElMessage.warning('请填写标题和题目内容')
    return
  }
  await api.post('/questions', form.value)
  ElMessage.success('题目已创建')
  createVisible.value = false
  form.value = { scene: 'agent', title: '', prompt: '', expected: '', difficulty: 'mid', source: 'manual' }
  await load()
}

// 删除题目
async function onDelete(id: string) {
  await ElMessageBox.confirm('确定删除该题目?', '删除', { type: 'warning' })
  await api.delete(`/questions/${id}`)
  await load()
}

onMounted(() => { load(); loadQuestionSets() })
</script>

<template>
  <div>
    <div class="page-header">
      <h2>评测题库</h2>
      <div>
        <el-button type="primary" @click="createVisible = true">
          <el-icon><Plus /></el-icon>&nbsp;手动录入
        </el-button>
        <el-button type="primary" @click="aiGenerateVisible = true; dict.ensureRuleSets()">
          <el-icon><MagicStick /></el-icon>&nbsp;AI 圆桌出题
        </el-button>
        <el-button :loading="importLoading" @click="importBaseline">
          <el-icon><Download /></el-icon>&nbsp;从 GitLab 导入基线
        </el-button>
      </div>
    </div>

    <el-row :gutter="16">
      <!-- 左侧筛选 -->
      <el-col :span="6">
        <el-card shadow="never">
          <template #header>筛选</template>
          <el-form label-position="top" size="small">
            <el-form-item label="场景">
              <el-select v-model="filter.scene" clearable @change="load" style="width:100%">
                <el-option label="Agent 评测" value="agent" />
                <el-option label="大模型评测" value="model" />
                <el-option label="竞品报告" value="competitor" />
                <el-option label="PRD 评测" value="prd" />
                <el-option label="AI 产品评测" value="product" />
              </el-select>
            </el-form-item>
            <el-form-item label="难度">
              <el-select v-model="filter.difficulty" clearable @change="load" style="width:100%">
                <el-option v-for="(lbl, k) in diffMap" :key="k" :label="lbl" :value="k" />
              </el-select>
            </el-form-item>
            <el-form-item label="来源">
              <el-select v-model="filter.source" clearable @change="load" style="width:100%">
                <el-option v-for="(lbl, k) in srcMap" :key="k" :label="lbl" :value="k" />
              </el-select>
            </el-form-item>
            <el-form-item label="关键词">
              <el-input v-model="filter.keyword" clearable placeholder="搜标题或题目内容" @keyup.enter="load" @clear="load" style="width:100%" />
            </el-form-item>
            <el-button @click="filter = { scene: '', difficulty: '', source: '', keyword: '' }; load()" plain size="small">
              重置筛选
            </el-button>
          </el-form>
        </el-card>
      </el-col>

      <!-- 右侧题目列表 -->
      <el-col :span="18">
        <el-card shadow="never">
          <template #header>
            <span>题目列表 ({{ allQuestions.length }})</span>
          </template>
          <el-skeleton v-if="loading" :rows="5" animated />
          <el-empty v-else-if="!allQuestions.length" description="暂无题目，点击按钮手动录入或 AI 出题">
            <el-button type="primary" @click="createVisible = true">+ 手动录入题目</el-button>
            <el-button @click="aiGenerateVisible = true">🤖 AI 出题</el-button>
          </el-empty>
          <div v-else class="q-list">
            <div v-for="q in allQuestions" :key="q.id" class="q-item">
              <div class="q-head" @click="expandedId = expandedId === q.id ? null : q.id">
                <span class="q-title">{{ q.title }}</span>
                <div class="q-tags">
                  <el-tag size="small" :type="diffTag[q.difficulty]">{{ diffMap[q.difficulty] }}</el-tag>
                  <el-tag size="small" type="info">{{ srcMap[q.source] }}</el-tag>
                  <el-tag v-if="q.source_repo" size="small" effect="dark">{{ q.source_repo }}</el-tag>
                  <el-tag v-if="q.desensitized" size="small" type="success">已脱敏</el-tag>
                  <el-button type="danger" text size="small" @click.stop="onDelete(q.id)">删除</el-button>
                </div>
              </div>
              <div v-if="expandedId === q.id" class="q-detail">
                <div class="sec"><span class="lbl">题目输入</span><pre>{{ q.prompt }}</pre></div>
                <div v-if="q.expected" class="sec"><span class="lbl">期望输出</span><pre>{{ q.expected }}</pre></div>
              </div>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 创建对话框 -->
    <el-dialog v-model="createVisible" title="手动录入题目" width="640px">
      <el-form label-width="80px">
        <el-form-item label="标题" required><el-input v-model="form.title" /></el-form-item>
        <el-form-item label="场景">
          <el-select v-model="form.scene" style="width:100%">
            <el-option label="Agent 评测" value="agent" />
            <el-option label="大模型评测" value="model" />
            <el-option label="竞品报告" value="competitor" />
            <el-option label="PRD 评测" value="prd" />
            <el-option label="AI 产品评测" value="product" />
          </el-select>
        </el-form-item>
        <el-form-item label="题目内容" required>
          <el-input v-model="form.prompt" type="textarea" :rows="5" placeholder="喂给被测 Agent 的完整输入" />
        </el-form-item>
        <el-form-item label="期望输出">
          <el-input v-model="form.expected" type="textarea" :rows="3" placeholder="可选,metric 打分参照" />
        </el-form-item>
        <el-form-item label="难度">
          <el-select v-model="form.difficulty" style="width:100%">
            <el-option v-for="(lbl, k) in diffMap" :key="k" :label="lbl" :value="k" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <!-- 测试卷(V2 7.1 基线测试集) -->
    <el-card v-if="questionSets.length" shadow="never" style="margin-top:16px">
      <template #header>
        <span>📋 基线测试卷 ({{ questionSets.length }})</span>
      </template>
      <el-table :data="questionSets" size="small" stripe>
        <el-table-column prop="name" label="名称" min-width="160" />
        <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip />
        <el-table-column label="题数" width="80">
          <template #default="{ row }">
            {{ (JSON.parse(row.question_ids || '[]') as string[]).length }}
          </template>
        </el-table-column>
        <el-table-column label="标签" width="140">
          <template #default="{ row }">
            <el-tag v-for="t in (JSON.parse(row.tags || '[]') as string[])" :key="t" size="small" style="margin:1px">{{ t }}</el-tag>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
    <!-- ═══ AI 圆桌出题对话框 ═══ -->
    <el-dialog v-model="aiGenerateVisible" title="🤖 AI 圆桌出题" width="640px">
      <el-form label-width="100px">
        <el-form-item label="评测场景">
          <el-select v-model="aiGenForm.scene" style="width:100%">
            <el-option label="Agent 评测" value="agent" />
            <el-option label="大模型评测" value="model" />
            <el-option label="竞品报告" value="competitor" />
            <el-option label="PRD 评测" value="prd" />
            <el-option label="AI 产品评测" value="product" />
          </el-select>
        </el-form-item>
        <el-form-item label="规则集" required>
          <el-select v-model="aiGenForm.rule_set_id" filterable placeholder="选择规则集" style="width:100%">
            <el-option v-for="rs in dict.ruleSets" :key="rs.id" :label="rs.name" :value="rs.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="每维度题数">
          <el-input-number v-model="aiGenForm.count_per_dim" :min="1" :max="5" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="aiGenerateVisible = false">取消</el-button>
        <el-button type="primary" :loading="aiGenerating" @click="runAIGenerate">
          {{ aiGenerating ? '出题中...' : '开始出题' }}
        </el-button>
      </template>

      <!-- 出题结果预览 -->
      <div v-if="aiGenResult.length" class="ai-gen-preview">
        <el-divider />
        <h4>生成题目预览 ({{ aiGenResult.length }} 道)</h4>
        <div v-for="(q, i) in aiGenResult" :key="i" class="gen-item">
          <el-tag size="small" :type="q.difficulty === 'high' || q.difficulty === 'adversarial' ? 'danger' : 'info'">
            {{ diffMap[q.difficulty] || q.difficulty }}
          </el-tag>
          <span class="gen-title">{{ q.title }}</span>
          <el-button text size="small" @click="expandedId = expandedId === '__gen_' + i ? null : '__gen_' + i">
            {{ expandedId === '__gen_' + i ? '收起' : '预览' }}
          </el-button>
          <div v-if="expandedId === '__gen_' + i" class="gen-detail">
            <pre>{{ q.prompt }}</pre>
            <div v-if="q.expected" class="gen-expected">期望: {{ q.expected }}</div>
          </div>
        </div>
        <el-button type="success" style="margin-top:12px" @click="acceptGeneratedQuestions">✅ 确认入库</el-button>
      </div>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.page-header h2 { margin: 0; font-size: 20px; color: #1a1a2e; }
.q-list { display: flex; flex-direction: column; }
.q-item { border-bottom: 1px solid #f0f0f0; }
.q-head { display: flex; justify-content: space-between; align-items: center; padding: 12px 0; cursor: pointer; }
.q-title { font-weight: 500; }
.q-tags { display: flex; gap: 6px; align-items: center; }
.q-detail { padding: 0 0 12px; }
.sec { margin-bottom: 8px; }
.lbl { display: block; font-size: 12px; color: #999; margin-bottom: 4px; }
pre { margin: 0; padding: 8px; background: #f5f7fa; border-radius: 4px; font-size: 13px; white-space: pre-wrap; }
.ai-gen-preview { margin-top: 12px; }
.gen-item { padding: 8px; border-bottom: 1px solid #f0f0f0; display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.gen-title { font-weight: 500; flex: 1; }
.gen-detail { width: 100%; }
.gen-detail pre { margin: 4px 0; }
.gen-expected { color: #67c23a; font-size: 13px; margin-top: 4px; }
</style>
