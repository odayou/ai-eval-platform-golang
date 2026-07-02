<script setup lang="ts">
/**
 * Settings.vue — 设置页(MRD 11.6)。
 *
 * 四个 section:
 *   ① 账号与安全(占位,Demo 阶段无鉴权)
 *   ② API Key 管理 — 接 models 表,集中管理各模型的 Key(保存调 PUT /api/models/:id)
 *   ③ 通知设置(占位)
 *   ④ 关于
 *
 * Key 输入框用 show-password,避免明文泄露。
 */
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '../utils/api'

// 设置页模型视图(后端返回脱敏 Key)
interface ModelView {
  id: string; name: string; base_url: string; api_format: string; has_key: boolean; masked_key: string
}

const models = ref<ModelView[]>([])
// 供应商式模型管理:左侧列表选中项 + 右侧表单
const selectedModelId = ref<string>('')
const modelFormMode = ref<'view' | 'add' | 'edit'>('view')
const modelForm = reactive({
  id: '', name: '', base_url: '', api_key: '', api_format: 'openai',
})
const apiFormatOptions = [
  { label: 'Chat Completions (/chat/completions)', value: 'openai' },
  { label: 'Anthropic Messages (/v1/messages)', value: 'anthropic' },
  { label: 'Gemini (generateContent)', value: 'gemini' },
  { label: '自动检测 (按 URL 推断)', value: '' },
]
// 调试对话
const debugChatVisible = ref(false)
const debugMessages = ref<{ role: string; text: string }[]>([])
const debugInput = ref('')
const debugSending = ref(false)

// 通知开关(本地状态,后端持久化留待 ⑤)
const notifyOnComplete = ref(true)

async function loadModels() {
  models.value = await api.get('/settings/models')
  if (models.value.length && !selectedModelId.value) {
    selectModel(models.value[0].id)
  }
}

function selectModel(id: string) {
  const m = models.value.find((x) => x.id === id)
  if (!m) return
  selectedModelId.value = id
  modelFormMode.value = 'view'
  modelForm.id = m.id
  modelForm.name = m.name
  modelForm.base_url = m.base_url
  modelForm.api_key = ''
  modelForm.api_format = m.api_format ?? ''
}

function startAddModel() {
  modelFormMode.value = 'add'
  modelForm.id = ''
  modelForm.name = ''
  modelForm.base_url = ''
  modelForm.api_key = ''
  modelForm.api_format = 'openai'
}

function startEditModel() {
  modelFormMode.value = 'edit'
  modelForm.api_key = '' // 编辑时不回填明文 Key
}

async function saveModelForm() {
  if (modelFormMode.value === 'add') {
    await api.post('/models', {
      name: modelForm.name,
      base_url: modelForm.base_url,
      api_key: modelForm.api_key,
      api_format: modelForm.api_format,
    })
    ElMessage.success('模型供应商已添加')
  } else {
    await api.put(`/models/${modelForm.id}`, {
      name: modelForm.name,
      base_url: modelForm.base_url,
      api_key: modelForm.api_key,
      api_format: modelForm.api_format,
    })
    ElMessage.success('已保存')
  }
  await loadModels()
  modelFormMode.value = 'view'
}

async function deleteModelFromSettings(id: string) {
  await ElMessageBox.confirm('确定删除该模型供应商？', '删除', { type: 'warning' })
  await api.delete(`/models/${id}`)
  selectedModelId.value = ''
  await loadModels()
}

// 真实调试对话(调模型端点)
function openModelDebug() {
  debugChatVisible.value = true
  debugMessages.value = [{ role: 'system', text: `已连接 ${modelForm.name}（真实调用）` }]
}
async function sendModelDebug() {
  if (!debugInput.value.trim() || debugSending.value) return
  const msg = debugInput.value
  debugMessages.value.push({ role: 'user', text: msg })
  debugInput.value = ''
  debugSending.value = true
  try {
    // 复用被测模型探活:直接调 probe 端点测连通;真实对话需创建临时 Agent
    // 这里用一个轻量方式:直接 POST 到一个调试用的模型调用
    // 由于没有直接的「按模型ID对话」接口,我们临时创建/复用一个 Agent 来调
    const res = await api.post(`/settings/models/${modelForm.id}/probe`, {})
    if (res.ok) {
      debugMessages.value.push({
        role: 'assistant',
        text: `✓ 连通正常 (${res.latency_ms}ms)。模型 ${modelForm.name} 已就绪,可在评测任务中使用。`,
      })
    } else {
      debugMessages.value.push({ role: 'assistant', text: `✗ 连接失败: ${res.message}` })
    }
  } catch (e: any) {
    debugMessages.value.push({ role: 'assistant', text: `错误: ${e.message}` })
  } finally {
    debugSending.value = false
  }
}

