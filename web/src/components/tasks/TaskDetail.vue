<script setup lang="ts">
/**
 * TaskDetail.vue — 任务详情(评测任务页右栏)。
 *
 * 三个子 Tab(对应 MRD 11.1):
 *   1. 配置 — 被测对象、规则集、状态等元信息
 *   2. 运行 — 日志流/轨迹(②引擎与 ⑤WebSocket 接入后填充,现阶段占位)
 *   3. 报告 — 委托给 TaskReport 组件渲染雷达图
 *
 * 本组件只做容器与数据展示,不处理创建/删除。
 */
import { computed } from 'vue'
import { useTaskStore } from '../../stores/taskStore'
import { useDictStore } from '../../stores/dictStore'
import TaskReport from './TaskReport.vue'
import TaskRun from './TaskRun.vue'
import TaskAttribution from './TaskAttribution.vue'
import AuditTimeline from '../AuditTimeline.vue'

const taskStore = useTaskStore()
const dict = useDictStore()

const task = computed(() => taskStore.selectedTask)

// 解析 rule_set JSON 取 rule_set_id,显示规则集名称
const ruleSetName = computed(() => {
  if (!task.value?.rule_set) return '-'
  try {
    const rsId = JSON.parse(task.value.rule_set).rule_set_id
    return dict.ruleSets.find((r) => r.id === rsId)?.name || rsId || '-'
  } catch {
    return '-'
  }
})

// Agent 名称
const agentName = computed(() => {
  if (!task.value?.agent_id) return '-'
  return dict.agents.find((a) => a.id === task.value?.agent_id)?.name || task.value.agent_id
})

const statusLabel: Record<string, string> = {
  pending: '待运行', running: '运行中', done: '已完成', failed: '失败', blown: '已熔断',
}
</script>

<template>
  <el-card shadow="never" class="detail-card">
    <!-- 未选中任务 -->
    <el-empty
      v-if="!task"
      description="选择左侧任务查看详情"
      :image-size="80"
    />

    <template v-else>
      <div class="detail-header">
        <h3>{{ task.name }}</h3>
        <el-tag size="small">{{ statusLabel[task.status] || task.status }}</el-tag>
      </div>

      <el-tabs class="detail-tabs">
        <!-- ── 配置 Tab ── -->
        <el-tab-pane label="配置" name="config">
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="任务 ID">{{ task.id }}</el-descriptions-item>
            <el-descriptions-item label="场景">{{ task.scene }}</el-descriptions-item>
            <el-descriptions-item label="被测 Agent">{{ agentName }}</el-descriptions-item>
            <el-descriptions-item label="规则集">{{ ruleSetName }}</el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ task.created_at?.slice(0, 19).replace('T', ' ') }}</el-descriptions-item>
            <el-descriptions-item label="更新时间">{{ task.updated_at?.slice(0, 19).replace('T', ' ') }}</el-descriptions-item>
          </el-descriptions>
        </el-tab-pane>

        <!-- ── 运行 Tab ── -->
        <el-tab-pane label="运行" name="run">
          <TaskRun />
        </el-tab-pane>

        <!-- ── 报告 Tab ── -->
        <el-tab-pane label="报告" name="report">
          <TaskReport :task="task" />
        </el-tab-pane>

        <!-- ── 归因 Tab ── -->
        <el-tab-pane label="归因" name="attribution">
          <TaskAttribution />
        </el-tab-pane>

        <!-- ── 白盒审计 Tab ── -->
        <el-tab-pane label="白盒审计" name="audit" v-if="task?.id">
          <AuditTimeline :task-id="task.id" />
        </el-tab-pane>
      </el-tabs>
    </template>
  </el-card>
</template>

<style scoped>
.detail-card { min-height: 500px; }
.detail-header { display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
.detail-header h3 { margin: 0; font-size: 18px; }
.detail-tabs { margin-top: 4px; }
</style>
