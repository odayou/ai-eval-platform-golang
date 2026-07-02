<script setup lang="ts">
// XtermTerminal.vue — MRD v2 10.2:虚拟化终端日志矩阵。
//
// 用 xterm.js 复现 Agent 执行时的终端效果(黑底绿字)
// 将对话记录渲染为终端会话风格:提示符 → 出题 → 回答 → 下一轮
//
// 指标(MRD 原话):"1 万行/秒,CPU < 5%"

import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { Terminal } from 'xterm'
import { FitAddon } from '@xterm/addon-fit'
import 'xterm/css/xterm.css'

interface Message { role: string; content: string }
interface Conversation {
  id: string; turn: number; messages: Message[]
  token_in: number; token_out: number
}

const props = defineProps<{ conversations: Conversation[] }>()

const el = ref<HTMLDivElement>()
let term: Terminal | null = null
let fitAddon: FitAddon | null = null

// 颜色方案:绿色终端经典配色
const GREEN = '\x1b[32m'
const CYAN = '\x1b[36m'
const YELLOW = '\x1b[33m'
const RESET = '\x1b[0m'
const BOLD = '\x1b[1m'
const DIM = '\x1b[2m'

function renderConversations() {
  if (!term) return
  term.clear()
  term.writeln(`${CYAN}╔══════════════════════════════════════════╗${RESET}`)
  term.writeln(`${CYAN}║  AI Eval Platform — Agent Terminal Log  ║${RESET}`)
  term.writeln(`${CYAN}╚══════════════════════════════════════════╝${RESET}`)
  term.writeln('')

  for (const c of props.conversations) {
    term.writeln(`${BOLD}${YELLOW}─── Turn ${c.turn} ─── in:${c.token_in} out:${c.token_out} ───${RESET}`)
    for (const m of c.messages) {
      if (m.role === 'user') {
        term.writeln(`${CYAN}$ eval-prompt${RESET}`)
        // 出题:分行显示,加行号前缀
        const lines = m.content.split('\n')
        for (const line of lines) {
          term.writeln(`${DIM}  > ${RESET}${line}`)
        }
      } else {
        term.writeln(`${GREEN}$ agent-response${RESET}`)
        const lines = m.content.split('\n')
        const maxShow = 20
        for (let i = 0; i < Math.min(lines.length, maxShow); i++) {
          term.writeln(`  ${lines[i]}`)
        }
        if (lines.length > maxShow) {
          term.writeln(`${DIM}  ... (${lines.length - maxShow} more lines)${RESET}`)
        }
      }
      term.writeln('')
    }
  }

  // 如果无对话,显示启动提示
  if (!props.conversations.length) {
    term.writeln(`${DIM}Waiting for evaluation to start...${RESET}`)
    term.writeln(`${DIM}Agent output will appear here when the task runs.${RESET}`)
  }
}

onMounted(() => {
  if (!el.value) return
  term = new Terminal({
    theme: {
      background: '#0d1117',
      foreground: '#c9d1d9',
      cursor: '#58a6ff',
      selectionBackground: '#264f78',
      black: '#484f58',
      green: '#3fb950',
      yellow: '#d29922',
      cyan: '#39c5cf',
    },
    fontSize: 13,
    fontFamily: 'Consolas, "Cascadia Code", "Courier New", monospace',
    cursorBlink: true,
    disableStdin: true,  // 只读终端
    rows: 24,
  })
  fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.open(el.value)
  fitAddon.fit()
  renderConversations()
})

// conversations 变化时重新渲染
watch(() => props.conversations, renderConversations, { deep: true })

// resize 时自适应
const handleResize = () => { fitAddon?.fit() }
onMounted(() => window.addEventListener('resize', handleResize))
onBeforeUnmount(() => {
  window.removeEventListener('resize', handleResize)
  term?.dispose()
})
</script>

<template>
  <div ref="el" class="xterm-container"></div>
</template>

<style scoped>
.xterm-container {
  width: 100%;
  border-radius: 6px;
  overflow: hidden;
  border: 1px solid #30363d;
}
</style>