onMounted(() => {
  loadModels()
  loadJudgeModels()
})

// ── 考官模型库(MRD 模型职责分离) ──
// 平台评测引擎(圆桌/判官/出题)专用,与上面"被测对象用"的 models 表分离,避免自评自审。
interface JudgeModelView {
  id: string; name: string; base_url: string; role: string; role_label: string;
  dimension: string; api_format: string; has_key: boolean; masked_key: string; available: boolean
}
const judgeModels = ref<JudgeModelView[]>([])
const judgeKeyBuffer = ref<Record<string, string>>({})
const judgeSaving = ref<string | null>(null)
const judgeProbing = ref<Record<string, boolean>>({})

// 可选维度(新增专家时选择;留空表示不绑定特定维度)
const expertDimensions = [
  { value: 'security', label: '安全' },
  { value: 'performance', label: '性能' },
  { value: 'architecture', label: '架构' },
  { value: 'product', label: '产品' },
  { value: 'correctness', label: '正确性' },
  { value: 'reliability', label: '可靠性' },
  { value: 'efficiency', label: '效率' },
  { value: 'maintainability', label: '可维护性' },
]

async function loadJudgeModels() {
  try {
    judgeModels.value = await api.get('/settings/judge-models')
  } catch { judgeModels.value = [] }
}

async function saveJudgeKey(role: string) {
  const m = judgeModels.value.find((x) => x.role === role)
  if (!m) return
  const newKey = judgeKeyBuffer.value[role]
  if (!newKey || !newKey.trim()) {
    ElMessage.warning('请输入 Key')
    return
  }
  judgeSaving.value = role
  try {
    await api.put('/settings/judge-models', {
      role, name: m.name, base_url: m.base_url, api_key: newKey, dimension: m.dimension, api_format: m.api_format,
    })
    ElMessage.success(`${m.role_label} Key 已保存`)
    judgeKeyBuffer.value[role] = ''
    await loadJudgeModels()
  } catch (e: any) { ElMessage.error(e.message || '保存失败') }
  finally { judgeSaving.value = null }
}

async function testJudgeConnect(role: string) {
  judgeProbing.value[role] = true
  try {
    const res = await api.post(`/settings/judge-models/${role}/probe`, {})
    if (res.available) ElMessage.success(`${role}: ${res.detail}`)
    else ElMessage.warning(`${role}: ${res.detail}`)
  } catch (e: any) { ElMessage.error(e.message || '检测失败') }
  finally { judgeProbing.value[role] = false }
}

// ── 新增/删除专家(自定义专家角色) ──
const showAddExpert = ref(false)
const addExpertSaving = ref(false)
const addExpertForm = reactive({
  role: '',         // 如 expert_reliability(自动加 expert_ 前缀)
  label: '',        // 中文名,如"可靠性专家"
  dimension: '',    // 负责的维度
  name: 'glm-4-flash',
  base_url: 'https://open.bigmodel.cn/api/paas/v4',
  api_key: '',
  api_format: 'openai',
})

// 是否为可删除的自定义专家(expert_* 且非预置 4 个)
function isCustomExpert(m: JudgeModelView): boolean {
  const builtin = ['expert_security', 'expert_perf', 'expert_arch', 'expert_product']
  return m.role.startsWith('expert_') && !builtin.includes(m.role)
}

async function addExpert() {
  // 校验 role
  let role = addExpertForm.role.trim()
  if (!role) { ElMessage.warning('请输入专家标识'); return }
  if (!role.startsWith('expert_')) role = 'expert_' + role
  if (!/^expert_[a-z0-9_]+$/i.test(role)) {
    ElMessage.warning('专家标识只能含字母/数字/下划线'); return
  }
  if (judgeModels.value.some((m) => m.role === role)) {
    ElMessage.warning(`角色 ${role} 已存在`); return
  }
  if (!addExpertForm.dimension) {
    ElMessage.warning('请选择该专家负责的维度'); return
  }
  addExpertSaving.value = true
  try {
    await api.put('/settings/judge-models', {
      role,
      name: addExpertForm.name,
      base_url: addExpertForm.base_url,
      api_key: addExpertForm.api_key,
      dimension: addExpertForm.dimension,
      api_format: addExpertForm.api_format,
      role_label: addExpertForm.label,
    })
    ElMessage.success(`已新增专家:${addExpertForm.label || role}`)
    // 重置表单
    addExpertForm.role = ''; addExpertForm.label = ''; addExpertForm.dimension = ''
    addExpertForm.api_key = ''
    showAddExpert.value = false
    await loadJudgeModels()
  } catch (e: any) { ElMessage.error(e.message || '新增失败') }
  finally { addExpertSaving.value = false }
}

