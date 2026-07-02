<script setup lang="ts">
// Clone.vue — 智能体多基座克隆与 A/B 对比(MRD 第八章 / ④.1)。
//
// 功能:
//   ① 配置区:选原体 Agent + 目标模型列表 + 规则集
//   ② 执行后展示:
//      - 雷达图叠加(原体 vs 各克隆体,同维度对比)
//      - 性价比帕累托散点图(X=费用 Y=分数)
//      - MSI 敏感度指数 + 运营建议文字结论
//      - 明细表(各 Agent 的分数/维度/token/费用/通过率)
import { ref, computed } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'
import BaseChart from '../components/BaseChart.vue'
import DiffViewer from '../components/DiffViewer.vue'

// 字典
const agents = ref<any[]>([])
const models = ref<any[]>([])
const ruleSets = ref<any[]>([])

// 配置表单
const form = ref({
  source_agent_id: '',
  target_models: [] as string[],
  rule_set_id: 'rs-fullstack',
  question_count: 3,
})

const loading = ref(false)
const result = ref<any>(null)
const diffQIndex = ref(0) // 选第几题做 diff 对比

// 维度中文
const dimLabel = (k: string): string => ({
  security: '安全', performance: '性能', architecture: '架构',
  correctness: '正确性', product: '产品',
} as Record<string, string>)[k] || k

// 最佳克隆体(分数最高)
const bestClone = computed(() => {
  if (!result.value?.clones?.length) return null
  return [...result.value.clones].sort((a: any, b: any) => (b.overall || 0) - (a.overall || 0))[0]
})

// Diff 对比数据:当前选题的原体 vs 最佳克隆
const diffData = computed(() => {
  const src = result.value?.source_agent
  const clone = bestClone.value
  if (!src?.samples?.[diffQIndex.value]) return null
  return {
    left: src.samples[diffQIndex.value].answer || '',
    right: clone?.samples?.[diffQIndex.value]?.answer || '',
    leftLabel: src.model || '原体',
    rightLabel: clone?.model || '克隆',
    question: src.samples[diffQIndex.value].question || '',
  }
})

// 收集所有维度
const allDimensions = computed<string[]>(() => {
  if (!result.value) return []
  const dims = new Set<string>()
  const collect = (bench: any) => {
    if (bench?.dimensions) Object.keys(bench.dimensions).forEach((d) => dims.add(d))
  }
  collect(result.value.source_agent)
  for (const c of result.value.clones || []) collect(c)
  return [...dims]
})

// 所有 benchmark(原体+克隆)统一列表(供表格和图表用)
const allBenches = computed(() => {
  if (!result.value) return []
  return [result.value.source_agent, ...(result.value.clones || [])]
})

// ── 雷达图叠加配置 ──
const radarOption = computed(() => {
  const dims = allDimensions.value
  if (!dims.length) return {}
  const colors = ['#409eff', '#67c23a', '#e6a23c', '#f56c6c', '#909399', '#9c27b0']
  return {
    tooltip: {},
    legend: { bottom: 0, data: allBenches.value.map((b: any) => b.model) },
    radar: {
      indicator: dims.map((d) => ({ name: dimLabel(d), max: 100 })),
      shape: 'polygon',
    },
    series: [{
      type: 'radar',
      data: allBenches.value.map((b: any, i: number) => ({
        value: dims.map((d) => b.dimensions?.[d] ?? 0),
        name: b.model,
        areaStyle: { color: colors[i % colors.length], opacity: 0.15 },
        lineStyle: { color: colors[i % colors.length] },
      })),
    }],
  } as any
})

