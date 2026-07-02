<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

const mode = ref<'quick' | 'detail'>('quick')
const purpose = ref('')
const modelRef = ref('')
const models = ref<any[]>([])
const isCreating = ref(false)
const draft = ref<any>(null)
const detailDefID = ref('')
const showPreview = ref(false)

onMounted(async () => {
  try { models.value = await api.get('/models') } catch { models.value = [] }
})

// ── 快速模式 ──
async function createQuick() {
  if (!purpose.value.trim()) { ElMessage.warning('请描述 Agent 用途'); return }
  if (!modelRef.value) { ElMessage.warning('请选择绑定模型'); return }
  isCreating.value = true
  try {
    const res = await api.post('/agents/ai-create-quick', {
      purpose: purpose.value, model_ref: modelRef.value,
      search_provider: 'none', search_api_key: '',
    })
    draft.value = res
    showPreview.value = true
    ElMessage.success(res.ai_generated ? 'AI 已生成 System Prompt' : '返回模板草稿')
  } catch (e: any) {
    ElMessage.error(e.message || '生成失败')
  } finally {
    isCreating.value = false
  }
}

async function saveQuick() {
  if (!draft.value) return
  try {
    const tools = Array.isArray(draft.value.tools) ? draft.value.tools : []
    await api.post('/agents', {
      name: draft.value.display_name || draft.value.name,
      adapter: draft.value.adapter || 'api',
      model: draft.value.model || modelRef.value,
      tools: JSON.stringify(tools),
      permission_level: draft.value.permission_level || 'workspace-write',
      system_prompt: draft.value.system_prompt || purpose.value,
      search_provider: 'none', search_api_key: '',
      example_conversations: '', agents_md: '', skill_ids: '[]',
      max_turns: draft.value.max_turns || 8, verify_command: '',
    })
    ElMessage.success('Agent 已保存到 Agent 库')
    reset()
  } catch (e: any) { ElMessage.error(e.message || '保存失败') }
}

// ── 详细模式 ──
async function createDetail() {
  if (!purpose.value.trim()) { ElMessage.warning('请描述 Agent 用途'); return }
  if (!modelRef.value) { ElMessage.warning('请选择绑定模型'); return }
  isCreating.value = true
  try {
    const res = await api.post('/agents/ai-create-detail', {
      purpose: purpose.value, model_ref: modelRef.value,
      search_provider: 'none', search_api_key: '',
    })
    draft.value = res
    if (res.definition_id) detailDefID.value = res.definition_id
    showPreview.value = true
    ElMessage.success(res.ai_generated ? 'AI 已生成完整定义,请审查后保存' : '返回模板草稿')
  } catch (e: any) {
    ElMessage.error(e.message || '生成失败')
  } finally {
    isCreating.value = false
  }
}

async function saveDetail() {
  if (!detailDefID.value) { ElMessage.error('缺少定义文件 ID'); return }
  try {
    await api.post(`/agent-definitions/${detailDefID.value}/instantiate`, {})
    ElMessage.success('Agent 已从定义文件实例化到 Agent 库')
    reset()
  } catch (e: any) { ElMessage.error(e.message || '保存失败') }
}

function create() {
  if (mode.value === 'quick') createQuick()
  else createDetail()
}

function reset() {
  purpose.value = ''; draft.value = null; detailDefID.value = ''
  showPreview.value = false
}
</script>

