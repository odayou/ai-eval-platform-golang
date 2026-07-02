<script setup lang="ts">
// Terminal.vue — 终端轨道(⑥ 协议层):评测团队自研本地 Agent。
//
// 工作流:
//   ① 填项目路径 → 点"自动识别" → AutoDiscover 推断启动命令/喂题模式
//   ② 微调配置(启动命令/喂题模式/超时)
//   ③ 输入测试题目 → 点"执行测试" → 看 stdout/stderr/耗时
//   ④ 满意后"保存为 Agent" → 写入 agents 表(adapter=terminal)
//         之后创建评测任务选这个 Agent,engine 会走终端轨道
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

interface TerminalConfig {
  project_path: string
  start_cmd: string
  feed_mode: string
  args_pattern: string
  stdin_prompt: string
  timeout: number
  max_output: number
}

interface TerminalResult {
  success: boolean
  output: string
  error: string
  exit_code: number
  duration: number
  truncated: boolean
}

// 配置表单
const config = ref<TerminalConfig>({
  project_path: '',
  start_cmd: '',
  feed_mode: 'oneshot',
  args_pattern: '--task {prompt}',
  stdin_prompt: '>>> ',
  timeout: 60,
  max_output: 100000,
})

const discoverLoading = ref(false)
const testLoading = ref(false)
const saveLoading = ref(false)
const testPrompt = ref('实现一个快速排序,并解释时间复杂度')
const testResult = ref<TerminalResult | null>(null)

// 自动识别项目类型
async function discover() {
  if (!config.value.project_path.trim()) {
    ElMessage.warning('请填写项目路径')
    return
  }
  discoverLoading.value = true
  try {
    const res = await api.post('/terminal/discover', { project_path: config.value.project_path })
    if (res.ok && res.config) {
      // 用识别到的配置覆盖(保留用户已填的 project_path)
      const discovered = res.config
      config.value.start_cmd = discovered.start_cmd || config.value.start_cmd
      config.value.feed_mode = discovered.feed_mode || config.value.feed_mode
      config.value.args_pattern = discovered.args_pattern || config.value.args_pattern
      ElMessage.success(`识别成功: ${discovered.start_cmd} (${discovered.feed_mode})`)
    } else {
      ElMessage.warning(res.error || '无法识别项目类型')
    }
  } catch (e: any) {
    ElMessage.error(e.message || '识别失败')
  } finally {
    discoverLoading.value = false
  }
}

// 执行测试
async function runTest() {
  if (!config.value.project_path.trim()) {
    ElMessage.warning('请填写项目路径')
    return
  }
  if (!config.value.start_cmd.trim()) {
    ElMessage.warning('请填写启动命令')
    return
  }
  testLoading.value = true
  testResult.value = null
  try {
    const res = await api.post('/terminal/test', {
      config: config.value,
      prompt: testPrompt.value,
    }) as TerminalResult
    testResult.value = res
    if (res.success) {
      ElMessage.success(`执行成功 (${res.duration}ms)`)
    } else {
      ElMessage.warning(`执行失败 exit=${res.exit_code}`)
    }
  } catch (e: any) {
    ElMessage.error(e.message || '执行失败')
  } finally {
    testLoading.value = false
  }
}

// 保存为 Agent(adapter=terminal)
async function saveAsAgent() {
  if (!config.value.project_path.trim() || !config.value.start_cmd.trim()) {
    ElMessage.warning('请先完成配置')
    return
  }
  const name = prompt('Agent 名称:', 'Terminal Agent')
  if (!name) return
  saveLoading.value = true
  try {
    // TerminalConfig 以 JSON 存入 tools 字段
    await api.post('/agents', {
      name,
      adapter: 'terminal',
      model: '',
      tools: JSON.stringify(config.value),
    })
    ElMessage.success(`Agent "${name}" 已保存,可在评测任务页选择使用`)
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  } finally {
    saveLoading.value = false
  }
}

const feedModes = [
  { label: '一次性执行 (oneshot)', value: 'oneshot' },
  { label: '命令行参数 (args)', value: 'args' },
  { label: 'stdin 注入', value: 'stdin' },
]
</script>