// ── ROI 帕累托散点图 ──
const scatterOption = computed(() => {
  if (!allBenches.value.length) return {}
  return {
    tooltip: {
      trigger: 'item',
      formatter: (p: any) => `${p.data[2]}<br/>分数: ${p.data[1]}<br/>费用: $${p.data[0]}`,
    },
    xAxis: { name: '费用 ($)', type: 'value', scale: true },
    yAxis: { name: '综合分数', type: 'value', min: 0, max: 100 },
    series: [{
      type: 'scatter',
      symbolSize: 20,
      data: allBenches.value.map((b: any) => [
        Number((b.est_cost || 0).toFixed(4)),
        b.overall || 0,
        b.model,
      ]),
      label: { show: true, formatter: (p: any) => p.data[2], position: 'right' },
    }],
  } as any
})

async function loadDict() {
  const [a, m, r] = await Promise.all([api.get('/agents'), api.get('/models'), api.get('/rule-sets')])
  agents.value = a
  models.value = m
  ruleSets.value = r
}

async function runClone() {
  if (!form.value.source_agent_id) {
    ElMessage.warning('请选择原体 Agent')
    return
  }
  if (!form.value.target_models.length) {
    ElMessage.warning('请至少选择一个目标模型')
    return
  }
  loading.value = true
  result.value = null
  try {
    result.value = await api.post('/clone-test', form.value)
    ElMessage.success('克隆对比完成')
  } catch (e: any) {
    ElMessage.error(e.message || '执行失败')
  } finally {
    loading.value = false
  }
}

// 初始化
loadDict()

// ── Prompt 平移适配器(MRD 8.5)──
const promptAdapting = ref(false)
const promptResult = ref<any>(null)
const promptForm = ref({
  source_prompt: '',
  source_model: '',
  target_model: '',
})
const promptTemplateId = ref('') // 从模板库加载
const promptTemplates = ref<any[]>([])
// 从 Clone 表单自动同步模型
const promptSourceModel = computed(() => {
  const a = agents.value.find((x: any) => x.id === form.value.source_agent_id)
  return a?.model || ''
})
const promptTargetModels = computed(() => form.value.target_models || [])

async function runPromptAdapt() {
  if (!promptForm.value.source_prompt) {
    ElMessage.warning('请先输入源 Prompt 或从 Prompt 模板库中加载')
    return
  }
  promptForm.value.source_model = promptSourceModel.value
  promptForm.value.target_model = promptTargetModels.value[0] || ''
  if (!promptForm.value.source_model || !promptForm.value.target_model) {
    ElMessage.warning('请先选择源 Agent 和目标模型')
    return
  }
  promptAdapting.value = true
  promptResult.value = null
  try {
    promptResult.value = await api.post('/prompt/adapt', promptForm.value)
    ElMessage.success(`生成 ${promptResult.value.suggestions?.length || 0} 条适配建议`)
  } catch (e: any) {
    ElMessage.error(e.message || '适配失败')
  } finally {
    promptAdapting.value = false
  }
}

// 从模板库加载 Prompt
async function loadPromptTemplate() {
  if (!promptTemplateId.value) return
  try {
    const t = await api.get(`/prompt-templates/${promptTemplateId.value}`)
    // 如果返回数组(列表),取第一个;如果单个对象,直接用
    const tmpl = Array.isArray(t) ? t[0] : t
    if (tmpl?.content) {
      promptForm.value.source_prompt = tmpl.content
      ElMessage.success(`已加载模板: ${tmpl.name} (v${tmpl.version})`)
    }
  } catch (e: any) {
    // fallback: 从 dict store 查找
    const found = promptTemplates.value.find((x: any) => x.id === promptTemplateId.value)
    if (found?.content) {
      promptForm.value.source_prompt = found.content
      ElMessage.success(`已加载模板: ${found.name}`)
    } else {
      ElMessage.error('加载失败')
    }
  }
}

// 加载模板列表
async function loadTemplates() {
  try {
    promptTemplates.value = await api.get('/prompt-templates')
  } catch { /* ignore */ }
}

const sevTagType = (s: string) => ({ must: 'danger', should: 'warning', may: 'info' } as Record<string, string>)[s] || 'info'
</script>

