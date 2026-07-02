<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../utils/api'

const activeTab = ref('agents')

// ── Shared ──
function iconColor(builtin: boolean) {
  return builtin ? '#67c23a' : '#409eff'
}

// ══════════════════════════════════════════
// Agents
// ══════════════════════════════════════════
const agents = ref<any[]>([])
const agentDialog = ref(false)
const agentForm = ref({ name: '', adapter: 'api', model: '', tools: '', permission_level: 'workspace-write' })
const debugDrawer = ref(false)
const debugTarget = ref<any>(null)
const debugMessages = ref<{ role: string; text: string }[]>([])
const debugInput = ref('')
const debugSending = ref(false)

async function loadAgents() { agents.value = await api.get('/agents') }
async function createAgent() {
  await api.post('/agents', agentForm.value)
  agentDialog.value = false
  agentForm.value = { name: '', adapter: 'api', model: '', tools: '', permission_level: 'workspace-write' }
  await loadAgents()
  ElMessage.success('Agent 已创建')
}
async function deleteAgent(id: string) {
  await ElMessageBox.confirm('确定删除该 Agent？', '删除', { type: 'warning' })
  await api.delete(`/agents/${id}`)
  await loadAgents()
}
function openDebug(agent: any) {
  debugTarget.value = agent
  debugMessages.value = [{ role: 'system', text: `已连接 ${agent.name}（真实调用 ${agent.model || '未配置模型'}）` }]
  debugDrawer.value = true
}
async function sendDebug() {
  if (!debugInput.value.trim() || debugSending.value) return
  const msg = debugInput.value
  debugMessages.value.push({ role: 'user', text: msg })
  debugInput.value = ''
  debugSending.value = true
  try {
    // 带上历史对话(多轮上下文),排除 system 消息
    const history = debugMessages.value
      .filter((m) => m.role === 'user' || m.role === 'assistant')
      .map((m) => ({ role: m.role, text: m.text }))
    // 最后一条是刚加的当前消息,从 history 去掉(后端用 message 字段单独传)
    history.pop()
    const res = await api.post(`/agents/${debugTarget.value.id}/debug`, { message: msg, history })
    if (res.ok) {
      debugMessages.value.push({ role: 'assistant', text: res.reply })
    } else {
      debugMessages.value.push({ role: 'assistant', text: `❌ 调用失败: ${res.error}` })
    }
  } catch (e: any) {
    debugMessages.value.push({ role: 'assistant', text: `❌ 错误: ${e.message}` })
  } finally {
    debugSending.value = false
  }
}

// ══════════════════════════════════════════
// Models
// ══════════════════════════════════════════
// 模型选项(供 Agent 库下拉选择,从大模型库加载)
const modelOptions = ref<any[]>([])
const models = ref<any[]>([])
const modelDialog = ref(false)
const modelDialogMode = ref<'add' | 'edit'>('add')
const modelForm = ref({ name: '', base_url: '', api_key: '', api_format: 'openai' })

const apiFormatOptions = [
  { label: 'OpenAI 兼容', value: 'openai' },
  { label: 'Anthropic (Claude)', value: 'anthropic' },
  { label: 'Gemini (Google)', value: 'gemini' },
  { label: '自动检测 (按 URL 推断)', value: '' },
]

async function loadModels() {
  models.value = await api.get('/models')
  // 同步填充模型选项(供 Agent 库下拉用)
  modelOptions.value = models.value.map((m: any) => ({ label: m.name, value: m.name, api_format: m.api_format }))
}

function openAddModel() {
  modelDialogMode.value = 'add'
  modelForm.value = { name: '', base_url: '', api_key: '', api_format: 'openai' }
  modelDialog.value = true
}

function openEditModel(m: any) {
  modelDialogMode.value = 'edit'
  modelForm.value = {
    name: m.name || '',
    base_url: m.base_url || '',
    api_key: '', // 编辑时不回填明文 Key(后端不返回),留空表示不改
    api_format: m.api_format ?? '',
  }
  ;(modelForm.value as any)._id = m.id
  modelDialog.value = true
}

async function submitModel() {
  if (modelDialogMode.value === 'add') {
    await api.post('/models', {
      name: modelForm.value.name,
      base_url: modelForm.value.base_url,
      api_key: modelForm.value.api_key,
      api_format: modelForm.value.api_format,
    })
    ElMessage.success('模型已添加')
  } else {
    const id = (modelForm.value as any)._id
    await api.put(`/models/${id}`, {
      name: modelForm.value.name,
      base_url: modelForm.value.base_url,
      api_key: modelForm.value.api_key, // 留空则后端会用空串覆盖(已知行为)
      api_format: modelForm.value.api_format,
    })
    ElMessage.success('模型已更新')
  }
  modelDialog.value = false
  await loadModels()
}

async function deleteModel(id: string) {
  await ElMessageBox.confirm('确定删除该模型？', '删除', { type: 'warning' })
  await api.delete(`/models/${id}`)
  await loadModels()
}