<template>
  <div>
    <h2 class="page-title">终端轨道 — 评测本地自研 Agent</h2>
    <p class="page-desc">
      指定 Agent 项目路径,平台拉起进程、喂题、收 stdout,实现自动化评测。
      支持 Python / Go / Node.js 项目的自动识别。
    </p>

    <el-row :gutter="16">
      <!-- 左:配置区 -->
      <el-col :span="14">
        <el-card shadow="never">
          <template #header>
            <div class="card-head">
              <span>配置</span>
              <el-button type="primary" size="small" :loading="discoverLoading" @click="discover">
                <el-icon><Search /></el-icon>&nbsp;自动识别
              </el-button>
            </div>
          </template>
          <el-form label-width="100px">
            <el-form-item label="项目路径" required>
              <el-input v-model="config.project_path" placeholder="如 E:\projects\my-agent 或 /home/user/agent" />
            </el-form-item>
            <el-form-item label="启动命令" required>
              <el-input v-model="config.start_cmd" placeholder="如 python main.py 或 go run ." />
            </el-form-item>
            <el-form-item label="喂题模式">
              <el-select v-model="config.feed_mode" style="width:100%">
                <el-option v-for="m in feedModes" :key="m.value" :label="m.label" :value="m.value" />
              </el-select>
            </el-form-item>
            <el-form-item v-if="config.feed_mode === 'args'" label="参数模板">
              <el-input v-model="config.args_pattern" placeholder="--task {prompt}({prompt} 会被替换为题目)" />
            </el-form-item>
            <el-form-item v-if="config.feed_mode === 'stdin'" label="stdin 触发词">
              <el-input v-model="config.stdin_prompt" placeholder=">>> " />
            </el-form-item>
            <el-form-item label="超时(秒)">
              <el-input-number v-model="config.timeout" :min="5" :max="600" />
            </el-form-item>
          </el-form>
        </el-card>
      </el-col>

      <!-- 右:测试区 -->
      <el-col :span="10">
        <el-card shadow="never">
          <template #header>执行测试</template>
          <el-form label-position="top">
            <el-form-item label="测试题目">
              <el-input v-model="testPrompt" type="textarea" :rows="3" />
            </el-form-item>
          </el-form>
          <el-button type="primary" :loading="testLoading" @click="runTest" style="width:100%;margin-bottom:12px">
            <el-icon><VideoPlay /></el-icon>&nbsp;执行测试
          </el-button>

          <!-- 测试结果 -->
          <div v-if="testResult" class="result-box">
            <div class="result-meta">
              <el-tag :type="testResult.success ? 'success' : 'danger'" size="small">
                {{ testResult.success ? '成功' : '失败' }} · exit={{ testResult.exit_code }}
              </el-tag>
              <span class="duration">{{ testResult.duration }}ms</span>
              <el-tag v-if="testResult.truncated" type="warning" size="small">已截断</el-tag>
            </div>
            <div class="result-section">
              <span class="sec-lbl">stdout</span>
              <pre class="output">{{ testResult.output || '(空)' }}</pre>
            </div>
            <div v-if="testResult.error" class="result-section">
              <span class="sec-lbl err">stderr</span>
              <pre class="output err">{{ testResult.error }}</pre>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 保存为 Agent -->
    <div class="save-bar">
      <el-button type="success" :loading="saveLoading" @click="saveAsAgent">
        <el-icon><Check /></el-icon>&nbsp;保存为 Terminal Agent
      </el-button>
      <span class="save-hint">保存后可在「评测任务」页选择该 Agent,引擎会自动走终端轨道</span>
    </div>
  </div>
</template>

<style scoped>
.page-title { font-size: 20px; color: #1a1a2e; margin: 0 0 8px; }
.page-desc { color: #909399; font-size: 13px; margin: 0 0 16px; }
.card-head { display: flex; justify-content: space-between; align-items: center; }
.result-box { margin-top: 12px; }
.result-meta { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.duration { font-size: 13px; color: #909399; }
.result-section { margin-bottom: 8px; }
.sec-lbl { font-size: 12px; color: #909399; font-weight: 600; }
.sec-lbl.err { color: #f56c6c; }
.output {
  margin: 4px 0; padding: 8px; background: #1a1a2e; color: #e0e0e0;
  border-radius: 4px; font-size: 12px; max-height: 200px; overflow-y: auto;
  white-space: pre-wrap; word-break: break-all;
}
.output.err { background: #2a1a1a; color: #ffb0b0; }
.save-bar { margin-top: 16px; display: flex; align-items: center; gap: 12px; }
.save-hint { color: #909399; font-size: 12px; }
</style>