<template>
  <div>
    <h2 class="page-title">克隆对比 — 多基座 A/B 测试</h2>

    <!-- 配置区 -->
    <el-card shadow="never" class="config-card">
      <el-form label-width="100px" inline>
        <el-form-item label="原体 Agent">
          <el-select v-model="form.source_agent_id" filterable placeholder="选择被测 Agent" style="width: 200px">
            <el-option v-for="a in agents" :key="a.id" :label="`${a.name} (${a.model})`" :value="a.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="目标模型">
          <el-select v-model="form.target_models" multiple filterable placeholder="克隆到哪些模型" style="width: 280px">
            <el-option v-for="m in models" :key="m.id" :label="m.name" :value="m.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="规则集">
          <el-select v-model="form.rule_set_id" style="width: 180px">
            <el-option v-for="r in ruleSets" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="题数">
          <el-input-number v-model="form.question_count" :min="1" :max="10" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" @click="runClone">
            <el-icon><Cpu /></el-icon>&nbsp;开始对比
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 结果区 -->
    <div v-if="result" v-loading="loading">
      <!-- 结论卡 -->
      <el-row :gutter="16" class="conclusion-row">
        <el-col :span="8">
          <el-card shadow="hover" class="conclusion-card">
            <div class="c-label">MSI 基座敏感度</div>
            <div class="c-value" :style="{ color: result.msi < 10 ? '#67c23a' : result.msi < 30 ? '#e6a23c' : '#f56c6c' }">
              {{ result.msi }}
            </div>
            <div class="c-hint">{{ result.msi < 10 ? '极稳定' : result.msi < 30 ? '中等敏感' : '高度敏感' }}</div>
          </el-card>
        </el-col>
        <el-col :span="8">
          <el-card shadow="hover" class="conclusion-card">
            <div class="c-label">最佳性价比</div>
            <div class="c-value" style="color: #67c23a">{{ result.best_value }}</div>
            <div class="c-hint">分数/费用比最高</div>
          </el-card>
        </el-col>
        <el-col :span="8">
          <el-card shadow="hover" class="conclusion-card">
            <div class="c-label">参与对比</div>
            <div class="c-value">{{ Number(result.clones?.length || 0) + 1 }}</div>
            <div class="c-hint">个 Agent(含原体)</div>
          </el-card>
        </el-col>
      </el-row>

      <!-- 运营建议 -->
      <el-alert :title="result.recommendation" type="info" show-icon :closable="false" class="reco-alert" />

      <!-- 图表区 -->
      <el-row :gutter="16" class="chart-row">
        <el-col :span="12">
          <el-card shadow="never">
            <template #header>雷达图叠加(维度对比)</template>
            <BaseChart :option="radarOption" height="340px" />
          </el-card>
        </el-col>
        <el-col :span="12">
          <el-card shadow="never">
            <template #header>性价比帕累托散点</template>
            <BaseChart :option="scatterOption" height="340px" />
          </el-card>
        </el-col>
      </el-row>

      <!-- 明细表 -->
      <el-card shadow="never" class="detail-card">
        <template #header>各 Agent 基准明细</template>
        <el-table :data="allBenches" stripe size="small">
          <el-table-column label="Agent">
            <template #default="{ row }">
              <el-tag :type="row.agent_id === result.source_agent.agent_id ? 'primary' : 'success'" size="small">
                {{ row.agent_id === result.source_agent.agent_id ? '原体' : '克隆' }}
              </el-tag>
              {{ row.agent_name }}
            </template>
          </el-table-column>
          <el-table-column prop="model" label="模型" width="140" />
          <el-table-column prop="overall" label="综合分" width="90">
            <template #default="{ row }">
              <span :style="{ color: row.overall >= 80 ? '#67c23a' : row.overall >= 60 ? '#e6a23c' : '#f56c6c', fontWeight: 700 }">{{ row.overall }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="pass_rate" label="通过率" width="90" />
          <el-table-column prop="token_total" label="Token" width="100" />
          <el-table-column label="费用" width="90">
            <template #default="{ row }">${{ (row.est_cost || 0).toFixed(4) }}</template>
          </el-table-column>
          <!-- 各维度分(动态列) -->
          <el-table-column
            v-for="dim in allDimensions"
            :key="dim"
            :label="dimLabel(dim)"
            width="80"
          >
            <template #default="{ row }">{{ row.dimensions?.[dim] ?? '-' }}</template>
          </el-table-column>
        </el-table>
      </el-card>

      <!-- ROI 两两对比 -->
      <el-card v-if="result.roi_analysis?.length" shadow="never" class="detail-card">
        <template #header>性价比两两对比</template>
        <el-table :data="result.roi_analysis" stripe size="small">
          <el-table-column label="对比">
            <template #default="{ row }">{{ row.model_a }} vs {{ row.model_b }}</template>
          </el-table-column>
          <el-table-column label="分数">
            <template #default="{ row }">{{ row.score_a }} vs {{ row.score_b }}</template>
          </el-table-column>
          <el-table-column label="费用">
            <template #default="{ row }">${{ (row.cost_a||0).toFixed(4) }} vs ${{ (row.cost_b||0).toFixed(4) }}</template>
          </el-table-column>
          <el-table-column label="性能胜出" width="120">
            <template #default="{ row }"><el-tag size="small">{{ row.winner }}</el-tag></template>
          </el-table-column>
          <el-table-column label="性价比胜出" width="120">
            <template #default="{ row }"><el-tag size="small" type="success">{{ row.best_value }}</el-tag></template>
          </el-table-column>
        </el-table>
      </el-card>

      <!-- ═══ 代码 Diff 对比 ═══ -->
      <el-card v-if="diffData" shadow="never" class="detail-card">
        <template #header>
          <div class="diff-header">
            <span>🔍 回答 Diff: {{ diffData.question }}</span>
            <el-select v-model="diffQIndex" size="small" style="width:160px">
              <el-option
                v-for="(s, i) in result.source_agent.samples"
                :key="i"
                :label="`题${Number(i) + 1}: ${s.question?.slice(0, 15) || ''}`"
                :value="i"
              />
            </el-select>
          </div>
        </template>
        <DiffViewer
          :left="diffData.left"
          :right="diffData.right"
          :left-label="diffData.leftLabel"
          :right-label="diffData.rightLabel"
        />
      </el-card>
    </div>

    <!-- ═══ Prompt 智能平移适配器(⑧.5)═══ -->
    <el-card shadow="never" class="detail-card prompt-adapter-card">
      <template #header>
        <div class="diff-header">
          <span>🔄 Prompt 平移适配器 (模型切换时自动建议 Prompt 适配)</span>
          <el-tag v-if="promptSourceModel" size="small" type="info">{{ promptSourceModel }}</el-tag>
          <span v-if="promptSourceModel && promptTargetModels.length" style="font-size:14px;color:#909399;margin:0 6px;">→</span>
          <el-tag v-if="promptTargetModels.length" size="small" type="warning">{{ promptTargetModels[0] }}</el-tag>
        </div>
      </template>
      <el-form label-width="100px">
        <el-form-item label="加载模板">
          <el-select v-model="promptTemplateId" filterable placeholder="从 Prompt 模板库加载" style="width: 280px" clearable @change="loadPromptTemplate" @visible-change="(v: boolean) => v && loadTemplates()">
            <el-option v-for="t in promptTemplates" :key="t.id" :label="`${t.name} (v${t.version})`" :value="t.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="源 Prompt">
          <el-input
            v-model="promptForm.source_prompt"
            type="textarea"
            :rows="6"
            placeholder="粘贴针对源模型优化的 System Prompt,或从 Prompt 模板库加载..."
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="promptAdapting" @click="runPromptAdapt">
            <el-icon><Switch /></el-icon>&nbsp;分析并适配
          </el-button>
        </el-form-item>
      </el-form>

      <!-- 适配结果 -->
      <div v-if="promptResult">
        <el-alert
          :title="promptResult.style_conflict"
          :type="promptResult.suggestions?.length ? 'warning' : 'success'"
          show-icon :closable="false"
          style="margin-bottom:12px"
        />
        <el-descriptions :column="2" size="small" border style="margin-bottom:12px">
          <el-descriptions-item label="源风格">{{ promptResult.source_profile?.display_name }} ({{ promptResult.source_profile?.style }})</el-descriptions-item>
          <el-descriptions-item label="目标风格">{{ promptResult.target_profile?.display_name }} ({{ promptResult.target_profile?.style }})</el-descriptions-item>
          <el-descriptions-item label="适配置信度">{{ ((promptResult.confidence || 0) * 100).toFixed(0) }}%</el-descriptions-item>
          <el-descriptions-item label="建议数">{{ promptResult.suggestions?.length || 0 }}</el-descriptions-item>
        </el-descriptions>

        <!-- 建议列表 -->
        <el-card v-if="promptResult.suggestions?.length" shadow="never" style="margin-bottom:12px">
          <template #header>适配建议</template>
          <div v-for="(s, i) in promptResult.suggestions" :key="i" class="suggestion-item">
            <el-tag :type="sevTagType(s.severity)" size="small" effect="dark">{{ s.severity === 'must' ? '必须' : s.severity === 'should' ? '建议' : '可选' }}</el-tag>
            <span class="sug-desc">{{ s.description }}</span>
            <div class="sug-reason">📝 {{ s.reason }}</div>
            <div v-if="s.before !== s.after" class="sug-diff">
              <span class="sug-before">❌ {{ (s.before || '').slice(0, 100) }}</span>
              <span class="sug-arrow">→</span>
              <span class="sug-after">✅ {{ (s.after || '').slice(0, 100) }}</span>
            </div>
          </div>
        </el-card>

        <!-- 适配预览 -->
        <el-card v-if="promptResult.adapted_prompt" shadow="never">
          <template #header>适配后 Prompt 预览</template>
          <el-input
            :model-value="promptResult.adapted_prompt"
            type="textarea"
            :rows="8"
            readonly
            style="font-family: monospace; font-size: 13px;"
          />
          <el-button size="small" style="margin-top:8px" @click="promptForm.source_prompt = promptResult.adapted_prompt">
            <el-icon><Check /></el-icon>&nbsp;采用此版本
          </el-button>
        </el-card>
      </div>
    </el-card>

    <!-- 空状态 -->
    <el-empty
      v-if="!result && !loading"
      description="配置原体 Agent 和目标模型,开始多基座 A/B 对比"
      :image-size="100"
    />
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 16px; }
.config-card { margin-bottom: 16px; }
.conclusion-row { margin-bottom: 16px; }
.conclusion-card { text-align: center; }
.c-label { font-size: 13px; color: #909399; }
.c-value { font-size: 28px; font-weight: 700; color: #409eff; margin: 6px 0 2px; }
.c-hint { font-size: 12px; color: #c0c4cc; }
.reco-alert { margin-bottom: 16px; }
.chart-row { margin-bottom: 16px; }
.detail-card { margin-bottom: 16px; }
.diff-header { display: flex; justify-content: space-between; align-items: center; }
.prompt-adapter-card { border-left: 3px solid var(--el-color-warning); }
.suggestion-item { padding: 10px 0; border-bottom: 1px solid #f0f0f0; }
.suggestion-item:last-child { border-bottom: none; }
.sug-desc { margin-left: 8px; font-weight: 500; color: #303133; }
.sug-reason { margin: 4px 0 4px 24px; font-size: 12px; color: #909399; }
.sug-diff { margin: 6px 0 2px 24px; font-size: 13px; font-family: monospace; }
.sug-before { color: #f56c6c; background: #fef0f0; padding: 1px 4px; border-radius: 3px; }
.sug-after { color: #67c23a; background: #f0f9eb; padding: 1px 4px; border-radius: 3px; }
.sug-arrow { margin: 0 6px; color: #909399; }
</style>