// 模型连通性测试
const modelProbing = ref<Record<string, boolean>>({})
async function testModel(m: any) {
  modelProbing.value[m.id] = true
  try {
    const res = await api.post(`/settings/models/${m.id}/probe`, {})
    if (res.ok) {
      ElMessage.success(`${m.name}: ✅ 连接成功 (${res.latency_ms}ms)`)
      m.available = true
    } else {
      ElMessage.warning(`${m.name}: ❌ ${res.message || '连接失败'}`)
      m.available = false
    }
  } catch (e: any) {
    ElMessage.error(e.message || '测试失败')
  } finally {
    modelProbing.value[m.id] = false
  }
}

// 模型调试(自动/手动)
const modelDebugVisible = ref(false)
const modelDebugTarget = ref<any>(null)
const modelDebugAuto = ref(false) // true=自动模式
const modelDebugInput = ref('')
const modelDebugMessages = ref<{ role: string; text: string }[]>([])
const modelDebugSending = ref(false)

function openModelDebug(m: any) {
  modelDebugTarget.value = m
  // 弹出模式选择
  ElMessageBox.alert('', '选择调试方式', {
    confirmButtonText: '自动调试',
    cancelButtonText: '手动调试',
    showCancelButton: true,
    type: 'info',
    callback: async (action: string) => {
      const isAuto = action === 'confirm'
      modelDebugAuto.value = isAuto
      modelDebugMessages.value = [{ role: 'system', text: `调试模型: ${m.name} (${m.api_format || '自动检测'})` }]
      modelDebugVisible.value = true
      if (isAuto) {
        modelDebugInput.value = ''
        // 自动发送"你好"
        await sendModelDebug('你好')
      }
    },
  })
}

async function sendModelDebug(msg?: string) {
  const text = msg || modelDebugInput.value
  if (!text.trim() || modelDebugSending.value) return
  modelDebugMessages.value.push({ role: 'user', text })
  if (!msg) modelDebugInput.value = ''
  modelDebugSending.value = true
  try {
    const m = modelDebugTarget.value
    const res = await api.post(`/models/${m.id}/debug`, { message: text })
    if (res.ok) {
      modelDebugMessages.value.push({ role: 'assistant', text: res.reply })
    } else {
      modelDebugMessages.value.push({ role: 'assistant', text: `❌ 调用失败: ${res.error}` })
    }
  } catch (e: any) {
    modelDebugMessages.value.push({ role: 'assistant', text: `❌ 错误: ${e.message}` })
  } finally {
    modelDebugSending.value = false
  }
}

// ══════════════════════════════════════════
// Tools
// ══════════════════════════════════════════
const tools = ref<any[]>([])
const toolDialog = ref(false)
const toolForm = ref({ name: '', description: '', params_schema: '', tags: '' })

async function loadTools() { tools.value = await api.get('/tools') }
async function createTool() {
  await api.post('/tools', toolForm.value)
  toolDialog.value = false
  toolForm.value = { name: '', description: '', params_schema: '', tags: '' }
  await loadTools()
  ElMessage.success('Tool 已创建')
}
async function deleteTool(id: string) {
  await ElMessageBox.confirm('确定删除该 Tool？', '删除', { type: 'warning' })
  await api.delete(`/tools/${id}`)
  await loadTools()
}

// ══════════════════════════════════════════
// MCP Servers
// ══════════════════════════════════════════
const mcpServers = ref<any[]>([])
const mcpDialog = ref(false)
const mcpForm = ref({ name: '', protocol: 'stdio', endpoint: '' })

async function loadMCP() { mcpServers.value = await api.get('/mcp-servers') }
async function createMCP() {
  await api.post('/mcp-servers', mcpForm.value)
  mcpDialog.value = false
  mcpForm.value = { name: '', protocol: 'stdio', endpoint: '' }
  await loadMCP()
  ElMessage.success('MCP Server 已添加')
}
async function deleteMCP(id: string) {
  await ElMessageBox.confirm('确定删除？', '删除', { type: 'warning' })
  await api.delete(`/mcp-servers/${id}`)
  await loadMCP()
}

// ══════════════════════════════════════════
// Skills
// ══════════════════════════════════════════
const skills = ref<any[]>([])
const skillDialog = ref(false)
const skillForm = ref({ name: '', description: '', tool_ids: '', tags: '' })

async function loadSkills() { skills.value = await api.get('/skills') }
async function createSkill() {
  await api.post('/skills', skillForm.value)
  skillDialog.value = false
  skillForm.value = { name: '', description: '', tool_ids: '', tags: '' }
  await loadSkills()
  ElMessage.success('Skill 已创建')
}
async function deleteSkill(id: string) {
  await ElMessageBox.confirm('确定删除？', '删除', { type: 'warning' })
  await api.delete(`/skills/${id}`)
  await loadSkills()
}

// ══════════════════════════════════════════
// Model Config
// ══════════════════════════════════════════
const modelConfig = ref<any[]>([])
const configDialog = ref(false)
const configForm = ref({ role: 'main', model_id: '' })

