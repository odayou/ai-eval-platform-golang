/**
 * taskStore.ts — 评测任务状态管理 + 动态场景(MRD 6.2)。
 *
 * 职责:任务列表加载、当前选中任务、创建任务、按场景过滤、动态场景管理。
 * 不在此处管理报告详情(报告由 TaskDetail 组件按需拉取,避免列表页过重)。
 */
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '../utils/api'

// 评测场景定义(对应后端 tasks.scene 与顶部 Tab)
export interface SceneOption {
  key: string
  label: string
  description?: string
  strategy?: string
  builtin?: boolean
}
// 默认场景(后端未返回或未加载时使用)
const DEFAULT_SCENES: SceneOption[] = [
  { key: 'agent', label: 'Agent 评测', description: '测各类智能体', builtin: true },
  { key: 'model', label: '大模型评测', description: '测裸模型 API', builtin: true },
  { key: 'competitor', label: '竞品报告', description: '对比分析报告', builtin: true },
  { key: 'prd', label: 'PRD 评测', description: '产品需求文档', builtin: true },
  { key: 'product', label: 'AI 产品评测', description: '完整 AI 产品', builtin: true },
]

// 任务记录(对应后端 tasks 表)
export interface Task {
  id: string
  name: string
  scene: string
  agent_id: string
  rule_set: string
  status: string        // pending | running | done | failed | blown
  result: string        // JSON 字符串,引擎产出
  chaos_enabled: boolean // 是否开启混沌对抗
  created_at: string
  updated_at: string
}

// 从 task.result JSON 解析综合分(done 状态才有),供列表展示用
export function taskOverall(t: Task): number | null {
  if (!t.result) return null
  try { return JSON.parse(t.result).overall ?? null } catch { return null }
}

export const useTaskStore = defineStore('task', () => {
  const tasks = ref<Task[]>([])
  const loading = ref(false)
  const activeScene = ref('agent')
  const selectedId = ref<string | null>(null)
  const scenes = ref<SceneOption[]>([...DEFAULT_SCENES])

  // 当前场景下的任务列表(场景 Tab 过滤)
  const filteredTasks = computed(() =>
    tasks.value.filter((t) => t.scene === activeScene.value),
  )

  // 当前选中的任务对象
  const selectedTask = computed(() =>
    tasks.value.find((t) => t.id === selectedId.value) || null,
  )

  // 拉取全部任务(不过滤,场景过滤在前端做)
  async function loadTasks() {
    loading.value = true
    try {
      tasks.value = await api.get('/tasks')
    } finally {
      loading.value = false
    }
  }

  // ── 动态场景管理(MRD 6.2)──

  // 从后端加载场景列表(含用户自定义)
  async function loadScenes() {
    try {
      const data = await api.get('/scenes')
      if (data && data.length > 0) {
        scenes.value = data.map((s: any) => ({
          key: s.id,
          label: s.label,
          description: s.description || '',
          strategy: s.strategy || 'text_review',
          builtin: s.builtin !== '0',
        }))
      }
    } catch {
      // 后端表未创建时用默认值
      scenes.value = [...DEFAULT_SCENES]
    }
  }

  // 创建自定义场景
  async function createScene(label: string, description: string, strategy: string, ruleSetId?: string) {
    const created = await api.post('/scenes', { label, description, strategy, rule_set_id: ruleSetId || '' })
    await loadScenes()
    // 自动切换到新场景
    activeScene.value = created.id
    return created
  }

  // 删除自定义场景
  async function deleteScene(id: string) {
    await api.delete(`/scenes/${id}`)
    await loadScenes()
    // 如果删除的是当前活跃场景,切换到第一个
    if (activeScene.value === id) {
      activeScene.value = scenes.value[0]?.key || 'agent'
    }
  }

  // 创建任务,成功后刷新列表并选中新任务
  async function createTask(payload: {
    name: string
    scene: string
    agent_id?: string
    rule_set?: string
    expert_group_id?: string
    chaos_enabled?: boolean
  }) {
    const created: Task = await api.post('/tasks', payload)
    await loadTasks()
    selectedId.value = created.id
    return created
  }

  // 删除任务
  async function deleteTask(id: string) {
    await api.delete(`/tasks/${id}`)
    if (selectedId.value === id) selectedId.value = null
    await loadTasks()
  }

  // 选中某个任务
  function selectTask(id: string) {
    selectedId.value = id
  }

  return {
    tasks, loading, activeScene, selectedId, scenes,
    filteredTasks, selectedTask,
    loadTasks, loadScenes,
    createTask, deleteTask, selectTask,
    createScene, deleteScene,
  }
})
