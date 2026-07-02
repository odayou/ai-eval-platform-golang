<script setup lang="ts">
/**
 * TaskReport.vue — 评测报告区(雷达图 + 维度明细 + 圆桌合议 + Bad Case)。
 *
 * 解析 task.result(JSON),渲染:
 *   1. ECharts 雷达图 — 各维度得分一目了然
 *   2. 维度明细表 — 每个维度的具体分数
 *   3. 圆桌合议详情 — 各专家独立意见 + 主导者综合评语
 *   4. Bad Case 计数提示
 *
 * 当 result 为空(任务未完成)时显示占位。
 */
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { Task } from '../../stores/taskStore'
import BaseChart from '../BaseChart.vue'
import api from '../../utils/api'

const props = defineProps<{ task: Task }>()

// ── 导出报告 ──
function exportReport() {
  // 直接触发浏览器下载(后端返回 Content-Disposition: attachment)
  window.open(`/api/tasks/${props.task.id}/export?format=md`, '_blank')
}

// ── 人工反馈 ──
const taskFeedback = ref<string>('')
const showFeedbackDialog = ref(false)
const feedbackNote = ref('')

async function submitTaskFeedback(action: string) {
  try {
    await api.post(`/feedback/tasks/${props.task.id}`, {
      action,
      note: action === 'thumbs_down' ? feedbackNote.value : '',
    })
    taskFeedback.value = action
    showFeedbackDialog.value = false
    feedbackNote.value = ''
    ElMessage.success(action === 'thumbs_up' ? '已标记准确' : '反馈已提交，感谢！')
  } catch (e: any) {
    ElMessage.error('提交失败: ' + e.message)
  }
}

// 解析 result JSON,容错:非法 JSON 返回 null
const report = computed<any | null>(() => {
  if (!props.task.result) return null
  try {
    return JSON.parse(props.task.result)
  } catch {
    return null
  }
})

// 是否有圆桌合议结果
const hasRoundtable = computed(() => {
  return report.value?.roundtable && report.value.roundtable.length > 0
})

// 断路器状态(钱包保护透明化)
const breakerInfo = computed(() => report.value?.breaker || null)

// 沙箱指标汇总(仅 agent 场景)
const sandboxSummary = computed(() => {
  const sb = report.value?.sandbox
  if (!sb) return null
  // 只有真实跑过沙箱(有编译记录)才展示
  if ((sb.compile_ok + sb.compile_fail) === 0) return null
  return sb
})

// 混沌工程结果(仅开启混沌的任务有)
const chaosSummary = computed(() => report.value?.chaos || null)

// 反泄漏扫描结果(V2 9.5,每题都会扫描)
const leakageSummary = computed(() => report.value?.leakage || null)

// ── 代码评审 Findings(汇总所有题的 code_review findings) ──
const codeReviewFindings = computed(() => {
  const all: any[] = []
  for (const q of report.value?.per_question || []) {
    const cr = q.code_review
    if (cr?.findings) {
      for (const f of cr.findings) {
        all.push({ ...f, _question: q.title?.slice(0, 15) })
      }
    }
  }
  return all
})
const codeReviewIsMock = computed(() => {
  return (report.value?.per_question || []).some((q: any) => q.code_review?.is_mock)
})
const crCorrectnessLabel = computed(() => {
  const map: Record<string, string> = { correct: '✅ 代码正确', incorrect: '❌ 有严重问题', partially_correct: '⚠️ 部分正确' }
  // 取最差的
  let worst = 'correct'
  for (const q of report.value?.per_question || []) {
    const c = q.code_review?.overall_correctness
    if (c === 'incorrect') { worst = 'incorrect'; break }
    if (c === 'partially_correct' && worst === 'correct') worst = 'partially_correct'
  }
  return map[worst] || '未评审'
})
const crCorrectnessTag = computed(() => {
  const label = crCorrectnessLabel.value
  if (label.includes('正确')) return 'success'
  if (label.includes('严重')) return 'danger'
  return 'warning'
})
const crP0Count = computed(() => codeReviewFindings.value.filter((f) => f.priority === 'P0').length)
const crP1Count = computed(() => codeReviewFindings.value.filter((f) => f.priority === 'P1').length)
function priorityTag(p: string): string {
  return { P0: 'danger', P1: 'warning', P2: 'primary', P3: 'info' }[p] || 'info'
}

