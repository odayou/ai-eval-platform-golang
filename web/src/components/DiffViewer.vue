<script setup lang="ts">
// DiffViewer.vue — Monaco Diff Editor 集成(MRD 10.2 ③)。
//
// 使用 Monaco Editor 的 Side-by-Side Diff 模式,提供:
//   - 语法高亮(自动检测语言)
//   - 行内差异高亮
//   - 虚拟滚动(Monaco 内置,大文件也流畅)
//   - 并排对比视图
//   - 回退方案:Monaco 加载失败时使用纯 CSS diff
//
// Props:
//   left/right — 内容
//   leftLabel/rightLabel — 标签

import { computed, ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { diffLines, type Change } from 'diff'

const props = withDefaults(defineProps<{
  left: string
  right: string
  leftLabel?: string
  rightLabel?: string
}>(), {
  leftLabel: '原版',
  rightLabel: '新版',
})

const containerRef = ref<HTMLDivElement | null>(null)
const monacoReady = ref(false)
const useFallback = ref(false)
let monacoEditor: any = null

// ── Monaco 加载(动态 import,不影响首屏)──
async function loadMonaco() {
  try {
    const monaco = await import('monaco-editor')
    monacoReady.value = true
    await nextTick()
    initMonaco(monaco)
  } catch (e) {
    console.warn('[DiffViewer] Monaco 不可用,使用 CSS fallback:', e)
    useFallback.value = true
  }
}

function initMonaco(monaco: any) {
  if (!containerRef.value) return

  const lang = detectLang(props.left || props.right)
  const original = monaco.editor.createModel(props.left, lang)
  const modified = monaco.editor.createModel(props.right, lang)

  const editor = monaco.editor.createDiffEditor(containerRef.value, {
    theme: 'vs-dark',
    automaticLayout: true,
    readOnly: true,
    renderSideBySide: true,
    originalEditable: false,
    enableSplitViewResizing: true,
    renderIndicators: true,
    ignoreTrimWhitespace: false,
    minimap: { enabled: false },
    scrollBeyondLastLine: false,
    fontSize: 13,
  })

  editor.setModel({ original, modified })
  monacoEditor = editor
}

function detectLang(text: string): string {
  if (!text) return 'plaintext'
  if (/func |package |import \(/.test(text)) return 'go'
  if (/def |import os|from \w+ import/.test(text)) return 'python'
  if (/(const |let |var |async function|module\.exports)/.test(text)) return 'javascript'
  if (/(<template|<script|<style|<\/)/.test(text)) return 'html'
  if (/(SELECT |CREATE TABLE|INSERT INTO)/i.test(text)) return 'sql'
  if (/[{].*".*:/.test(text)) return 'json'
  return 'plaintext'
}

watch([() => props.left, () => props.right], () => {
  if (!monacoEditor) return
  const oModel = monacoEditor.getOriginalEditor().getModel()
  const mModel = monacoEditor.getModifiedEditor().getModel()
  if (oModel) oModel.setValue(props.left)
  if (mModel) mModel.setValue(props.right)
})

onMounted(() => loadMonaco())
onUnmounted(() => { if (monacoEditor) monacoEditor.dispose() })

// ── CSS Fallback ──
const fallbackLines = computed(() => {
  if (!useFallback.value) return []
  const changes: Change[] = diffLines(props.left, props.right)
  const result: any[] = []
  let ln = 0, rn = 0
  for (const ch of changes) {
    const ls = ch.value.split('\n')
    if (ls[ls.length - 1] === '') ls.pop()
    for (const l of ls) {
      if (ch.added) result.push({ type: 'add', leftNum: null, rightNum: ++rn, content: l })
      else if (ch.removed) result.push({ type: 'remove', leftNum: ++ln, rightNum: null, content: l })
      else result.push({ type: 'same', leftNum: ++ln, rightNum: ++rn, content: l })
    }
  }
  return result
})

const fallbackStats = computed(() => {
  let a = 0, r = 0
  for (const l of fallbackLines.value) { if (l.type === 'add') a++; else if (l.type === 'remove') r++ }
  return { added: a, removed: r }
})
</script>

<template>
  <div class="diff-root">
    <!-- Monaco 模式 -->
    <div v-if="!useFallback" class="monaco-zone">
      <div v-if="!monacoReady" class="monaco-loading">
        <el-icon class="is-loading" :size="20"><Loading /></el-icon>
        加载 Monaco Diff Editor...
      </div>
      <div ref="containerRef" class="monaco-box" :style="{ visibility: monacoReady ? 'visible' : 'hidden' }" />
      <div class="monaco-labels">
        <span>{{ leftLabel }}</span>
        <span>{{ rightLabel }}</span>
      </div>
    </div>

    <!-- CSS Fallback 模式 -->
    <div v-else class="fallback-root">
      <div class="fb-stats">
        <span class="fb-add">+{{ fallbackStats.added }}</span>
        <span class="fb-rm">-{{ fallbackStats.removed }}</span>
      </div>
      <div class="fb-lines">
        <div v-for="(l, i) in fallbackLines" :key="i" :class="['fb-line', 'fb-' + l.type]">
          <span class="fb-ln">{{ l.leftNum ?? '' }}</span>
          <span class="fb-ln">{{ l.rightNum ?? '' }}</span>
          <span class="fb-txt">
            <span class="fb-prefix">{{ l.type === 'add' ? '+' : l.type === 'remove' ? '-' : ' ' }}</span>
            {{ l.content || ' ' }}
          </span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.diff-root { border: 1px solid #2a2a3e; border-radius: 6px; overflow: hidden; }

/* Monaco */
.monaco-zone { position: relative; }
.monaco-loading { display: flex; align-items: center; justify-content: center; gap: 8px; height: 200px; background: #1e1e1e; color: #909399; font-size: 14px; }
.monaco-box { width: 100%; height: 450px; }
.monaco-labels { display: flex; justify-content: space-between; padding: 4px 16px; background: #252526; border-top: 1px solid #3a3a4e; font-size: 11px; color: #909399; }

/* Fallback */
.fallback-root { font-family: Consolas, "Cascadia Code", monospace; font-size: 12px; line-height: 1.5; }
.fb-stats { display: flex; gap: 12px; padding: 6px 12px; background: #f6f8fa; border-bottom: 1px solid #d0d7de; font-weight: 600; font-size: 13px; }
.fb-add { color: #1a7f37; } .fb-rm { color: #cf222e; }
.fb-lines { max-height: 500px; overflow-y: auto; }
.fb-line { display: flex; min-height: 20px; }
.fb-add { background: #e6ffec; } .fb-remove { background: #ffebe9; } .fb-same { background: #fff; }
.fb-ln { width: 48px; text-align: right; padding: 0 6px; color: #8c959f; user-select: none; flex-shrink: 0; border-right: 1px solid #eceff2; }
.fb-txt { flex: 1; padding: 0 8px; white-space: pre; overflow: hidden; }
.fb-prefix { font-weight: 700; } .fb-add .fb-prefix { color: #1a7f37; } .fb-remove .fb-prefix { color: #cf222e; }
</style>