const configRoles = [
  { value: 'main', label: '主 Agent' },
  { value: 'expert_security', label: '安全专家' },
  { value: 'expert_perf', label: '性能专家' },
  { value: 'expert_arch', label: '架构专家' },
  { value: 'expert_product', label: '产品专家' },
  { value: 'question_gen', label: '出题 Agent' },
  { value: 'vision', label: '视觉解析' },
  { value: 'rag', label: 'RAG 检索' },
]

async function loadModelConfig() { modelConfig.value = await api.get('/model-config') }
async function saveModelConfig() {
  await api.put('/model-config', configForm.value)
  configDialog.value = false
  await loadModelConfig()
  ElMessage.success('配置已保存')
}

// ══════════════════════════════════════════
// Prompt Templates
// ══════════════════════════════════════════
const prompts = ref<any[]>([])
const promptDialog = ref(false)
const promptForm = ref({ name: '', content: '', model_hint: '' })

async function loadPrompts() { prompts.value = await api.get('/prompt-templates') }
async function createPrompt() {
  await api.post('/prompt-templates', promptForm.value)
  promptDialog.value = false
  promptForm.value = { name: '', content: '', model_hint: '' }
  await loadPrompts()
  ElMessage.success('模板已创建')
}
async function deletePrompt(id: string) {
  await ElMessageBox.confirm('确定删除？', '删除', { type: 'warning' })
  await api.delete(`/prompt-templates/${id}`)
  await loadPrompts()
}

// ── Init ──
const pageLoading = ref(false)
onMounted(async () => {
  pageLoading.value = true
  try {
    await Promise.all([
      loadAgents(), loadModels(), loadTools(), loadMCP(),
      loadSkills(), loadModelConfig(), loadPrompts(), loadExpertGroups(),
    ])
  } catch (e: any) {
    ElMessage.error('资源加载失败: ' + (e.message || '网络错误'))
  } finally {
    pageLoading.value = false
  }
})

// ── 专家组 ──
interface ExpertGroup {
  id: string; name: string; description: string; status: string; member_count: number
}
interface EgMember {
  member_type: string; member_ref: string; dimension: string; role_label: string
  weight: number; prompt_text?: string; prompt_template_id?: string
}
const expertGroups = ref<ExpertGroup[]>([])
const egDialog = ref(false)
const egForm = ref<{ id?: string; name: string; description: string; members: EgMember[] }>({
  name: '', description: '', members: []
})
const egExpandedPrompt = ref('')
const aiGenDialog = ref(false)
const aiGenForm = ref({ scenario: '', count: 4 })
const aiGenLoading = ref(false)
const aiGenNote = ref('')

async function loadExpertGroups() {
  try { expertGroups.value = await api.get('/expert-groups') } catch { expertGroups.value = [] }
}

function openExpertGroupDialog(g?: ExpertGroup) {
  if (g) {
    // 编辑:加载详情
    api.get(`/expert-groups/${g.id}`).then((res: any) => {
      egForm.value = {
        id: res.group.id, name: res.group.name, description: res.group.description || '',
        members: (res.members || []).map((m: any) => ({
          member_type: m.member_type, member_ref: m.member_ref, dimension: m.dimension,
          role_label: m.role_label, weight: m.weight, prompt_text: m.prompt_text,
          prompt_template_id: m.prompt_template_id,
        })),
      }
      egExpandedPrompt.value = egForm.value.members[0]?.prompt_text || ''
      egDialog.value = true
    })
  } else {
    egForm.value = { name: '', description: '', members: [
      { member_type: 'role', member_ref: '', dimension: '', role_label: '', weight: 1.0 }
    ] }
    egExpandedPrompt.value = ''
    egDialog.value = true
  }
}

async function saveExpertGroup() {
  if (!egForm.value.name.trim()) { ElMessage.warning('请输入名称'); return }
  // 把展开的提示词回填到第一个成员(简化版)
  if (egForm.value.members.length > 0 && egExpandedPrompt.value) {
    egForm.value.members[0].prompt_text = egExpandedPrompt.value
  }
  try {
    if (egForm.value.id) {
      await api.put(`/expert-groups/${egForm.value.id}`, {
        name: egForm.value.name, description: egForm.value.description,
        status: 'active', members: egForm.value.members,
      })
      ElMessage.success('已更新')
    } else {
      await api.post('/expert-groups', {
        name: egForm.value.name, description: egForm.value.description,
        status: 'active', members: egForm.value.members,
      })
      ElMessage.success('已创建')
    }
    egDialog.value = false
    await loadExpertGroups()
  } catch (e: any) { ElMessage.error(e.message || '保存失败') }
}

async function deleteExpertGroup(id: string) {
  try {
    await api.delete(`/expert-groups/${id}`)
    ElMessage.success('已删除')
    await loadExpertGroups()
  } catch (e: any) { ElMessage.error(e.message || '删除失败') }
}

