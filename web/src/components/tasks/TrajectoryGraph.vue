<script setup lang="ts">
// TrajectoryGraph.vue — Agent 思考轨迹拓扑图(MRD v2 7.2)。
//
// 将评测会话转化为 ECharts Graph,展示 Agent 的"思维链路":
//   出题 → Agent回答 → (Tool调用) → (Tool结果) → 出题 → ...
// 每个节点用颜色区分:🟡用户 / 🔵Agent / 🟢Tool / 🔴异常
//
// 用法: <TrajectoryGraph :conversations="convList" />
import { computed } from 'vue'
import BaseChart from '../BaseChart.vue'

interface Message { role: string; content: string }
interface Conversation {
  id: string; turn: number; messages: Message[]
  token_in: number; token_out: number
}

const props = defineProps<{ conversations: Conversation[] }>()

const graphOption = computed(() => {
  const convs = props.conversations
  if (!convs.length) return {}

  const nodes: any[] = []
  const links: any[] = []
  const colors: Record<string, string> = {
    user: '#e6a23c',      // 🟡 用户出题
    assistant: '#409eff',  // 🔵 Agent 回答
    tool: '#67c23a',       // 🟢 工具调用
    error: '#f56c6c',      // 🔴 异常
  }

  let nodeId = 0
  const addNode = (label: string, category: string) => {
    const id = `n${nodeId++}`
    nodes.push({
      id,
      name: label,
      symbolSize: category === 'error' ? 34 : category === 'user' ? 28 : 24,
      itemStyle: { color: colors[category] || '#909399' },
      category: category === 'user' ? 0 : category === 'assistant' ? 1 : 2,
    })
    return id
  }

  let prevId: string | null = null
  for (const c of convs) {
    for (let i = 0; i < c.messages.length; i++) {
      const m = c.messages[i]
      const role = m.role === 'user' ? 'user' : (m.role === 'assistant' ? 'assistant' : 'tool')
      // 截断标签(节点显示摘要)
      const label = `[T${c.turn}] ${role === 'user' ? '📝' : '🤖'} ${truncNodeLabel(m.content)}`
      const id = addNode(label, role)
      if (prevId) {
        links.push({ source: prevId, target: id, lineStyle: { color: '#c0c4cc', width: 1.5, curveness: 0.2 } })
      }
      prevId = id
    }
  }

  return {
    tooltip: { formatter: (p: any) => p.data?.name?.slice(0, 80) },
    legend: { bottom: 0, data: ['用户', 'Agent', 'Tool'] },
    series: [{
      type: 'graph',
      layout: 'force',
      force: {
        repulsion: 200,
        edgeLength: [80, 200],
        gravity: 0.1,
      },
      roam: true,
      draggable: true,
      label: { show: true, fontSize: 10, formatter: (p: any) => p.name.slice(0, 15) },
      edgeSymbol: ['none', 'arrow'],
      edgeSymbolSize: [0, 8],
      data: nodes,
      links,
      categories: [
        { name: '用户', itemStyle: { color: '#e6a23c' } },
        { name: 'Agent', itemStyle: { color: '#409eff' } },
        { name: 'Tool', itemStyle: { color: '#67c23a' } },
      ],
      lineStyle: { color: '#c0c4cc', width: 1.5, curveness: 0.2 },
      emphasis: { focus: 'adjacency' },
    }],
  } as any
})

function truncNodeLabel(content: string): string {
  if (!content) return '(空)'
  const r = content.replace(/\n/g, ' ').trim()
  return r.length > 25 ? r.slice(0, 25) + '…' : r
}
</script>

<template>
  <BaseChart :option="graphOption" height="340px" />
</template>