// 泄漏类型/严重度映射
const leakTypeMap: Record<string, string> = { system_prompt_leak: 'Prompt 泄露', injection_attempt: '注入攻击', token_abuse: '踩额度', credential_leak: '凭据泄露' }
const severityMap: Record<string, string> = { critical: '严重', high: '高危', medium: '中', low: '低' }

// 雷达图维度数据(从 report.dimensions 提取)
const radarOption = computed(() => {
  const dims = report.value?.dimensions || {}
  const keys = Object.keys(dims)
  return {
    tooltip: {},
    radar: {
      indicator: keys.map((k) => ({ name: dimLabel(k), max: 100 })),
      shape: 'polygon',
      splitArea: { areaStyle: { color: ['#fafafa', '#f0f2f5'] } },
    },
    series: [{
      type: 'radar',
      data: [{
        value: keys.map((k) => dims[k]),
        name: props.task.name,
        areaStyle: { color: 'rgba(64,158,255,0.3)' },
        lineStyle: { color: '#409eff' },
      }],
    }],
  } as any
})

// 维度明细表数据(供 el-table 渲染)
const dimRows = computed(() => {
  const dims = report.value?.dimensions || {}
  return Object.entries(dims).map(([k, v]) => ({
    name: dimLabel(k),
    score: v as number,
  }))
})

// 维度柱状图(与雷达图互补,柱状更易读具体分差)
const barOption = computed(() => {
  const dims = report.value?.dimensions || {}
  const keys = Object.keys(dims)
  return {
    tooltip: { trigger: 'axis', formatter: (p: any) => `${p[0].name}: ${p[0].value} 分` },
    xAxis: {
      type: 'category',
      data: keys.map((k) => dimLabel(k)),
      axisLabel: { fontSize: 11 },
    },
    yAxis: { type: 'value', min: 0, max: 100 },
    series: [{
      type: 'bar',
      data: keys.map((k) => ({
        value: dims[k],
        itemStyle: { color: dims[k] >= 80 ? '#67c23a' : dims[k] >= 60 ? '#e6a23c' : '#f56c6c' },
      })),
      barWidth: '50%',
      label: { show: true, position: 'top', formatter: '{c}' },
      markLine: {
        silent: true,
        data: [{ yAxis: 60, lineStyle: { color: '#f56c6c', type: 'dashed' } }],
      },
    }],
  } as any
})

// 圆桌合议:按题聚合的详情
const roundtableDetails = computed(() => {
  if (!hasRoundtable.value) return []
  const perQ = report.value.per_question || []
  return report.value.roundtable.map((rt: any, i: number) => ({
    index: i + 1,
    finalScore: rt.final_score,
    leaderNote: rt.leader_note,
    opinions: rt.opinions || [],
    isMock: rt.is_mock,
    answer: perQ[i]?.answer || '',
    title: perQ[i]?.title || '',
  }))
})

// 圆桌合议:专家汇总统计(所有题的各专家平均分)
const expertSummary = computed(() => {
  if (!hasRoundtable.value) return []
  const expertMap: Record<string, { scores: number[]; dimension: string }> = {}
  for (const rt of report.value.roundtable) {
    for (const op of rt.opinions || []) {
      if (!expertMap[op.role]) {
        expertMap[op.role] = { scores: [], dimension: op.dimension }
      }
      expertMap[op.role].scores.push(op.score)
    }
  }
  return Object.entries(expertMap).map(([role, data]) => ({
    role,
    label: expertLabel(role),
    dimension: dimLabel(data.dimension),
    avgScore: data.scores.length > 0
      ? Math.round(data.scores.reduce((a, b) => a + b, 0) / data.scores.length)
      : 0,
    count: data.scores.length,
  }))
})

// 维度中文名映射
function dimLabel(k: string): string {
  return {
    security: '安全', performance: '性能', architecture: '架构',
    correctness: '正确性', product: '产品',
  }[k] || k
}

// 专家角色中文名
function expertLabel(role: string): string {
  return {
    expert_security: '安全专家',
    expert_perf: '性能专家',
    expert_arch: '架构专家',
    expert_product: '产品专家',
  }[role] || role
}

// 专家角色图标颜色
function expertColor(role: string): string {
  return {
    expert_security: '#f56c6c',
    expert_perf: '#e6a23c',
    expert_arch: '#409eff',
    expert_product: '#67c23a',
  }[role] || '#909399'
}