<template>
  <div class="create-agent-page">
    <h2>✨ 创造 Agent</h2>
    <p class="page-desc">用 AI 自动设计并创建评测 Agent。选择大模型广场的模型来生成。</p>

    <!-- 模式切换 -->
    <el-radio-group v-model="mode" style="margin-bottom: 20px">
      <el-radio-button value="quick">⚡ 快速模式</el-radio-button>
      <el-radio-button value="detail">📋 详细模式</el-radio-button>
    </el-radio-group>

    <!-- ====== 输入区(两种模式共用,详细模式更全) ====== -->
    <el-card shadow="never" class="input-card">
      <template #header>
        <span>{{ mode === 'quick' ? '⚡ 快速创建' : '📋 完整设计' }}</span>
        <el-tag v-if="mode === 'quick'" type="info" size="small" style="margin-left:8px">
          仅生成 System Prompt + 基础信息
        </el-tag>
        <el-tag v-else type="success" size="small" style="margin-left:8px">
          生成完整 Manifest(SP/工具/示例对话/工作流)
        </el-tag>
      </template>
      <el-form label-width="100px">
        <el-form-item label="用途描述">
          <el-input v-model="purpose" type="textarea" :rows="3"
            :placeholder="mode === 'quick'
              ? '简单描述你想创建什么 Agent，如：帮我审查代码安全性的 Agent'
              : '详细描述 Agent 的职责和能力，如：我需要一个能自动审查 Go 代码安全漏洞的 Agent，它需要在提交代码前检查 SQL 注入、XSS、硬编码密钥等风险，并给出修复建议'" />
        </el-form-item>
        <el-form-item label="绑定模型">
          <el-select v-model="modelRef" filterable placeholder="从大模型广场选择" style="width: 100%">
            <el-option v-for="m in models" :key="m.id"
              :label="`${m.name}${m.api_format ? ' ('+m.api_format+')' : ''}`" :value="m.id" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" size="large" :loading="isCreating" @click="create">
            <el-icon><MagicStick /></el-icon>&nbsp; {{ mode === 'quick' ? '让 AI 生成' : '让 AI 完整设计' }}
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- ====== 预览区 ====== -->
    <transition name="fade">
      <div v-if="showPreview && draft">

        <!-- ── 快速模式预览 ── -->
        <template v-if="mode === 'quick'">
          <el-card shadow="never" class="preview-card">
            <template #header>
              <span>🎯 快速生成结果</span>
              <el-tag v-if="draft.ai_generated" type="success" size="small" style="margin-left:8px">AI 生成</el-tag>
              <el-tag v-else type="warning" size="small">模板草稿</el-tag>
            </template>

            <el-form label-width="100px">
              <el-form-item label="名称">
                <el-input v-model="draft.display_name" />
              </el-form-item>
              <el-form-item label="描述">
                <el-input v-model="draft.description" type="textarea" :rows="2" />
              </el-form-item>
              <el-form-item label="⚠ System Prompt" class="sp-item">
                <el-input v-model="draft.system_prompt" type="textarea" :rows="10" />
                <div class="sp-hint">这是 Agent 的核心行为指令，AI 已根据你的用途描述生成，可编辑调整</div>
              </el-form-item>
              <el-form-item>
                <el-button type="success" size="large" @click="saveQuick">保存到 Agent 库</el-button>
                <el-button @click="reset">放弃</el-button>
              </el-form-item>
            </el-form>
          </el-card>
        </template>

        <!-- ── 详细模式预览(结构化只读展示) ── -->
        <template v-else>
          <el-card shadow="never" class="preview-card detail-preview">
            <template #header>
              <span>📋 完整设计预览</span>
              <el-tag v-if="draft.ai_generated" type="success" size="small" style="margin-left:8px">AI 生成</el-tag>
              <el-tag v-else type="warning" size="small" style="margin-left:8px">模板草稿</el-tag>
              <span class="preview-hint">请逐项审查 AI 的设计，确认后点击底部「保存到 Agent 库」</span>
            </template>

            <!-- 1. 基本信息 -->
            <div class="detail-section">
              <div class="section-title">📋 基本信息</div>
              <div class="section-body">
                <div class="info-row">
                  <span class="info-label">名称</span>
                  <span class="info-value">{{ draft.display_name || draft.name }}</span>
                </div>
                <div class="info-row">
                  <span class="info-label">接入方式</span>
                  <span class="info-value">{{ draft.adapter || 'api' }}</span>
                </div>
                <div class="info-row">
                  <span class="info-label">描述</span>
                  <span class="info-value-desc">{{ draft.description }}</span>
                </div>
                <div class="info-row">
                  <span class="info-label">绑定模型</span>
                  <span class="info-value">{{ draft.model || modelRef }}</span>
                </div>
              </div>
            </div>

            <!-- 2. System Prompt(核心) -->
            <div class="detail-section">
              <div class="section-title">🧠 System Prompt</div>
              <div class="section-desc">Agent 的核心行为指令，AI 根据用途描述生成</div>
              <div class="sp-block">{{ draft.system_prompt || '(未生成)' }}</div>
            </div>

            <!-- 3. 工具/MCP/Skill -->
            <div class="detail-section" v-if="draft.tools?.length || draft.mcps?.length || draft.skills?.length">
              <div class="section-title">🔧 挂载资源</div>
              <div class="section-body">
                <div v-if="draft.tools?.length" class="resource-line">
                  <span class="info-label">工具</span>
                  <el-tag v-for="t in draft.tools" :key="t" style="margin-right:6px">{{ t }}</el-tag>
                </div>
                <div v-if="draft.mcps?.length" class="resource-line">
                  <span class="info-label">MCP</span>
                  <el-tag v-for="m in draft.mcps" :key="m" type="warning" style="margin-right:6px">{{ m }}</el-tag>
                </div>
                <div v-if="draft.skills?.length" class="resource-line">
                  <span class="info-label">Skills</span>
                  <el-tag v-for="s in draft.skills" :key="s" type="success" style="margin-right:6px">{{ s }}</el-tag>
                </div>
              </div>
            </div>

            <!-- 4. 示例对话 -->
            <div class="detail-section" v-if="draft.example_conversations?.length">
              <div class="section-title">💬 示例对话</div>
              <div class="section-desc">AI 模拟的典型交互场景，展示 Agent 的预期行为</div>
              <div v-for="(conv, i) in draft.example_conversations" :key="i" class="conv-block">
                <div class="conv-user">👤 {{ conv.user }}</div>
                <div class="conv-agent">🤖 {{ conv.agent }}</div>
              </div>
            </div>

            <!-- 5. 保存 -->
            <div class="detail-actions">
              <el-button type="success" size="large" @click="saveDetail">
                <el-icon><Check /></el-icon>&nbsp; 保存到 Agent 库
              </el-button>
              <el-button @click="reset">放弃</el-button>
            </div>
          </el-card>
        </template>

      </div>
    </transition>
  </div>
