<script setup lang="ts">
/**
 * Tasks.vue — 评测任务页(默认落地页)。
 *
 * 容器职责:
 *   1. 顶部场景 Tab(5 场景 + 切换)
 *   2. 后端健康状态
 *   3. 左右分栏:TaskList + TaskDetail
 *   4. 创建对话框开关
 *
 * 具体逻辑下沉到子组件,本文件保持精简。
 */
import { ref, computed, onMounted } from 'vue'
import { useTaskStore } from '../stores/taskStore'
import { useDictStore } from '../stores/dictStore'
import { ElMessage, ElMessageBox } from 'element-plus'
import TaskList from '../components/tasks/TaskList.vue'
import TaskDetail from '../components/tasks/TaskDetail.vue'
import TaskCreateDialog from '../components/tasks/TaskCreateDialog.vue'
import api from '../utils/api'

const taskStore = useTaskStore()
const dict = useDictStore()

const healthStatus = ref('checking...')
const createVisible = ref(false)
const sceneCreateVisible = ref(false)
const newScene = ref({ label: '', description: '', strategy: 'text_review' })

// 各场景任务计数(供 Tab 徽标)
const sceneCounts = computed(() => {
  const counts: Record<string, number> = {}
  for (const t of taskStore.tasks) {
    counts[t.scene] = (counts[t.scene] || 0) + 1
  }
  return counts
})

// 创建自定义场景
async function submitCreateScene() {
  if (!newScene.value.label.trim()) {
    ElMessage.warning('请输入场景名称')
    return
  }
  try {
    await taskStore.createScene(newScene.value.label, newScene.value.description, newScene.value.strategy)
    ElMessage.success(`场景「${newScene.value.label}」已创建`)
    sceneCreateVisible.value = false
    newScene.value = { label: '', description: '', strategy: 'text_review' }
  } catch (e: any) {
    ElMessage.error(e.message || '创建失败')
  }
}

// 删除自定义场景
async function handleDeleteScene(key: string) {
  const scene = taskStore.scenes.find(s => s.key === key)
  if (scene?.builtin) {
    ElMessage.warning('内置场景不可删除')
    return
  }
  try {
    await ElMessageBox.confirm(`确定删除场景「${scene?.label}」?`, '删除场景', { type: 'warning' })
    await taskStore.deleteScene(key)
    ElMessage.success('场景已删除')
  } catch { /* 用户取消 */ }
}

onMounted(async () => {
  // 并行拉取:健康检查 + 场景列表 + 任务列表 + 字典
  await Promise.all([
    api.get('/health').then((r: any) => { healthStatus.value = r.status })
      .catch(() => { healthStatus.value = 'unreachable' }),
    taskStore.loadScenes(),
    taskStore.loadTasks(),
    dict.ensureAgents(),
    dict.ensureRuleSets(),
  ])
})
</script>

<template>
  <div>
    <!-- 页头 -->
    <div class="page-header">
      <h2>评测任务</h2>
      <el-tag :type="healthStatus === 'ok' ? 'success' : 'danger'" size="small">
        Backend: {{ healthStatus }}
      </el-tag>
    </div>

    <!-- 场景 Tab(动态) -->
    <el-tabs v-model="taskStore.activeScene" class="scene-tabs">
      <el-tab-pane v-for="s in taskStore.scenes" :key="s.key" :name="s.key">
        <template #label>
          <span @contextmenu.prevent="handleDeleteScene(s.key)" :title="s.description || s.label">
            {{ s.label }}
            <el-badge :value="sceneCounts[s.key] || 0" :hidden="!sceneCounts[s.key]" class="tab-badge" />
          </span>
        </template>
      </el-tab-pane>
      <!-- + 按钮:创建自定义场景 -->
      <el-tab-pane name="__add__" disabled>
        <template #label>
          <el-button size="small" text type="primary" @click.stop="sceneCreateVisible = true" title="创建自定义评测场景">
            <el-icon><Plus /></el-icon>
          </el-button>
        </template>
      </el-tab-pane>
    </el-tabs>

    <!-- 左右分栏 -->
    <el-row :gutter="16">
      <el-col :span="8">
        <TaskList @create="createVisible = true" />
      </el-col>
      <el-col :span="16">
        <TaskDetail />
      </el-col>
    </el-row>

    <!-- 创建对话框 -->
    <TaskCreateDialog v-model="createVisible" />

    <!-- 创建自定义场景对话框(MRD 6.2) -->
    <el-dialog v-model="sceneCreateVisible" title="+ 创建评测场景" width="480px">
      <el-form label-width="80px">
        <el-form-item label="场景名称" required>
          <el-input v-model="newScene.label" placeholder="如: AIGC 文生图评测、客服话术质量评测" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="newScene.description" type="textarea" :rows="2" placeholder="该场景评测什么..." />
        </el-form-item>
        <el-form-item label="评测策略">
          <el-select v-model="newScene.strategy" style="width: 100%">
            <el-option label="沙箱 + 圆桌合议(完整流程)" value="sandbox_roundtable" />
            <el-option label="API 直调 + 单判官(轻量)" value="api_direct" />
            <el-option label="纯文本评审(圆桌)" value="text_review" />
            <el-option label="多模态评审" value="multimodal_review" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="sceneCreateVisible = false">取消</el-button>
        <el-button type="primary" @click="submitCreateScene">创建场景</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.page-header h2 { margin: 0; font-size: 20px; color: #1a1a2e; }
.scene-tabs { margin-bottom: 16px; }
.tab-badge { margin-left: 4px; }
.tab-badge :deep(.el-badge__content) { font-size: 11px; }
</style>