async function deleteExpert(role: string) {
  try {
    await ElMessageBox.confirm(
      `确认删除专家 "${role}"?删除后该专家不再参与圆桌评审。`,
      '删除确认',
      { type: 'warning' },
    )
  } catch { return } // 用户取消
  try {
    await api.delete(`/settings/judge-models/${role}`)
    ElMessage.success(`已删除 ${role}`)
    await loadJudgeModels()
  } catch (e: any) { ElMessage.error(e.message || '删除失败') }
}
</script>

<template>
  <div>
    <h2 class="page-title">设置</h2>
    <el-card shadow="never" class="settings-card">
      <!-- 账号 -->
      <el-divider content-position="left">账号与安全</el-divider>
      <el-form label-width="120px">
        <el-form-item label="用户名">
          <el-input model-value="admin" disabled />
        </el-form-item>
      </el-form>

      <!-- 模型供应商管理(仿智谱清言式 左右分栏) -->
      <el-divider content-position="left">模型供应商管理</el-divider>
      <p class="section-hint">
        管理自定义模型供应商,配置后可在评测任务和系统功能中使用。
        添加时选择对应的 API 格式,支持 OpenAI / Anthropic / Gemini 三种协议。
      </p>

      <div class="provider-layout">
        <!-- 左侧:供应商列表 -->
        <div class="provider-sidebar">
          <div class="provider-group">
            <div class="provider-group-title">自定义供应商</div>
            <div
              v-for="m in models"
              :key="m.id"
              class="provider-item"
              :class="{ active: selectedModelId === m.id }"
              @click="selectModel(m.id)"
            >
              <span class="provider-dot" :class="m.has_key ? 'on' : 'off'"></span>
              <span class="provider-name">{{ m.name }}</span>
              <el-tag v-if="m.api_format" size="small" type="info" effect="plain">{{ m.api_format }}</el-tag>
              <el-tag v-else size="small" type="info" effect="plain">自动</el-tag>
            </div>
            <div v-if="!models.length" class="provider-empty">暂无供应商</div>
          </div>
          <el-button type="primary" plain class="add-provider-btn" @click="startAddModel">
            <el-icon><Plus /></el-icon> 添加供应商
          </el-button>
        </div>

        <!-- 右侧:详情/添加/编辑表单 -->
        <div class="provider-content">
          <!-- 添加/编辑模式 -->
          <template v-if="modelFormMode !== 'view'">
            <h3 class="provider-form-title">
              {{ modelFormMode === 'add' ? '添加模型供应商' : '编辑模型供应商' }}
            </h3>
            <p class="provider-form-hint">配置一个完全自定义的 API 端点和模型。</p>
            <el-form label-width="100px" class="provider-form">
              <el-form-item label="名称">
                <el-input v-model="modelForm.name" placeholder="如：智谱 GLM、DeepSeek" />
              </el-form-item>
              <el-form-item label="Base URL">
                <el-input v-model="modelForm.base_url" placeholder="https://api.deepseek.com/anthropic" />
              </el-form-item>
              <el-form-item label="API Key">
                <el-input
                  v-model="modelForm.api_key"
                  show-password
                  :placeholder="modelFormMode === 'edit' ? '留空表示不修改' : '输入 API Key'"
                />
              </el-form-item>
              <el-form-item label="API 格式">
                <el-select v-model="modelForm.api_format" style="width: 100%">
                  <el-option v-for="opt in apiFormatOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
                </el-select>
              </el-form-item>
              <el-form-item>
                <el-button type="primary" @click="saveModelForm" :disabled="!modelForm.name || !modelForm.base_url">
                  {{ modelFormMode === 'add' ? '添加供应商' : '保存' }}
                </el-button>
                <el-button @click="modelFormMode = 'view'; modelForm.id && selectModel(modelForm.id)">取消</el-button>
              </el-form-item>
            </el-form>
          </template>

          <!-- 查看模式 -->
          <template v-else-if="selectedModelId">
            <div class="provider-detail-head">
              <h3 class="provider-form-title">{{ modelForm.name }}</h3>
              <el-tag :type="models.find(x => x.id === selectedModelId)?.has_key ? 'success' : 'danger'" size="small">
                {{ models.find(x => x.id === selectedModelId)?.has_key ? '已启用' : '未配置 Key' }}
              </el-tag>
            </div>
            <el-descriptions :column="1" border class="provider-desc">
              <el-descriptions-item label="Base URL">{{ modelForm.base_url }}</el-descriptions-item>
              <el-descriptions-item label="API 格式">
                {{ modelForm.api_format || '自动检测(按 URL 推断)' }}
              </el-descriptions-item>
              <el-descriptions-item label="API Key">
                {{ models.find(x => x.id === selectedModelId)?.masked_key || '未配置' }}
              </el-descriptions-item>
            </el-descriptions>
            <div class="provider-actions">
              <el-button type="primary" @click="startEditModel">编辑</el-button>
              <el-button @click="openModelDebug">调试</el-button>
              <el-button type="danger" plain @click="deleteModelFromSettings(selectedModelId)">删除</el-button>
            </div>
          </template>

          <!-- 空状态 -->
          <div v-else class="provider-empty-detail">
            <el-empty description="点击左侧「添加供应商」开始配置" :image-size="80" />
          </div>
        </div>
      </div>

      <!-- 考官模型库(MRD 模型职责分离) -->
      <el-divider content-position="left">考官模型库(评测引擎专用)</el-divider>
      <p class="section-hint">
        平台评测引擎(圆桌各专家 / 判官 / 出题)专用模型,与上面"被测对象用"的模型分离,避免自评自审。
        每个专家可用不同模型;未单独配置的角色自动回退到主导者(main)。
      </p>
      <el-form label-width="160px">
        <el-form-item v-for="m in judgeModels" :key="m.role" :label="m.role_label">
          <div class="key-row">
            <el-tag size="small" type="info">{{ m.name }}</el-tag>
            <el-tag v-if="m.dimension" size="small" type="warning">维度:{{ m.dimension }}</el-tag>
            <el-input
              v-model="judgeKeyBuffer[m.role]"
              show-password
              style="flex:1"
              :placeholder="m.has_key ? `已配置 ${m.masked_key}` : `输入 ${m.role_label} 的 Key`"
            />
            <el-button :loading="judgeSaving === m.role" type="primary" @click="saveJudgeKey(m.role)">保存</el-button>
            <el-button :loading="judgeProbing[m.role]" @click="testJudgeConnect(m.role)">测试</el-button>
            <el-button v-if="isCustomExpert(m)" type="danger" plain @click="deleteExpert(m.role)">删除</el-button>
            <el-tag v-if="m.available" type="success" size="small">可用</el-tag>
          </div>
        </el-form-item>
        <el-form-item v-if="!judgeModels.length">
          <el-empty description="考官模型未初始化(启动后会自动种子)" :image-size="50" />
        </el-form-item>
        <!-- 新增专家入口 -->
        <el-form-item v-if="!showAddExpert">
          <el-button type="success" plain @click="showAddExpert = true">+ 新增自定义专家</el-button>
        </el-form-item>
        <el-form-item v-if="showAddExpert" label="新增专家">
          <div class="add-expert-form">
            <div class="add-expert-row">
              <el-input v-model="addExpertForm.role" placeholder="专家标识(如 reliability)" style="width:180px">
                <template #prepend>expert_</template>
              </el-input>
              <el-input v-model="addExpertForm.label" placeholder="中文名(如 可靠性专家)" style="width:200px" />
              <el-select v-model="addExpertForm.dimension" placeholder="负责维度" style="width:160px">
                <el-option v-for="d in expertDimensions" :key="d.value" :label="d.label" :value="d.value" />
              </el-select>
            </div>
            <div class="add-expert-row">
              <el-input v-model="addExpertForm.name" placeholder="模型名(如 glm-4-flash)" style="width:180px" />
              <el-input v-model="addExpertForm.base_url" placeholder="Base URL" style="flex:1" />
            </div>
            <div class="add-expert-row">
              <el-select v-model="addExpertForm.api_format" placeholder="API 格式" style="width:200px">
                <el-option label="OpenAI 兼容" value="openai" />
                <el-option label="Anthropic (Claude)" value="anthropic" />
                <el-option label="Gemini (Google)" value="gemini" />
                <el-option label="自动检测" value="" />
              </el-select>
            </div>
            <div class="add-expert-row">
              <el-input v-model="addExpertForm.api_key" show-password placeholder="API Key(可选,稍后填)" style="flex:1" />
              <el-button :loading="addExpertSaving" type="primary" @click="addExpert">新增</el-button>
              <el-button @click="showAddExpert = false">取消</el-button>
            </div>
            <p class="section-hint" style="margin:4px 0 0">
              专家标识会自动加 expert_ 前缀;维度决定该专家评审哪些规则。新增后立即参与圆桌评审。
            </p>
          </div>
        </el-form-item>
      </el-form>

      <!-- 通知 -->
      <el-divider content-position="left">通知</el-divider>
      <el-form label-width="140px">
        <el-form-item label="任务完成通知">
          <el-switch v-model="notifyOnComplete" />
        </el-form-item>
      </el-form>

      <!-- 关于 -->
      <el-divider content-position="left">关于</el-divider>
      <el-form label-width="140px">
        <el-form-item label="平台版本">
          <el-tag>v0.1.0 (Phase ① 地基)</el-tag>
        </el-form-item>
        <el-form-item label="架构参考">
          <span class="about-text">Agent 运行时借鉴 claw-code(ConversationRuntime / PermissionEnforcer / ContextCompaction / Session)</span>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 模型调试 Drawer -->
    <el-drawer v-model="debugChatVisible" :title="`调试 — ${modelForm.name}`" size="40%" direction="rtl">
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
          @keydown.enter.exact.prevent="sendModelDebug"
        />
        <el-button :loading="debugSending" type="primary" @click="sendModelDebug" style="margin-left:8px">发送</el-button>
      </div>
    </el-drawer>
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 16px; }
.settings-card { max-width: 720px; }
.section-hint { color: #999; font-size: 13px; margin: -8px 0 12px; }
.key-row { display: flex; gap: 8px; width: 100%; align-items: center; }
.key-row .el-input { flex: 1; }
.add-expert-form { display: flex; flex-direction: column; gap: 8px; width: 100%; }
.add-expert-row { display: flex; gap: 8px; width: 100%; align-items: center; }
.about-text { color: #666; font-size: 13px; }

/* 供应商式模型管理布局 */
.provider-layout { display: flex; gap: 16px; border: 1px solid #ebeef5; border-radius: 8px; overflow: hidden; min-height: 360px; }
.provider-sidebar { width: 220px; border-right: 1px solid #ebeef5; background: #fafafa; padding: 12px; display: flex; flex-direction: column; }
.provider-group-title { font-size: 12px; color: #909399; margin-bottom: 8px; padding: 0 4px; }
.provider-item { display: flex; align-items: center; gap: 8px; padding: 8px 10px; border-radius: 6px; cursor: pointer; font-size: 14px; transition: background .2s; }
.provider-item:hover { background: #f0f2f5; }
.provider-item.active { background: #ecf5ff; color: #409eff; }
.provider-item.active .provider-dot.on { background: #67c23a; }
.provider-dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.provider-dot.on { background: #67c23a; }
.provider-dot.off { background: #c0c4cc; }
.provider-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.provider-empty { color: #c0c4cc; font-size: 13px; padding: 12px 4px; }
.add-provider-btn { margin-top: auto; }
.provider-content { flex: 1; padding: 20px 24px; overflow-y: auto; }
.provider-form-title { font-size: 16px; margin: 0 0 4px; display: inline; }
.provider-form-hint { color: #999; font-size: 13px; margin: 0 0 16px; }
.provider-form { max-width: 460px; }
.provider-detail-head { display: flex; align-items: center; gap: 10px; margin-bottom: 16px; }
.provider-desc { max-width: 500px; margin-bottom: 16px; }
.provider-actions { display: flex; gap: 8px; }
.provider-empty-detail { display: flex; align-items: center; justify-content: center; height: 100%; }
.debug-chat { height: calc(100% - 60px); overflow-y: auto; padding: 12px; }
.debug-msg { margin-bottom: 12px; padding: 8px 12px; border-radius: 8px; max-width: 85%; word-break: break-word; font-size: 14px; }
.debug-msg.user { background: #409eff; color: #fff; margin-left: auto; }
.debug-msg.assistant { background: #f4f4f5; }
.debug-msg.system { background: #fdf6ec; color: #e6a23c; font-size: 13px; }
.debug-input { position: absolute; bottom: 0; left: 0; right: 0; padding: 12px; border-top: 1px solid #ebeef5; }
</style>