</template>

<style scoped>
.create-agent-page { max-width: 900px; margin: 0 auto; padding: 20px; }
.page-desc { color: #909399; margin-bottom: 20px; }
.input-card { margin-bottom: 16px; }
.preview-card { margin-top: 16px; }
.preview-hint { font-size: 12px; color: #909399; font-weight: normal; margin-left: 12px; }
.sp-item :deep(.el-textarea__inner) { min-height: 200px !important; font-family: monospace; font-size: 13px; }
.sp-hint { font-size: 12px; color: #909399; margin-top: 4px; }

/* 详细模式结构化卡片 */
.detail-preview :deep(.el-card__body) { padding: 0; }
.detail-section {
  padding: 20px 24px;
  border-bottom: 1px solid #ebeef5;
}
.detail-section:last-of-type { border-bottom: none; }
.section-title {
  font-size: 16px; font-weight: 700; color: #303133;
  margin-bottom: 8px;
}
.section-desc { font-size: 13px; color: #909399; margin-bottom: 12px; }
.info-row {
  display: flex; align-items: center; gap: 12px;
  padding: 6px 0; border-bottom: 1px solid #f5f5f5;
}
.info-row:last-child { border-bottom: none; }
.info-label { color: #909399; min-width: 70px; font-size: 13px; flex-shrink: 0; }
.info-value { font-size: 14px; }
.info-value-desc { flex: 1; font-size: 14px; color: #606266; }
.resource-line {
  display: flex; align-items: center; gap: 12px;
  padding: 6px 0;
}
.sp-block {
  background: #1a1a2e; color: #e0e0e0;
  padding: 14px 16px; border-radius: 6px;
  font-family: monospace; font-size: 13px;
  line-height: 1.6; white-space: pre-wrap;
  max-height: 400px; overflow-y: auto;
}
.conv-block {
  border-left: 3px solid #409eff;
  padding: 10px 14px; margin-bottom: 10px;
  background: #f5f7fa; border-radius: 4px;
}
.conv-user { font-weight: 600; color: #303133; margin-bottom: 4px; }
.conv-agent { color: #606266; }
.detail-actions { padding: 20px 24px; }

.fade-enter-active, .fade-leave-active { transition: opacity .3s ease; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