async function generateExpertGroup() {
  if (!aiGenForm.value.scenario.trim()) { ElMessage.warning('请输入评测场景'); return }
  aiGenLoading.value = true
  aiGenNote.value = ''
  try {
    const draft = await api.post('/expert-groups/ai-generate', aiGenForm.value)
    // 把草稿填入主对话框供编辑
    egForm.value = {
      name: draft.name || '', description: draft.description || '',
      members: (draft.members || []).map((m: any) => ({
        member_type: m.member_type || 'role', member_ref: m.member_ref || '',
        dimension: m.dimension || '', role_label: m.role_label || '',
        weight: m.weight || 1.0, prompt_text: m.prompt_text || '',
      })),
    }
    egExpandedPrompt.value = egForm.value.members[0]?.prompt_text || ''
    aiGenDialog.value = false
    egDialog.value = true
    ElMessage.success(draft.ai_generated ? 'AI 已生成草稿,请确认后保存' : '返回模板草稿,请编辑后保存')
    if (draft.note) aiGenNote.value = draft.note
  } catch (e: any) {
    ElMessage.error(e.message || '生成失败')
  } finally { aiGenLoading.value = false }
}

// 权限级别展示(MRD §5.6)
function permLabel(level: string): string {
  return { 'read-only': '只读', 'workspace-write': '工作区写', 'full': '完全放行' }[level || 'workspace-write'] || level || '工作区写'
}
function permTagType(level: string): string {
  return { 'read-only': 'success', 'workspace-write': 'warning', 'full': 'danger' }[level || 'workspace-write'] || 'info'
}

// 切换 Agent 上下线
async function toggleOnline(a: any) {
  try {
    const newState = !a.online
    await api.put(`/agents/${a.id}/online`, { online: newState })
    a.online = newState
    ElMessage.success(newState ? '已上线' : '已下线')
  } catch (e: any) {
    ElMessage.error(e.message || '操作失败')
  }
}

// Tab change handler
function onTabChange(tab: string) {
  activeTab.value = tab
}
</script>

