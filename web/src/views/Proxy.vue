<script setup lang="ts">
// Proxy.vue — MITM 代理管理页(MRD v2 5.1 协议逆向代理轨道)。
//
// 用途:评测闭源桌面 Agent(zCode/Codex),让用户配置 MITM 代理端口和转发目标,
// 捕获 Agent → 云端的所有请求/响应对,一键转为评测任务。
//
// 使用方式:
//   ① 填写监听端口(如 9090)和目标 URL(如 https://api.openai.com)
//   ② 启动代理
//   ③ 将被测 Agent 的请求指向 http://127.0.0.1:9090
//   ④ 代理自动捕获所有流量
//   ⑤ 在会话列表里点击"转评测任务"

import { ref, onMounted, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

const running = ref(false)
const listenAddr = ref(':9090')
const targetURL = ref('https://api.openai.com/v1')
const replayURL = ref('https://api.openai.com/v1')
const replayLoading = ref(false)
const replayResult = ref<any>(null)
const selectedReplaySid = ref('')
const loading = ref(false)
const sessions = ref<any[]>([])
let pollTimer: number | null = null

async function start() {
  if (!listenAddr.value || !targetURL.value) {
    ElMessage.warning('请填写监听端口和目标 URL')
    return
  }
  try {
    await api.post('/proxy/start', { listen_addr: listenAddr.value, target_url: targetURL.value })
    running.value = true
    ElMessage.success('MITM 代理已启动')
    startPolling()
  } catch (e: any) { ElMessage.error(e.message) }
}

async function stop() {
  await api.post('/proxy/stop')
  running.value = false
  stopPolling()
  ElMessage.info('代理已停止')
}

async function loadSessions() {
  try {
    sessions.value = await api.get('/proxy/sessions')
  } catch { /* ignore */ }
}

async function loadStatus() {
  try {
    const s = await api.get('/proxy/status')
    running.value = s.running || false
  } catch { running.value = false }
}

async function convertSession(sid: string) {
  try {
    const res = await api.post(`/proxy/sessions/${sid}/convert`, { agent_id: '' })
    ElMessage.success(`已转为评测任务: ${res.task_id}`)
  } catch (e: any) { ElMessage.error(e.message) }
}

async function replaySession(sid: string) {
  replayLoading.value = true
  replayResult.value = null
  try {
    replayResult.value = await api.post(`/proxy/sessions/${sid}/replay`, { target_url: replayURL.value })
    ElMessage.success(`回放完成: ${replayResult.value.count} 条请求`)
  } catch (e: any) { ElMessage.error(e.message) }
  finally { replayLoading.value = false }
}

function startPolling() {
  if (pollTimer) return
  pollTimer = window.setInterval(loadSessions, 2000)
}
function stopPolling() {
  if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
}

onMounted(async () => { await loadStatus(); await loadSessions(); if (running.value) startPolling() })
onBeforeUnmount(stopPolling)

// ── 协议逆向:环境变量重定向(对标 claude-code-router) ──
const redirectVisible = ref(false)
const redirectForm = ref({ local_proxy_url: 'http://127.0.0.1:9090', protocol: 'anthropic', api_key: '', format: 'sh' })
const redirectResult = ref<any>(null)

async function generateRedirect() {
  try {
    redirectResult.value = await api.post('/proxy/redirect-script', redirectForm.value)
    ElMessage.success('已生成重定向脚本')
  } catch (e: any) { ElMessage.error(e.message) }
}
</script>

<template>
  <div>
    <h2 class="page-title">协议逆向代理 — MITM</h2>
    <p class="page-desc">
      评测闭源桌面 Agent(如 zCode/Codex/Cursor)的流量拦截轨道。
      启动本地 HTTP 代理 → 将被测 Agent 请求转发到真实后端 → 捕获所有问答对 → 一键转评测任务。
    </p>

    <!-- 配置区 -->
    <el-card shadow="never" class="config-card">
      <el-form inline>
        <el-form-item label="监听端口">
          <el-input v-model="listenAddr" placeholder=":9090" style="width:120px" />
        </el-form-item>
        <el-form-item label="转发目标 URL">
          <el-input v-model="targetURL" placeholder="https://api.openai.com/v1" style="width:300px" />
        </el-form-item>
        <el-form-item>
          <el-button v-if="!running" type="primary" @click="start">
            <el-icon><VideoPlay /></el-icon>&nbsp;启动代理
          </el-button>
          <el-button v-else type="danger" @click="stop">
            <el-icon><VideoPause /></el-icon>&nbsp;停止代理
          </el-button>
          <el-button @click="redirectVisible = true">
            <el-icon><Connection /></el-icon>&nbsp;协议逆向重定向
          </el-button>
        </el-form-item>
      </el-form>
      <div class="status-line">
        状态:
        <el-tag :type="running ? 'success' : 'info'" size="small" effect="dark">
          {{ running ? '🟢 运行中' : '⚫ 已停止' }}
        </el-tag>
        <span v-if="running" class="hint">
          ← 将被测 Agent 的 API 请求指向 http://127.0.0.1{{ listenAddr }}
        </span>
      </div>
    </el-card>

    <!-- 会话列表 -->
    <el-card shadow="never">
      <template #header>
        <span>捕获的会话 ({{ sessions.length }})</span>
        <el-button size="small" @click="loadSessions" :loading="loading" style="float:right">刷新</el-button>
      </template>
      <el-empty v-if="!sessions.length" description="暂无捕获的会话,启动代理后自动记录流量" :image-size="60" />
      <el-table v-else :data="sessions" stripe size="small">
        <el-table-column prop="id" label="会话 ID" width="100">
          <template #default="{ row }">{{ row.id?.slice(0, 8) }}</template>
        </el-table-column>
        <el-table-column prop="target_url" label="目标 URL" min-width="200" show-overflow-tooltip />
        <el-table-column prop="start_time" label="开始时间" width="160" />
        <el-table-column prop="request_count" label="请求数" width="90" />
        <el-table-column label="操作" width="150">
          <template #default="{ row }">
            <el-button size="small" type="primary" @click="convertSession(row.id)">转评测任务</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 会话回放(V2 5.1 凭证复用) -->
    <el-card v-if="sessions.length" shadow="never" style="margin-top:16px">
      <template #header>🔄 会话回放(换模型对比)</template>
      <el-form inline>
        <el-form-item label="回放到目标 URL">
          <el-input v-model="replayURL" placeholder="https://api.openai.com/v1" style="width:300px" />
        </el-form-item>
        <el-form-item label="选择会话">
          <el-select v-model="selectedReplaySid" placeholder="选择" style="width:200px">
            <el-option v-for="s in sessions" :key="s.id" :label="s.id?.slice(0,8)+' ('+s.request_count+' 请求)'" :value="s.id" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="warning" :loading="replayLoading" @click="replaySession(selectedReplaySid)" :disabled="!selectedReplaySid">
            执行回放
          </el-button>
        </el-form-item>
      </el-form>
      <div v-if="replayResult" class="replay-info">
        <el-tag type="success">回放完成: {{ replayResult.count }} 条请求 → {{ replayResult.replayed_to }}</el-tag>
      </div>
    </el-card>

    <!-- 说明卡片 -->
    <el-alert type="info" :closable="false" style="margin-top:16px" title="使用说明">
      <p>1. 启动代理后,它会监听本地端口并转发到目标 URL</p>
      <p>2. 修改被测 Agent 的配置,将 API Base URL 改为 http://127.0.0.1:9090</p>
      <p>3. 让 Agent 执行任务 → 代理自动捕获流量 → 点"转评测任务"即可评测</p>
      <p>4. 参考实现: claude-code-router(MITM 代理 + 环境变量重定向)</p>
    </el-alert>

    <!-- ═══ 协议逆向:环境变量重定向对话框(对标 claude-code-router) ═══ -->
    <el-dialog v-model="redirectVisible" title="协议逆向 — 环境变量重定向" width="640px">
      <el-alert type="info" :closable="false" style="margin-bottom:16px">
        对标 claude-code-router 的实用方案:把闭源客户端的 API 端点环境变量重定向到本地代理。
        在启动被测客户端的终端里执行下面的脚本,客户端流量就会经过本地代理(需先启动 HTTP 代理)。
      </el-alert>
      <el-form label-width="110px">
        <el-form-item label="本地代理地址">
          <el-input v-model="redirectForm.local_proxy_url" />
        </el-form-item>
        <el-form-item label="协议">
          <el-select v-model="redirectForm.protocol" style="width:100%">
            <el-option label="Anthropic (Claude 系)" value="anthropic" />
            <el-option label="OpenAI 兼容" value="openai" />
          </el-select>
        </el-form-item>
        <el-form-item label="API Key">
          <el-input v-model="redirectForm.api_key" show-password placeholder="透传给云端的 Key(本地不留存)" />
        </el-form-item>
        <el-form-item label="脚本格式">
          <el-select v-model="redirectForm.format" style="width:100%">
            <el-option label="sh / bash" value="sh" />
            <el-option label="cmd (Windows)" value="cmd" />
            <el-option label="PowerShell" value="powershell" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="generateRedirect">生成重定向脚本</el-button>
        </el-form-item>
      </el-form>
      <div v-if="redirectResult" class="redirect-result">
        <div class="redirect-hint">{{ redirectResult.hint }}</div>
        <pre class="script-box">{{ redirectResult.script }}</pre>
      </div>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 8px; }
.page-desc { color: #909399; font-size: 13px; margin: 0 0 16px; }
.config-card { margin-bottom: 16px; }
.status-line { margin-top: 12px; display: flex; align-items: center; gap: 8px; font-size: 13px; color: #606266; }
.hint { color: #409eff; font-size: 12px; }
.replay-info { margin-top: 12px; }
.redirect-result { margin-top: 12px; }
.redirect-hint { color: #909399; font-size: 12px; margin-bottom: 8px; }
.script-box {
  background: #1a1a2e; color: #67c23a; padding: 12px; border-radius: 6px;
  font-family: Consolas, monospace; font-size: 13px; white-space: pre-wrap;
  max-height: 200px; overflow-y: auto;
}
</style>