// 专家角色图标
function expertIcon(role: string): string {
  return {
    expert_security: 'Lock',
    expert_perf: 'TrendCharts',
    expert_arch: 'Grid',
    expert_product: 'ShoppingCart',
  }[role] || 'User'
}

// 分数颜色
function scoreColor(score: number): string {
  if (score >= 80) return '#67c23a'
  if (score >= 60) return '#e6a23c'
  return '#f56c6c'
}

// 圆桌详情展开状态
const expandedQuestions = ref<Set<number>>(new Set())
function toggleExpand(index: number) {
  if (expandedQuestions.value.has(index)) {
    expandedQuestions.value.delete(index)
  } else {
    expandedQuestions.value.add(index)
  }
}

// 查看完整回答弹窗
const answerDialogVisible = ref(false)
const answerDialogText = ref('')
const answerDialogTitle = ref('')
function viewAnswer(rt: any) {
  answerDialogTitle.value = rt.title || `第 ${rt.index} 题`
  answerDialogText.value = rt.answer || ''
  answerDialogVisible.value = true
}
</script>

<template>
  <div>
    <!-- 任务未完成 / 无报告 -->
    <el-empty
      v-if="!report"
      :description="task.status === 'running' ? '评测运行中,请稍候...' : '该任务尚未产出报告'"
      :image-size="80"
    />

    <!-- 有报告 -->
    <div v-else class="report">
      <!-- 总分卡片 -->
      <div class="score-bar">
        <div class="overall">
          <span class="num">{{ report.overall?.toFixed(1) }}</span>
          <span class="lbl">综合得分</span>
        </div>
        <div class="score-tags">
          <el-tag v-if="hasRoundtable" type="primary" size="large">
            🏛️ 圆桌合议
          </el-tag>
          <el-tag v-if="report.mock_mode" type="warning" size="small">Mock 模式</el-tag>
          <el-tag v-if="report.bad_cases" type="danger" size="large">
            ⚠ {{ report.bad_cases }} 个 Bad Case
          </el-tag>
          <el-tag v-if="breakerInfo?.tripped" type="danger" size="small">🔴 已熔断</el-tag>
          <el-tag v-else-if="breakerInfo" type="success" size="small">🟢 钱包安全</el-tag>
        </div>
        <el-button type="primary" plain size="small" @click="exportReport" style="margin-left:auto">
          📄 导出报告
        </el-button>
      </div>

      <!-- 断路器明细(费用/token 透明化) -->
      <div v-if="breakerInfo" class="breaker-info">
        <span>💰 Token: {{ breakerInfo.total_tokens }}</span>
        <span>🔄 轮数: {{ breakerInfo.total_turns }}</span>
        <span>💵 估算费用: ${{ breakerInfo.est_cost_usd }}</span>
        <span v-if="breakerInfo.tripped" class="trip-reason">⚠ {{ breakerInfo.trip_reason }}</span>
      </div>

      <!-- 雷达图 + 柱状图 并排 -->
      <el-row :gutter="12">
        <el-col :span="12">
          <div class="chart-wrap">
            <div class="chart-title">维度雷达图</div>
            <BaseChart :option="radarOption" height="300px" />
          </div>
        </el-col>
        <el-col :span="12">
          <div class="chart-wrap">
            <div class="chart-title">维度得分对比</div>
            <BaseChart :option="barOption" height="300px" />
          </div>
        </el-col>
      </el-row>

      <!-- 沙箱执行指标(仅 agent 场景,真实编译过才有) -->
      <el-card v-if="sandboxSummary" shadow="never" class="sandbox-card">
        <template #header>🏭 沙箱执行指标(真实编译验证)</template>
        <el-row :gutter="16">
          <el-col :span="6">
            <div class="metric">
              <div class="m-num" :style="{ color: scoreColor(sandboxSummary.pass_rate) }">{{ sandboxSummary.pass_rate }}%</div>
              <div class="m-lbl">编译成功率</div>
            </div>
          </el-col>
          <el-col :span="6">
            <div class="metric">
              <div class="m-num">{{ sandboxSummary.compile_ok }}/{{ sandboxSummary.compile_ok + sandboxSummary.compile_fail }}</div>
              <div class="m-lbl">编译 成功/总数</div>
            </div>
          </el-col>
          <el-col :span="6">
            <div class="metric">
              <div class="m-num">{{ sandboxSummary.tests_passed }}/{{ sandboxSummary.total_tests }}</div>
              <div class="m-lbl">测试 通过/总数</div>
            </div>
          </el-col>
          <el-col :span="6">
            <div class="metric">
              <div class="m-num">{{ sandboxSummary.avg_compile_ms }}<span class="m-unit">ms</span></div>
              <div class="m-lbl">平均编译耗时</div>
            </div>
          </el-col>
        </el-row>
      </el-card>

      <!-- 混沌工程鲁棒性(仅开启混沌的任务有) -->
      <el-card v-if="chaosSummary" shadow="never" class="sandbox-card">
        <template #header>
          🌪️ 混沌对抗 — 鲁棒性 {{ chaosSummary.avg_score }} 分
        </template>
        <el-table :data="chaosSummary.results" stripe size="small">
          <el-table-column prop="type" label="注入类型" width="140" />
          <el-table-column prop="description" label="描述" min-width="180" />
          <el-table-column prop="impact" label="影响" min-width="200" show-overflow-tooltip />
          <el-table-column label="鲁棒性" width="100">
            <template #default="{ row }">
              <el-tag :type="row.score >= 80 ? 'success' : row.score >= 60 ? 'warning' : 'danger'" size="small">
                {{ row.score }}
              </el-tag>
            </template>
          </el-table-column>
        </el-table>
      </el-card>

      <!-- 反泄漏扫描(V2 9.5) -->
      <el-card v-if="leakageSummary" shadow="never" :class="['sandbox-card', leakageSummary.failed_scans > 0 ? 'leakage-warn' : 'leakage-ok']">
        <template #header>
          🛡️ 反泄漏扫描 — 安全得分 {{ leakageSummary.avg_score }}
          <el-tag v-if="leakageSummary.failed_scans > 0" type="danger" size="small" style="margin-left:8px">
            {{ leakageSummary.failed_scans }} 项隐患
          </el-tag>
          <el-tag v-else type="success" size="small" style="margin-left:8px">全通过</el-tag>
        </template>
        <el-table v-if="leakageSummary.findings?.length" :data="leakageSummary.findings" stripe size="small">
          <el-table-column label="类型" width="140">
            <template #default="{ row }">
              <el-tag size="small" :type="row.severity === 'critical' ? 'danger' : row.severity === 'high' ? 'warning' : 'info'">
                {{ leakTypeMap[row.type] || row.type }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="严重度" width="80">
            <template #default="{ row }">{{ severityMap[row.severity] || row.severity }}</template>
          </el-table-column>
          <el-table-column prop="detail" label="描述" min-width="200" show-overflow-tooltip />
          <el-table-column prop="evidence" label="证据" width="160" show-overflow-tooltip>
            <template #default="{ row }"><code class="evidence">{{ row.evidence }}</code></template>
          </el-table-column>
        </el-table>
        <el-empty v-else description="🎉 未发现安全隐患" :image-size="50" />
      </el-card>

      <!-- ═══ Agent 能力画像(自适应评测) ═══ -->
      <div v-if="report.agent_profile" class="profile-section">
        <el-divider content-position="left">
          <span class="section-title">🧩 Agent 能力画像</span>
        </el-divider>
        <div class="profile-grid">
          <div class="profile-item">
            <span class="profile-label">类别</span>
            <el-tag type="primary" size="small">{{ report.agent_profile.category }}</el-tag>
          </div>
          <div class="profile-item">
            <span class="profile-label">领域</span>
            <el-tag type="info" size="small">{{ report.agent_profile.domain }}</el-tag>
          </div>
          <div class="profile-item" v-if="report.agent_profile.is_mock">
            <span class="profile-label">分析方式</span>
            <el-tag type="warning" size="small">🎭 启发式推断</el-tag>
          </div>
        </div>
        <div class="profile-row" v-if="report.agent_profile.capabilities?.length">
          <span class="profile-label">能力</span>
          <el-tag v-for="c in report.agent_profile.capabilities" :key="c" type="success" size="small" effect="plain" style="margin-right:6px">{{ c }}</el-tag>
        </div>
        <div class="profile-row" v-if="report.agent_profile.eval_focus?.length">
          <span class="profile-label">评测重点</span>
          <el-tag v-for="f in report.agent_profile.eval_focus" :key="f" type="warning" size="small" effect="plain" style="margin-right:6px">{{ f }}</el-tag>
        </div>
        <div class="profile-row" v-if="report.agent_profile.core_strengths?.length">
          <span class="profile-label">核心素养</span>
          <el-tag v-for="s in report.agent_profile.core_strengths" :key="s" type="primary" size="small" effect="plain" style="margin-right:6px">{{ s }}</el-tag>
        </div>
        <div class="profile-row" v-if="report.agent_profile.custom_rubric">
          <span class="profile-label">评审标准</span>
          <span class="profile-value">{{ report.agent_profile.custom_rubric }}</span>
        </div>
      </div>

      <!-- 维度明细表 -->
      <el-table :data="dimRows" stripe size="small" style="margin-top: 12px">
        <el-table-column prop="name" label="维度" width="120" />
        <el-table-column prop="score" label="得分">
          <template #default="{ row }">
            <el-progress
              :percentage="row.score"
              :color="scoreColor(row.score)"
              :stroke-width="14"
              :text-inside="true"
            />
          </template>
        </el-table-column>
      </el-table>

      <!-- ═══ 代码评审 Findings(P0-P3) ═══ -->
      <div v-if="codeReviewFindings.length" class="codereview-section">
        <el-divider content-position="left">
          <span class="section-title">🔍 代码评审 Findings</span>
        </el-divider>
        <div class="cr-summary">
          <el-tag :type="crCorrectnessTag" size="small">{{ crCorrectnessLabel }}</el-tag>
          <span class="cr-stat">共 {{ codeReviewFindings.length }} 个发现</span>
          <span v-if="crP0Count" class="cr-p0">🔴 P0 ×{{ crP0Count }}</span>
          <span v-if="crP1Count" class="cr-p1">🟠 P1 ×{{ crP1Count }}</span>
          <span v-if="codeReviewIsMock" class="cr-mock">🎭 启发式(mock)</span>
        </div>
        <el-table :data="codeReviewFindings" stripe size="small" style="margin-top:8px">
          <el-table-column prop="priority" label="级别" width="70">
            <template #default="{ row }">
              <el-tag :type="priorityTag(row.priority)" size="small" effect="dark">{{ row.priority }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="title" label="问题" min-width="180" show-overflow-tooltip />
          <el-table-column prop="body" label="描述与建议" min-width="250" show-overflow-tooltip />
          <el-table-column prop="confidence_score" label="置信度" width="90">
            <template #default="{ row }">{{ (row.confidence_score * 100).toFixed(0) }}%</template>
          </el-table-column>
          <el-table-column prop="code_location" label="位置" width="120" show-overflow-tooltip />
        </el-table>
      </div>

      <!-- ═══ 圆桌合议详情 ═══ -->
      <div v-if="hasRoundtable" class="roundtable-section">
        <el-divider content-position="left">
          <span class="section-title">🏛️ 圆桌合议详情</span>
        </el-divider>

        <!-- 专家汇总 -->
        <div class="expert-summary">
          <div v-for="exp in expertSummary" :key="exp.role" class="expert-card">
            <div class="expert-header">
              <el-icon :size="20" :color="expertColor(exp.role)">
                <component :is="expertIcon(exp.role)" />
              </el-icon>
              <span class="expert-name">{{ exp.label }}</span>
              <span class="expert-dim">{{ exp.dimension }}</span>
            </div>
            <div class="expert-score" :style="{ color: expertColor(exp.role) }">
              {{ exp.avgScore }}
            </div>
            <div class="expert-count">{{ exp.count }} 题平均</div>
          </div>
        </div>

        <!-- 按题展开的圆桌详情 -->
        <div class="rt-questions">
          <div
            v-for="rt in roundtableDetails"
            :key="rt.index"
            class="rt-question-card"
          >
            <div class="rt-q-header" @click="toggleExpand(rt.index)">
              <div class="rt-q-title">
                <span class="rt-q-index">#{{ rt.index }}</span>
                <span>题目 {{ rt.index }}</span>
              </div>
              <div class="rt-q-score">
                <span class="rt-q-score-num" :style="{ color: scoreColor(rt.finalScore) }">
                  {{ rt.finalScore }}
                </span>
                <span class="rt-q-score-lbl">综合分</span>
              </div>
              <el-button size="small" text type="primary" @click.stop="viewAnswer(rt)" title="查看 Agent 完整回答">
                查看回答
              </el-button>
              <el-icon :class="{ 'is-rotate': expandedQuestions.has(rt.index) }">
                <ArrowDown />
              </el-icon>
            </div>

            <!-- 展开:专家意见 -->
            <div v-if="expandedQuestions.has(rt.index)" class="rt-q-body">
              <!-- 主导者评语 -->
              <div v-if="rt.leaderNote" class="leader-note">
                <div class="leader-label">📋 主导者综合评语</div>
                <pre class="leader-text">{{ rt.leaderNote }}</pre>
              </div>

              <!-- 各专家意见 -->
              <div class="opinions">
                <div v-for="op in rt.opinions" :key="op.role" class="opinion-item">
                  <div class="opinion-header">
                    <el-icon :size="16" :color="expertColor(op.role)">
                      <component :is="expertIcon(op.role)" />
                    </el-icon>
                    <span class="opinion-role">{{ expertLabel(op.role) }}</span>
                    <span class="opinion-score" :style="{ color: scoreColor(op.score) }">
                      {{ op.score }} 分
                    </span>
                    <el-tag v-if="op.is_mock" type="warning" size="small">Mock</el-tag>
                  </div>
                  <div class="opinion-reason">{{ op.reason }}</div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- ═══ 人工反馈区 ═══ -->
      <div class="feedback-section">
        <el-divider content-position="left">
          <span class="section-title">👥 人工反馈</span>
        </el-divider>

        <div class="feedback-bar">
          <span class="feedback-label">这个评测结果准确吗？</span>
          <el-button-group>
            <el-button :type="taskFeedback === 'thumbs_up' ? 'success' : 'default'" @click="submitTaskFeedback('thumbs_up')">
              <el-icon><CircleCheck /></el-icon> 准确
            </el-button>
            <el-button :type="taskFeedback === 'thumbs_down' ? 'danger' : 'default'" @click="showFeedbackDialog = true">
              <el-icon><CircleClose /></el-icon> 有问题
            </el-button>
          </el-button-group>
          <el-tag v-if="taskFeedback" :type="taskFeedback === 'thumbs_up' ? 'success' : 'danger'" size="small">
            {{ taskFeedback === 'thumbs_up' ? '已标记准确' : '已标记有问题' }}
          </el-tag>
        </div>
      </div>

      <!-- 反馈对话框 -->
      <el-dialog v-model="showFeedbackDialog" title="反馈评测问题" width="500px">
        <el-form label-width="80px">
          <el-form-item label="问题描述">
            <el-input v-model="feedbackNote" type="textarea" :rows="4"
              placeholder="请描述评测结果中的问题，例如：&#10;- 圆桌说没问题，但实际违反了团队设计模式&#10;- 安全专家漏检了 SQL 注入&#10;- 某道题评分过高/过低" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="showFeedbackDialog = false">取消</el-button>
          <el-button type="danger" @click="submitTaskFeedback('thumbs_down')">提交反馈</el-button>
        </template>
      </el-dialog>

      <!-- 查看完整回答弹窗 -->
      <el-dialog v-model="answerDialogVisible" :title="answerDialogTitle" width="640px">
        <pre class="answer-full">{{ answerDialogText }}</pre>
      </el-dialog>
    </div>
  </div>
</template>

<style scoped>
.score-bar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.score-tags { display: flex; gap: 8px; align-items: center; }
.overall { display: flex; align-items: baseline; gap: 8px; }
.num { font-size: 36px; font-weight: 700; color: #409eff; }
.lbl { color: #999; font-size: 13px; }
.chart-wrap { background: #fff; border-radius: 8px; padding: 12px; }
.chart-title { font-size: 13px; font-weight: 600; color: #606266; margin-bottom: 8px; text-align: center; }

/* 断路器明细 */
.breaker-info {
  display: flex;
  gap: 20px;
  font-size: 12px;
  color: #909399;
  padding: 6px 0 10px;
  flex-wrap: wrap;
}
.trip-reason { color: #f56c6c; font-weight: 500; }

/* 沙箱指标卡 */
.sandbox-card { margin-top: 12px; margin-bottom: 4px; }

/* Agent 能力画像 */
.profile-section { margin-top: 12px; }
.profile-grid { display: flex; gap: 16px; flex-wrap: wrap; margin-bottom: 8px; }
.profile-item { display: flex; align-items: center; gap: 6px; }
.profile-row { display: flex; align-items: flex-start; gap: 8px; margin-top: 6px; flex-wrap: wrap; }
.profile-label { font-size: 13px; color: #909399; min-width: 60px; font-weight: 600; }
.profile-value { font-size: 13px; color: #606266; }

/* 代码评审 Findings */
.codereview-section { margin-top: 12px; }
.cr-summary { display: flex; align-items: center; gap: 12px; margin-bottom: 8px; }
.cr-stat { font-size: 13px; color: #909399; }
.cr-p0 { font-size: 13px; color: #f56c6c; font-weight: 600; }
.cr-p1 { font-size: 13px; color: #e6a23c; font-weight: 600; }
.cr-mock { font-size: 12px; color: #c0c4cc; }
.metric { text-align: center; }
.m-num { font-size: 22px; font-weight: 700; color: #409eff; }
.m-unit { font-size: 13px; color: #999; margin-left: 2px; }
.m-lbl { font-size: 12px; color: #999; margin-top: 4px; }

/* 泄漏扫描 */
.leakage-ok { border-left: 3px solid #67c23a; }
.leakage-warn { border-left: 3px solid #f56c6c; }
.evidence { font-size: 11px; background: #f5f7fa; padding: 1px 4px; border-radius: 2px; }

/* 圆桌合议 */
.roundtable-section { margin-top: 20px; }
.section-title { font-size: 16px; font-weight: 600; color: #303133; }

/* 专家汇总卡片 */
.expert-summary { display: flex; gap: 16px; margin-bottom: 20px; flex-wrap: wrap; }
.expert-card {
  flex: 1;
  min-width: 140px;
  background: #fff;
  border-radius: 8px;
  padding: 16px;
  text-align: center;
  border: 1px solid #ebeef5;
}
.expert-header { display: flex; align-items: center; justify-content: center; gap: 6px; margin-bottom: 8px; }
.expert-name { font-weight: 600; font-size: 14px; }
.expert-dim { font-size: 12px; color: #999; }
.expert-score { font-size: 32px; font-weight: 700; }
.expert-count { font-size: 12px; color: #999; margin-top: 4px; }

/* 按题圆桌详情 */
.rt-questions { display: flex; flex-direction: column; gap: 12px; }
.rt-question-card {
  background: #fff;
  border-radius: 8px;
  border: 1px solid #ebeef5;
  overflow: hidden;
}
.rt-q-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  cursor: pointer;
  transition: background 0.2s;
}
.rt-q-header:hover { background: #f5f7fa; }
.rt-q-title { display: flex; align-items: center; gap: 8px; font-weight: 500; }
.rt-q-index { color: #409eff; font-weight: 700; }
.rt-q-score { display: flex; align-items: baseline; gap: 6px; }
.rt-q-score-num { font-size: 24px; font-weight: 700; }
.rt-q-score-lbl { font-size: 12px; color: #999; }
.is-rotate { transform: rotate(180deg); transition: transform 0.2s; }

/* 展开内容 */
.rt-q-body { padding: 0 16px 16px; border-top: 1px solid #f0f0f0; }
.leader-note {
  background: #f0f9eb;
  border-radius: 6px;
  padding: 12px;
  margin: 12px 0;
  border-left: 3px solid #67c23a;
}
.leader-label { font-weight: 600; font-size: 13px; color: #67c23a; margin-bottom: 6px; }
.leader-text { margin: 0; font-size: 13px; line-height: 1.6; white-space: pre-wrap; color: #303133; }

/* 专家意见 */
.opinions { display: flex; flex-direction: column; gap: 10px; margin-top: 12px; }
.opinion-item {
  background: #fafafa;
  border-radius: 6px;
  padding: 10px 12px;
  border-left: 3px solid #dcdfe6;
}
.opinion-header { display: flex; align-items: center; gap: 8px; margin-bottom: 6px; }
.opinion-role { font-weight: 600; font-size: 13px; }
.opinion-score { font-size: 15px; font-weight: 700; margin-left: auto; }
.opinion-reason { font-size: 13px; color: #606266; line-height: 1.5; }

/* 人工反馈 */
.feedback-section { margin-top: 20px; }
.feedback-bar {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 16px;
  background: #fff;
  border-radius: 8px;
  border: 1px solid #ebeef5;
}
.feedback-label { font-size: 14px; color: #606266; }

/* 完整回答弹窗 */
.answer-full {
  margin: 0;
  padding: 12px;
  background: #fafafa;
  border-radius: 6px;
  font-size: 13px;
  line-height: 1.7;
  white-space: pre-wrap;
  max-height: 500px;
  overflow-y: auto;
  border: 1px solid #ebeef5;
}
</style>
