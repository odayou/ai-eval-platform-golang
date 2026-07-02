/**
 * dictStore.ts — 字典数据缓存(下拉选项)。
 *
 * 多个页面都需要 Agent 列表、规则集列表、题库列表作为下拉选项。
 * 集中缓存避免重复请求;各页面 onMounted 时调 ensure* 方法拉取(已缓存则跳过)。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import api from '../utils/api'

export interface Agent {
  id: string; name: string; adapter: string; model: string; online: boolean
}
export interface RuleSet {
  id: string; name: string; scene: string; rule_ids: string
}
export interface Question {
  id: string; scene: string; title: string; prompt: string
  difficulty: string; source: string
}
export interface Tool {
  id: string; name: string; description: string; params_schema: string; tags: string
}
export interface PromptTemplate {
  id: string; name: string; content: string; version: number; model_hint: string
}
export interface ExpertGroup {
  id: string; name: string; description: string; status: string; member_count: number
}

export const useDictStore = defineStore('dict', () => {
	const agents = ref<Agent[]>([])
	const ruleSets = ref<RuleSet[]>([])
	const questions = ref<Question[]>([])
	const tools = ref<Tool[]>([])
	const promptTemplates = ref<PromptTemplate[]>([])
	const expertGroups = ref<ExpertGroup[]>([])

  // 幂等拉取:已缓存则不重复请求
  async function ensureAgents() {
    if (agents.value.length) return
    agents.value = await api.get('/agents')
  }
  async function ensureRuleSets() {
    if (ruleSets.value.length) return
    ruleSets.value = await api.get('/rule-sets')
  }
	  async function ensureQuestions() {
	    if (questions.value.length) return
	    questions.value = await api.get('/questions')
	  }
	  async function ensureTools() {
	    if (tools.value.length) return
	    tools.value = await api.get('/tools')
	  }
	  async function ensurePromptTemplates() {
	    if (promptTemplates.value.length) return
	    promptTemplates.value = await api.get('/prompt-templates')
	  }
	  async function ensureExpertGroups() {
	    if (expertGroups.value.length) return
	    expertGroups.value = await api.get('/expert-groups')
	  }

  // 强制刷新(创建/删除后调用)
  async function refreshAgents() { agents.value = await api.get('/agents') }
  async function refreshRuleSets() { ruleSets.value = await api.get('/rule-sets') }
  async function refreshQuestions() { questions.value = await api.get('/questions') }
  async function refreshExpertGroups() { expertGroups.value = await api.get('/expert-groups') }

	  return {
	    agents, ruleSets, questions, tools, promptTemplates, expertGroups,
	    ensureAgents, ensureRuleSets, ensureQuestions, ensureTools, ensurePromptTemplates, ensureExpertGroups,
	    refreshAgents, refreshRuleSets, refreshQuestions, refreshExpertGroups,
	  }
})