<template>
  <div v-loading="pageLoading" element-loading-text="加载资源中...">
    <h2 class="page-title">资源管理中心</h2>

    <el-tabs v-model="activeTab" @tab-change="onTabChange">

      <!-- ════ Agent 库 ════ -->
      <el-tab-pane label="Agent 库" name="agents">
        <div class="tab-header">
          <el-button type="primary" @click="agentDialog = true">
            <el-icon><Plus /></el-icon> 新建 Agent
          </el-button>
        </div>
        <el-row :gutter="16">
          <el-col :span="8" v-for="a in agents" :key="a.id">
            <el-card shadow="hover" class="resource-card">
              <div class="card-header">
                <el-icon :size="20" :color="iconColor(false)"><Cpu /></el-icon>
                <span class="card-name">{{ a.name }}</span>
                <el-tag size="small" :type="a.online ? 'success' : (a.model ? 'info' : 'danger')">
                  {{ a.online ? '在线' : (a.model ? '离线' : '未配模型') }}
                </el-tag>
              </div>
              <div class="card-meta">接入方式：{{ a.adapter }}</div>
              <div class="card-meta">模型：{{ a.model || '未配置' }}</div>
              <div class="card-meta">
                权限：
                <el-tag size="small" :type="permTagType(a.permission_level)">
                  {{ permLabel(a.permission_level) }}
                </el-tag>
              </div>
              <div class="card-actions">
                <el-button size="small" @click="openDebug(a)">调试</el-button>
                <el-button
                  size="small"
                  :type="a.online ? 'warning' : 'success'"
                  :disabled="!a.model"
                  @click="toggleOnline(a)"
                >
                  {{ a.online ? '下线' : '上线' }}
                </el-button>
                <el-button size="small" type="danger" text @click="deleteAgent(a.id)">删除</el-button>
              </div>
            </el-card>
          </el-col>
          <el-col :span="8" v-if="!agents.length">
            <el-empty description="暂无 Agent" />
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- ════ 大模型库 ════ -->
      <el-tab-pane label="大模型库" name="models">
        <div class="tab-header">
          <el-button type="primary" @click="openAddModel">
            <el-icon><Plus /></el-icon> 添加模型
          </el-button>
        </div>
        <el-row :gutter="16">
          <el-col :span="8" v-for="m in models" :key="m.id">
            <el-card shadow="hover" class="resource-card">
              <div class="card-header">
                <el-icon :size="20" color="#e6a23c"><Connection /></el-icon>
                <span class="card-name">{{ m.name }}</span>
                <el-tag size="small" :type="m.available ? 'success' : 'danger'">
                  {{ m.available ? '可用' : '不可用' }}
                </el-tag>
              </div>
              <div class="card-meta">
                <el-tag size="small" type="info" effect="plain" style="margin-right: 6px">
                  {{ m.api_format ? m.api_format : '自动检测' }}
                </el-tag>
                Endpoint：{{ m.base_url }}
              </div>
              <div class="card-actions">
                <el-button size="small" @click="openEditModel(m)">编辑</el-button>
                <el-button size="small" type="success" plain @click="testModel(m)">测试</el-button>
                <el-button size="small" @click="openModelDebug(m)">调试</el-button>
                <el-button size="small" type="danger" text @click="deleteModel(m.id)">删除</el-button>
              </div>
            </el-card>
          </el-col>
          <el-col :span="8" v-if="!models.length">
            <el-empty description="暂无模型" />
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- ════ Tools ════ -->
      <el-tab-pane label="Tools" name="tools">
        <div class="tab-header">
          <el-button type="primary" @click="toolDialog = true">
            <el-icon><Plus /></el-icon> 新建 Tool
          </el-button>
        </div>
        <el-row :gutter="16">
          <el-col :span="8" v-for="t in tools" :key="t.id">
            <el-card shadow="hover" class="resource-card">
              <div class="card-header">
                <el-icon :size="20" color="#409eff"><SetUp /></el-icon>
                <span class="card-name">{{ t.name }}</span>
                <el-tag v-if="t.builtin" size="small" type="success">内置</el-tag>
              </div>
              <div class="card-desc">{{ t.description || '无描述' }}</div>
              <div class="card-actions">
                <el-button size="small" type="danger" text @click="deleteTool(t.id)">删除</el-button>
              </div>
            </el-card>
          </el-col>
          <el-col :span="8" v-if="!tools.length">
            <el-empty description="暂无 Tool" />
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- ════ MCP Server ════ -->
      <el-tab-pane label="MCP Server" name="mcp">
        <div class="tab-header">
          <el-button type="primary" @click="mcpDialog = true">
            <el-icon><Plus /></el-icon> 添加 MCP Server
          </el-button>
        </div>
        <el-row :gutter="16">
          <el-col :span="8" v-for="s in mcpServers" :key="s.id">
            <el-card shadow="hover" class="resource-card">
              <div class="card-header">
                <el-icon :size="20" color="#909399"><Link /></el-icon>
                <span class="card-name">{{ s.name }}</span>
                <el-tag size="small" :type="s.status === 'online' ? 'success' : 'danger'">
                  {{ s.status }}
                </el-tag>
              </div>
              <div class="card-meta">协议：{{ s.protocol }}</div>
              <div class="card-meta" v-if="s.endpoint">地址：{{ s.endpoint }}</div>
              <div class="card-actions">
                <el-button size="small" type="danger" text @click="deleteMCP(s.id)">删除</el-button>
              </div>
            </el-card>
          </el-col>
          <el-col :span="8" v-if="!mcpServers.length">
            <el-empty description="暂无 MCP Server" />
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- ════ Skills ════ -->
      <el-tab-pane label="Skills" name="skills">
        <div class="tab-header">
          <el-button type="primary" @click="skillDialog = true">
            <el-icon><Plus /></el-icon> 新建 Skill
          </el-button>
        </div>
        <el-row :gutter="16">
          <el-col :span="8" v-for="s in skills" :key="s.id">
            <el-card shadow="hover" class="resource-card">
              <div class="card-header">
                <el-icon :size="20" color="#67c23a"><MagicStick /></el-icon>
                <span class="card-name">{{ s.name }}</span>
                <el-tag v-if="s.builtin" size="small" type="success">内置</el-tag>
              </div>
              <div class="card-desc">{{ s.description || '无描述' }}</div>
              <div class="card-actions">
                <el-button size="small" type="danger" text @click="deleteSkill(s.id)">删除</el-button>
              </div>
            </el-card>
          </el-col>
          <el-col :span="8" v-if="!skills.length">
            <el-empty description="暂无 Skill" />
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- ════ 模型配置 ════ -->
      <el-tab-pane label="模型配置" name="modelConfig">
        <div class="tab-header">
          <el-button type="primary" @click="configDialog = true">
            <el-icon><Plus /></el-icon> 配置角色模型
          </el-button>
        </div>
        <el-table :data="modelConfig" stripe style="width: 100%; max-width: 700px">
          <el-table-column prop="role" label="角色" width="200">
            <template #default="{ row }">
              {{ configRoles.find(r => r.value === row.role)?.label || row.role }}
            </template>
          </el-table-column>
          <el-table-column prop="model_id" label="模型" />
          <el-table-column prop="updated_at" label="更新时间" width="200">
            <template #default="{ row }">{{ row.updated_at?.slice(0, 19) }}</template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- ════ Prompt 模板库 ════ -->
      <el-tab-pane label="Prompt 模板库" name="prompts">
        <div class="tab-header">
          <el-button type="primary" @click="promptDialog = true">
            <el-icon><Plus /></el-icon> 新建模板
          </el-button>
        </div>
        <el-row :gutter="16">
          <el-col :span="8" v-for="p in prompts" :key="p.id">
            <el-card shadow="hover" class="resource-card">
              <div class="card-header">
                <el-icon :size="20" color="#f56c6c"><Notebook /></el-icon>
                <span class="card-name">{{ p.name }}</span>
                <el-tag size="small">v{{ p.version }}</el-tag>
              </div>
              <div class="card-meta" v-if="p.model_hint">适配模型：{{ p.model_hint }}</div>
              <div class="card-desc">{{ p.content?.slice(0, 80) }}...</div>
              <div class="card-actions">
                <el-button size="small" type="danger" text @click="deletePrompt(p.id)">删除</el-button>
              </div>
            </el-card>
          </el-col>
          <el-col :span="8" v-if="!prompts.length">
            <el-empty description="暂无模板" />
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- ════ 专家组 ════ -->
      <el-tab-pane label="专家组" name="expertGroups">
        <div class="tab-header">
          <el-button type="primary" @click="openExpertGroupDialog()">
            <el-icon><Plus /></el-icon> 新建专家组
          </el-button>
          <el-button @click="aiGenDialog = true" type="success" plain>
            <el-icon><MagicStick /></el-icon> 让 AI 创建
          </el-button>
        </div>
        <el-row :gutter="16">
          <el-col :span="8" v-for="g in expertGroups" :key="g.id">
            <el-card shadow="hover" class="resource-card">
              <div class="card-header">
                <el-icon :size="20" color="#67c23a"><UserFilled /></el-icon>
                <span class="card-name">{{ g.name }}</span>
                <el-tag size="small" :type="g.status === 'draft' ? 'warning' : 'success'">
                  {{ g.status === 'draft' ? '草稿' : '可用' }}
                </el-tag>
              </div>
              <div class="card-meta">{{ g.member_count }} 个成员</div>
              <div class="card-desc" v-if="g.description">{{ g.description }}</div>
              <div class="card-actions">
                <el-button size="small" @click="openExpertGroupDialog(g)">编辑</el-button>
                <el-button size="small" type="danger" text @click="deleteExpertGroup(g.id)" v-if="g.id !== 'eg-default'">删除</el-button>
              </div>
            </el-card>
          </el-col>
          <el-col :span="8" v-if="!expertGroups.length">
            <el-empty description="暂无专家组" />
          </el-col>
        </el-row>
      </el-tab-pane>
    </el-tabs>

    <!-- ═══════════ Dialogs ═══════════ -->

    <!-- 专家组 Dialog -->
    <el-dialog v-model="egDialog" :title="egForm.id ? '编辑专家组' : '新建专家组'" width="780px" top="5vh">
      <el-form label-width="90px">
        <el-form-item label="名称"><el-input v-model="egForm.name" placeholder="如：SQL评测专家组" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="egForm.description" placeholder="一句话描述" /></el-form-item>
        <el-form-item label="成员">
          <el-table :data="egForm.members" border size="small" style="width:100%">
            <el-table-column label="类型" width="110">
              <template #default="{ row }">
                <el-select v-model="row.member_type" size="small">
                  <el-option label="模型(role)" value="role" />
                  <el-option label="Agent" value="agent" />
                  <el-option label="考官model" value="model" />
                </el-select>
              </template>
            </el-table-column>
            <el-table-column label="引用" min-width="130">
              <template #default="{ row }">
                <el-input v-model="row.member_ref" size="small"
                  :placeholder="row.member_type === 'agent' ? 'agents.id' : (row.member_type === 'model' ? 'judge_models.role' : 'expert_xxx')" />
              </template>
            </el-table-column>
            <el-table-column label="维度" width="110">
              <template #default="{ row }">
                <el-input v-model="row.dimension" size="small" placeholder="security" />
              </template>
            </el-table-column>
            <el-table-column label="角色名" width="100">
              <template #default="{ row }">
                <el-input v-model="row.role_label" size="small" placeholder="安全专家" />
              </template>
            </el-table-column>
            <el-table-column label="权重" width="80">
              <template #default="{ row }">
                <el-input-number v-model="row.weight" :min="0" :max="5" :step="0.1" size="small" controls-position="right" style="width:70px" />
              </template>
            </el-table-column>
            <el-table-column label="操作" width="70">
              <template #default="{ $index }">
                <el-button size="small" type="danger" text @click="egForm.members.splice($index, 1)">删</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-button size="small" @click="egForm.members.push({ member_type: 'role', member_ref: '', dimension: '', role_label: '', weight: 1.0 })" style="margin-top:8px">
            <el-icon><Plus /></el-icon> 添加成员
          </el-button>
        </el-form-item>
        <el-form-item v-if="egForm.members.length > 0" label="提示词">
          <el-input type="textarea" :rows="4" v-model="egExpandedPrompt"
            placeholder="第一个成员的评审提示词(留空用规则自动拼 rubric)" />
          <div class="form-tip">提示词优先级:引用模板 > 成员内嵌 > 自动拼装。高级编辑可在创建后逐成员配置。</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="egDialog = false">取消</el-button>
        <el-button type="primary" @click="saveExpertGroup">保存</el-button>
      </template>
    </el-dialog>

    <!-- AI 生成专家组 Dialog -->
    <el-dialog v-model="aiGenDialog" title="让 AI 创建专家组" width="520px">
      <el-form label-width="100px">
        <el-form-item label="评测场景">
          <el-input v-model="aiGenForm.scenario" placeholder="如：数据库SQL评测 / 前端组件评测" />
        </el-form-item>
        <el-form-item label="成员数量">
          <el-input-number v-model="aiGenForm.count" :min="2" :max="8" />
        </el-form-item>
      </el-form>
      <el-alert v-if="aiGenNote" :title="aiGenNote" type="info" :closable="false" style="margin-top:12px" />
      <template #footer>
        <el-button @click="aiGenDialog = false">取消</el-button>
        <el-button type="success" :loading="aiGenLoading" @click="generateExpertGroup">生成草稿</el-button>
      </template>
    </el-dialog>

    <!-- Agent Dialog -->
    <el-dialog v-model="agentDialog" title="新建 Agent" width="480px">
      <el-form label-width="100px">
        <el-form-item label="名称"><el-input v-model="agentForm.name" placeholder="如：CodeGen Agent" /></el-form-item>
        <el-form-item label="接入方式">
          <el-select v-model="agentForm.adapter" style="width: 100%">
            <el-option label="API" value="api" />
            <el-option label="本地源码" value="source" />
            <el-option label="终端托管" value="terminal" />
          </el-select>
        </el-form-item>
        <el-form-item label="模型">
          <el-select
            v-model="agentForm.model"
            filterable
            allow-create
            default-first-option
            placeholder="从大模型库选择，或直接输入模型名"
            style="width: 100%"
          >
            <el-option
              v-for="opt in modelOptions"
              :key="opt.value"
              :label="opt.label + (opt.api_format ? ' (' + opt.api_format + ')' : '')"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="权限级别">
          <el-select v-model="agentForm.permission_level" style="width: 100%">
            <el-option label="只读（read-only）" value="read-only" />
            <el-option label="工作区写（workspace-write）" value="workspace-write" />
            <el-option label="完全放行（full）" value="full" />
          </el-select>
          <div class="form-tip">被测 Agent 执行代码/命令的权限级别（终端与沙箱轨道生效）。</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="agentDialog = false">取消</el-button>
        <el-button type="primary" @click="createAgent" :disabled="!agentForm.name">创建</el-button>
      </template>
    </el-dialog>

    <!-- Model Dialog -->
    <el-dialog v-model="modelDialog" :title="modelDialogMode === 'add' ? '添加模型' : '编辑模型'" width="480px">
      <el-form label-width="100px">
        <el-form-item label="API 格式">
          <el-select v-model="modelForm.api_format" style="width: 100%">
            <el-option v-for="opt in apiFormatOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="名称"><el-input v-model="modelForm.name" placeholder="如：GLM-5.2" /></el-form-item>
        <el-form-item label="Base URL"><el-input v-model="modelForm.base_url" placeholder="https://open.bigmodel.cn/api/paas/v4" /></el-form-item>
        <el-form-item label="API Key">
          <el-input
            v-model="modelForm.api_key"
            show-password
            :placeholder="modelDialogMode === 'edit' ? '留空表示不修改' : 'sk-...'"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="modelDialog = false">取消</el-button>
        <el-button type="primary" @click="submitModel" :disabled="!modelForm.name || !modelForm.base_url">
          {{ modelDialogMode === 'add' ? '添加' : '保存' }}
        </el-button>
      </template>
	    </el-dialog>

    <!-- 模型调试 Drawer -->
    <el-drawer v-model="modelDebugVisible" :title="`调试 — ${modelDebugTarget?.name || ''}`" size="40%" direction="rtl">
      <div class="debug-chat">
        <div v-for="(msg, i) in modelDebugMessages" :key="i" :class="['debug-msg', msg.role]">
          <strong v-if="msg.role !== 'user'">{{ msg.role === 'system' ? '🔔' : '🤖' }}</strong>
          <span>{{ msg.text }}</span>
        </div>
      </div>
      <div class="debug-input">
        <el-input
          v-model="modelDebugInput"
          type="textarea"
          :rows="2"
          resize="none"
          :placeholder="modelDebugAuto ? '自动模式(等待回复中...)' : 'Enter 发送, Shift+Enter 换行...'"
          :disabled="modelDebugSending || modelDebugAuto"
          @keydown.enter.exact.prevent="!modelDebugSending && sendModelDebug()"
        />
        <el-button
          :loading="modelDebugSending"
          type="primary"
          :disabled="modelDebugAuto"
          @click="sendModelDebug()"
          style="margin-left:8px"
        >发送</el-button>
      </div>
    </el-drawer>

	    <!-- Tool Dialog -->
    <el-dialog v-model="toolDialog" title="新建 Tool" width="480px">
      <el-form label-width="100px">
        <el-form-item label="名称"><el-input v-model="toolForm.name" placeholder="如：shell_exec" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="toolForm.description" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="参数 Schema"><el-input v-model="toolForm.params_schema" type="textarea" :rows="3" placeholder='{"type":"object","properties":{}}' /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="toolDialog = false">取消</el-button>
        <el-button type="primary" @click="createTool" :disabled="!toolForm.name">创建</el-button>
      </template>
    </el-dialog>

    <!-- MCP Dialog -->
    <el-dialog v-model="mcpDialog" title="添加 MCP Server" width="480px">
      <el-form label-width="100px">
        <el-form-item label="名称"><el-input v-model="mcpForm.name" placeholder="如：Docker Sandbox" /></el-form-item>
        <el-form-item label="协议">
          <el-select v-model="mcpForm.protocol" style="width: 100%">
            <el-option label="stdio" value="stdio" />
            <el-option label="SSE" value="sse" />
            <el-option label="Streamable" value="streamable" />
          </el-select>
        </el-form-item>
        <el-form-item label="Endpoint"><el-input v-model="mcpForm.endpoint" placeholder="如：http://localhost:3001" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="mcpDialog = false">取消</el-button>
        <el-button type="primary" @click="createMCP" :disabled="!mcpForm.name">添加</el-button>
      </template>
    </el-dialog>

    <!-- Skill Dialog -->
    <el-dialog v-model="skillDialog" title="新建 Skill" width="480px">
      <el-form label-width="100px">
        <el-form-item label="名称"><el-input v-model="skillForm.name" placeholder="如：代码安全审查" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="skillForm.description" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="skillDialog = false">取消</el-button>
        <el-button type="primary" @click="createSkill" :disabled="!skillForm.name">创建</el-button>
      </template>
    </el-dialog>

    <!-- Model Config Dialog -->
    <el-dialog v-model="configDialog" title="配置角色模型" width="480px">
      <el-form label-width="100px">
        <el-form-item label="角色">
          <el-select v-model="configForm.role" style="width: 100%">
            <el-option v-for="r in configRoles" :key="r.value" :label="r.label" :value="r.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="模型 ID"><el-input v-model="configForm.model_id" placeholder="如：glm-5.2" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="configDialog = false">取消</el-button>
        <el-button type="primary" @click="saveModelConfig" :disabled="!configForm.model_id">保存</el-button>
      </template>
    </el-dialog>

    <!-- Prompt Template Dialog -->
    <el-dialog v-model="promptDialog" title="新建 Prompt 模板" width="600px">
      <el-form label-width="100px">
        <el-form-item label="名称"><el-input v-model="promptForm.name" placeholder="如：安全专家 System Prompt" /></el-form-item>
        <el-form-item label="适配模型"><el-input v-model="promptForm.model_hint" placeholder="如：glm-5.2（可选）" /></el-form-item>
        <el-form-item label="内容"><el-input v-model="promptForm.content" type="textarea" :rows="8" placeholder="You are a security expert..." /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="promptDialog = false">取消</el-button>
        <el-button type="primary" @click="createPrompt" :disabled="!promptForm.name || !promptForm.content">创建</el-button>
      </template>
    </el-dialog>

    <!-- ═══════════ Debug Drawer ═══════════ -->
    <el-drawer v-model="debugDrawer" :title="`调试 — ${debugTarget?.name}`" size="40%" direction="rtl">
      <div class="debug-chat">
        <div v-for="(msg, i) in debugMessages" :key="i" :class="['debug-msg', msg.role]">
          <strong v-if="msg.role !== 'user'">{{ msg.role === 'system' ? '🔔' : '🤖' }}</strong>
          <span>{{ msg.text }}</span>
        </div>
      </div>
      <div class="debug-input">
        <el-input
          v-model="debugInput"
          type="textarea"
          :rows="2"
          resize="none"
          placeholder="发送消息测试(Enter 发送,Shift+Enter 换行)..."
          :disabled="debugSending"
          @keydown.enter.exact.prevent="sendDebug"
        />
        <el-button :loading="debugSending" type="primary" @click="sendDebug" style="margin-left:8px">发送</el-button>
      </div>
    </el-drawer>
  </div>
</template>

<style scoped>
.page-title {
  font-size: 20px;
  color: #1a1a2e;
  margin: 0 0 16px;
}
.tab-header {
  margin-bottom: 16px;
}
.resource-card {
  margin-bottom: 16px;
}
.card-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.card-name {
  font-weight: 600;
  font-size: 15px;
  flex: 1;
}
.card-meta {
  font-size: 13px;
  color: #999;
  margin-bottom: 4px;
}
.form-tip {
  font-size: 12px;
  color: #909399;
  line-height: 1.4;
  margin-top: 4px;
}
.card-desc {
  font-size: 13px;
  color: #666;
  margin-bottom: 8px;
  line-height: 1.5;
}
.card-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}

/* Debug drawer */
.debug-chat {
  flex: 1;
  overflow-y: auto;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 300px;
}
.debug-msg {
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.5;
}
.debug-msg.user {
  background: #ecf5ff;
  align-self: flex-end;
  max-width: 80%;
}
.debug-msg.system {
  background: #f0f9eb;
  color: #67c23a;
}
.debug-msg.assistant {
  background: #f4f4f5;
}
.debug-input {
  padding: 12px;
  border-top: 1px solid #eee;
}
</style>
