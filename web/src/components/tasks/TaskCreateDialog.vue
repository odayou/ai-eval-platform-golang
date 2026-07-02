<script setup lang="ts">
/**
 * TaskCreateDialog.vue — 新建评测任务对话框。
 *
 * 表单字段:任务名、场景、被测 Agent、规则集。
 * 提交后调 taskStore.createTask,成功后关闭并 emit created。
 */
import { ref, watch } from 'vue'
import { useTaskStore } from '../../stores/taskStore'
import { useDictStore } from '../../stores/dictStore'
import { ElMessage } from 'element-plus'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
}>()

const taskStore = useTaskStore()
const dict = useDictStore()

	// 表单状态
	const form = ref({
	  name: '', scene: 'agent', agent_id: '', rule_set: '',
	  expert_group_id: '' as string,
	  chaos_enabled: false,
	  tools: [] as string[],
	  breaker_max_tokens: 100000,
	  breaker_max_turns: 30,
	})

	// 对话框打开时,预填场景为当前 Tab,并确保字典已加载
	watch(() => props.modelValue, async (open) => {
	  if (open) {
	    form.value.scene = taskStore.activeScene
	    form.value.name = ''
	    form.value.agent_id = ''
	    form.value.rule_set = ''
	    form.value.expert_group_id = ''
	    form.value.chaos_enabled = false
	    form.value.tools = []
	    form.value.breaker_max_tokens = 100000
	    form.value.breaker_max_turns = 30
	    await Promise.all([dict.ensureAgents(), dict.ensureRuleSets(), dict.ensureTools(), dict.ensureExpertGroups()])
	  }
	})

// 表单校验
function validate(): boolean {
  if (!form.value.name.trim()) {
    ElMessage.warning('请填写任务名称')
    return false
  }
  if (!form.value.agent_id) {
    ElMessage.warning('请选择被测 Agent')
    return false
  }
  return true
}

// 快速推荐:选 Agent 后自动推荐规则集
const RECOMMEND_RULESET: Record<string, string> = {
  agent: 'rs-fullstack',
  model: 'rs-model',
  competitor: 'rs-competitor',
  prd: 'rs-prd',
  product: 'rs-product',
}
function autoRecommend() {
  // 根据 Agent 名字/场景推荐
  const agent = dict.agents.find((a: any) => a.id === form.value.agent_id)
  if (agent) {
    // 自动填任务名
    if (!form.value.name) {
      form.value.name = `${agent.name} 评测 ${new Date().toLocaleDateString()}`
    }
  }
  // 推荐规则集
  const recommended = RECOMMEND_RULESET[form.value.scene]
  if (recommended && !form.value.rule_set) {
    const rs = dict.ruleSets.find((r: any) => r.id === recommended || r.name === recommended)
    if (rs) {
      form.value.rule_set = rs.id
      ElMessage.success(`已自动推荐规则集: ${rs.name}`)
    }
  }
}

// 提交创建
async function submit() {
  if (!validate()) return
  try {
    await taskStore.createTask({
      name: form.value.name,
      scene: form.value.scene,
      agent_id: form.value.agent_id || undefined,
      rule_set: form.value.rule_set ? JSON.stringify({ rule_set_id: form.value.rule_set }) : undefined,
      expert_group_id: form.value.expert_group_id || undefined,
      chaos_enabled: form.value.chaos_enabled,
    })
    ElMessage.success('任务已创建')
    emit('update:modelValue', false)
  } catch (e: any) {
    ElMessage.error(e.message || '创建失败')
  }
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    @update:model-value="emit('update:modelValue', $event)"
    title="新建评测任务"
    width="560px"
  >
    <el-form label-width="90px">
      <el-form-item label="任务名称" required>
        <el-input v-model="form.name" placeholder="如:CodeGen Agent 安全专项评测" />
      </el-form-item>
	      <el-form-item label="评测场景">
	        <el-select v-model="form.scene" style="width: 100%">
	          <el-option v-for="s in taskStore.scenes" :key="s.key" :label="s.label" :value="s.key" />
	        </el-select>
	      </el-form-item>
      <el-form-item label="被测 Agent" required>
        <el-select v-model="form.agent_id" style="width: 100%" clearable placeholder="选择 Agent(选后自动推荐配置)" @change="autoRecommend">
          <el-option
            v-for="a in dict.agents"
            :key="a.id"
            :label="`${a.name} (${a.model || '-'})`"
            :value="a.id"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="专家组">
        <el-select v-model="form.expert_group_id" style="width: 100%" clearable placeholder="留空用默认专家组">
          <el-option
            v-for="g in dict.expertGroups"
            :key="g.id"
            :label="`${g.name} (${g.member_count}人)`"
            :value="g.id"
          />
        </el-select>
        <span class="chaos-hint">选择圆桌评测用的专家组。留空用默认专家组(安全/性能/架构/产品)</span>
      </el-form-item>
      <el-form-item label="规则集">
        <el-select v-model="form.rule_set" style="width: 100%" clearable placeholder="选择规则集">
          <el-option
            v-for="r in dict.ruleSets"
            :key="r.id"
            :label="r.name"
            :value="r.id"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="混沌对抗">
        <el-switch v-model="form.chaos_enabled" />
        <span class="chaos-hint">开启后注入弱网/脏数据/越狱/资源耗尽,测 Agent 鲁棒性</span>
      </el-form-item>
      <el-form-item label="挂载 Tools">
        <el-select v-model="form.tools" multiple filterable style="width: 100%" placeholder="选择评测时 Agent 可用的 Tools">
          <el-option v-for="t in dict.tools" :key="t.id" :label="`${t.name} (${t.description?.slice(0, 20) || ''})`" :value="t.id" />
        </el-select>
        <span class="chaos-hint">可选,限制 Agent 可用工具集。留空则使用 Agent 默认配置</span>
      </el-form-item>
      <el-divider content-position="left">⚡ 断路器(钱包保护)</el-divider>
      <el-form-item label="Token 上限">
        <el-input-number v-model="form.breaker_max_tokens" :min="1000" :max="1000000" :step="10000" />
        <span class="chaos-hint">单次评测最大 Token 消耗,超限自动熔断</span>
      </el-form-item>
      <el-form-item label="最大轮数">
        <el-input-number v-model="form.breaker_max_turns" :min="5" :max="200" />
        <span class="chaos-hint">Tool Calling 最大轮数,防止死循环</span>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" @click="submit">创建</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.chaos-hint {
  margin-left: 8px;
  font-size: 12px;
  color: #909399;
}
</style>
