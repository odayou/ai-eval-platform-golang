<script setup lang="ts">
// RuleGraph.vue — 规则知识图谱可视化(MRD 4.5 / 7.3)。
//
// 功能:
//   - Canvas 力导向布局图:节点=规则,边=关系(同维度/关联/反哺)
//   - 按 dimension 聚类着色
//   - 点击节点查看规则详情
//   - 支持拖拽/缩放/全屏
//   - 无需外部依赖(G6),纯 Canvas 实现
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import api from '../utils/api'

interface GraphNode {
  id: string; name: string; dimension: string; group: string
  x: number; y: number; color: string; score: number
  // 运行时状态
  vx?: number; vy?: number
  r?: number
}
interface GraphEdge {
  source: string; target: string; label: string; type: string
}

const visible = ref(false)
const loading = ref(false)
const nodes = ref<GraphNode[]>([])
const edges = ref<GraphEdge[]>([])
const selectedNode = ref<GraphNode | null>(null)
const canvasRef = ref<HTMLCanvasElement | null>(null)

// 交互状态
let animFrame = 0
let scale = 1
let offsetX = 0
let offsetY = 0
let dragging = false
let dragNode: GraphNode | null = null
let lastX = 0
let lastY = 0

async function open() {
  visible.value = true
  loading.value = true
  try {
    const res = await api.get('/rules/graph')
    nodes.value = (res.nodes || []).map((n: GraphNode) => ({ ...n, r: 16 + n.score * 8 }))
    edges.value = res.edges || []
    selectedNode.value = null
    scale = 1
    offsetX = 0
    offsetY = 0
    await nextTick()
    startSimulation()
  } catch (e: any) {
    console.error('加载知识图谱失败:', e)
  } finally {
    loading.value = false
  }
}

function close() {
  visible.value = false
  if (animFrame) cancelAnimationFrame(animFrame)
}

function startSimulation() {
  if (animFrame) cancelAnimationFrame(animFrame)

  // 力导向模拟(简化版)
  let iteration = 0
  const maxIterations = 200
  const nodeMap = new Map(nodes.value.map(n => [n.id, n]))

  function step() {
    if (iteration >= maxIterations) { draw(); return }
    iteration++

    // 排斥力(节点之间)
    const repulsion = 2000
    for (const a of nodes.value) {
      for (const b of nodes.value) {
        if (a.id === b.id) continue
        const dx = b.x - a.x
        const dy = b.y - a.y
        const dist = Math.max(Math.sqrt(dx*dx + dy*dy), 1)
        const force = repulsion / (dist * dist)
        const fx = (dx / dist) * force
        const fy = (dy / dist) * force
        a.vx = (a.vx || 0) - fx * 0.01
        a.vy = (a.vy || 0) - fy * 0.01
        b.vx = (b.vx || 0) + fx * 0.01
        b.vy = (b.vy || 0) + fy * 0.01
      }
    }

    // 吸引力(边连接)
    const attraction = 0.005
    for (const e of edges.value) {
      const a = nodeMap.get(e.source)
      const b = nodeMap.get(e.target)
      if (!a || !b) continue
      const dx = b.x - a.x
      const dy = b.y - a.y
      a.vx = (a.vx || 0) + dx * attraction
      a.vy = (a.vy || 0) + dy * attraction
      b.vx = (b.vx || 0) - dx * attraction
      b.vy = (b.vy || 0) - dy * attraction
    }

    // 中心引力
    for (const n of nodes.value) {
      n.vx = (n.vx || 0) - n.x * 0.0001
      n.vy = (n.vy || 0) - n.y * 0.0001
    }

    // 阻尼
    for (const n of nodes.value) {
      if (n === dragNode) continue
      n.vx = (n.vx || 0) * 0.92
      n.vy = (n.vy || 0) * 0.92
      n.x += n.vx || 0
      n.y += n.vy || 0
      // 边界
      n.x = Math.max(n.r || 16, Math.min(780, n.x))
      n.y = Math.max(n.r || 16, Math.min(580, n.y))
    }

    draw()
    animFrame = requestAnimationFrame(step)
  }

  animFrame = requestAnimationFrame(step)
}

function draw() {
  const canvas = canvasRef.value
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  const W = canvas.width
  const H = canvas.height
  ctx.clearRect(0, 0, W, H)

  ctx.save()
  ctx.translate(offsetX + W/2, offsetY + H/2)
  ctx.scale(scale, scale)
  ctx.translate(-400, -300)

  // 边
  const nodeMap = new Map(nodes.value.map(n => [n.id, n]))
  for (const e of edges.value) {
    const a = nodeMap.get(e.source)
    const b = nodeMap.get(e.target)
    if (!a || !b) continue
    ctx.beginPath()
    ctx.moveTo(a.x, a.y)
    ctx.lineTo(b.x, b.y)
    ctx.strokeStyle = e.type === 'related' ? 'rgba(230,162,60,0.3)' : 'rgba(192,196,204,0.15)'
    ctx.lineWidth = e.type === 'related' ? 1.5 : 0.6
    ctx.stroke()
  }

  // 节点
  for (const n of nodes.value) {
    const r = n.r || 16
    // 选中高亮
    if (selectedNode.value?.id === n.id) {
      ctx.beginPath()
      ctx.arc(n.x, n.y, r + 5, 0, Math.PI * 2)
      ctx.fillStyle = 'rgba(255,255,255,0.3)'
      ctx.fill()
      ctx.strokeStyle = '#fff'
      ctx.lineWidth = 2.5
      ctx.stroke()
    }

    // 节点圆
    ctx.beginPath()
    ctx.arc(n.x, n.y, r, 0, Math.PI * 2)
    const grad = ctx.createRadialGradient(n.x - r*0.3, n.y - r*0.3, r*0.1, n.x, n.y, r)
    grad.addColorStop(0, lighten(n.color, 40))
    grad.addColorStop(1, n.color)
    ctx.fillStyle = grad
    ctx.fill()
    ctx.strokeStyle = 'rgba(255,255,255,0.6)'
    ctx.lineWidth = 1.5
    ctx.stroke()

    // 标签
    ctx.fillStyle = '#1a1a2e'
    ctx.font = `${11 + n.score}px "PingFang SC", "Microsoft YaHei", sans-serif`
    ctx.textAlign = 'center'
    ctx.fillText(n.name.slice(0, 8), n.x, n.y + r + 14)
  }

  ctx.restore()
}

function lighten(color: string, amount: number): string {
  const hex = color.replace('#', '')
  const r = Math.min(255, parseInt(hex.slice(0, 2), 16) + amount)
  const g = Math.min(255, parseInt(hex.slice(2, 4), 16) + amount)
  const b = Math.min(255, parseInt(hex.slice(4, 6), 16) + amount)
  return `rgb(${r},${g},${b})`
}

// 鼠标交互
function onMouseDown(e: MouseEvent) {
  const canvas = canvasRef.value
  if (!canvas) return
  const rect = canvas.getBoundingClientRect()
  const mx = (e.clientX - rect.left - offsetX - canvas.width/2) / scale + 400
  const my = (e.clientY - rect.top - offsetY - canvas.height/2) / scale + 300

  // 检查是否点到节点
  const clicked = nodes.value.find(n => {
    const dx = mx - n.x
    const dy = my - n.y
    return Math.sqrt(dx*dx + dy*dy) < (n.r || 16) + 4
  })

  if (clicked) {
    selectedNode.value = clicked
    dragNode = clicked
    dragging = true
  } else {
    selectedNode.value = null
    dragging = true
    dragNode = null
  }
  lastX = e.clientX
  lastY = e.clientY
}

function onMouseMove(e: MouseEvent) {
  if (!dragging) return
  const dx = e.clientX - lastX
  const dy = e.clientY - lastY

  if (dragNode) {
    dragNode.x += dx / scale
    dragNode.y += dy / scale
  } else {
    offsetX += dx
    offsetY += dy
  }
  lastX = e.clientX
  lastY = e.clientY
  draw()
}

function onMouseUp() {
  dragging = false
  dragNode = null
}

function onWheel(e: WheelEvent) {
  e.preventDefault()
  const delta = e.deltaY > 0 ? 0.9 : 1.1
  scale = Math.max(0.3, Math.min(3, scale * delta))
  draw()
}

function resetView() {
  scale = 1; offsetX = 0; offsetY = 0
  selectedNode.value = null
  draw()
}

onMounted(() => { /* canvas 在 open() 后绑定 */ })
onUnmounted(() => { if (animFrame) cancelAnimationFrame(animFrame) })

defineExpose({ open, close })
</script>

<template>
  <!-- 全屏 Dialog -->
  <el-dialog
    v-model="visible"
    title="规则知识图谱"
    width="95vw"
    top="2vh"
    :close-on-click-modal="false"
    destroy-on-close
    @opened="() => canvasRef?.focus()"
  >
    <template #header>
      <div style="display:flex;align-items:center;gap:12px">
        <span>规则知识图谱</span>
        <el-tag v-for="d in [...new Set(nodes.map(n=>n.group))]" :key="d" size="small" :color="nodes.find(n=>n.group===d)?.color" style="color:#fff">
          {{ d === 'security' ? '安全' : d === 'performance' ? '性能' : d === 'architecture' ? '架构' : d === 'product' ? '产品' : d }}
        </el-tag>
        <span style="font-size:12px;color:#909399;margin-left:auto">{{ nodes.length }} 节点 · {{ edges.length }} 边</span>
      </div>
    </template>

    <div v-loading="loading" class="graph-container">
      <canvas
        ref="canvasRef"
        width="800"
        height="600"
        class="graph-canvas"
        @mousedown="onMouseDown"
        @mousemove="onMouseMove"
        @mouseup="onMouseUp"
        @mouseleave="onMouseUp"
        @wheel="onWheel"
      />

      <!-- 选中详情 -->
      <div v-if="selectedNode" class="node-detail">
        <div class="nd-header">
          <span class="nd-dot" :style="{ background: selectedNode.color }"></span>
          {{ selectedNode.name }}
        </div>
        <div class="nd-row"><span>维度</span> {{ selectedNode.group }}</div>
        <div class="nd-row"><span>权重</span> {{ selectedNode.score }}</div>
        <div class="nd-row"><span>关联边</span> {{ edges.filter(e=>e.source===selectedNode!.id||e.target===selectedNode!.id).length }} 条</div>
      </div>

      <!-- 操作栏 -->
      <div class="graph-toolbar">
        <el-button size="small" circle @click="resetView" title="重置视图">
          <el-icon><Refresh /></el-icon>
        </el-button>
        <el-button size="small" circle @click="scale*=1.1;draw()" title="放大">
          <el-icon><ZoomIn /></el-icon>
        </el-button>
        <el-button size="small" circle @click="scale*=0.9;draw()" title="缩小">
          <el-icon><ZoomOut /></el-icon>
        </el-button>
      </div>

      <div class="graph-hint">🖱 拖拽空白=平移 | 滚轮=缩放 | 拖节点=移动 | 点击节点=详情</div>
    </div>
  </el-dialog>
</template>

<style scoped>
.graph-container {
  position: relative;
  background: #0d1117;
  border-radius: 8px;
  overflow: hidden;
  min-height: 500px;
}
.graph-canvas {
  display: block;
  width: 100%;
  height: 70vh;
  cursor: grab;
}
.graph-canvas:active { cursor: grabbing; }

.node-detail {
  position: absolute;
  top: 12px;
  right: 12px;
  background: rgba(26,26,46,0.95);
  border: 1px solid rgba(255,255,255,0.12);
  border-radius: 8px;
  padding: 14px 18px;
  color: #e0e0e0;
  font-size: 13px;
  min-width: 180px;
  box-shadow: 0 4px 20px rgba(0,0,0,0.4);
}
.nd-header { display: flex; align-items: center; gap: 8px; font-weight: 600; font-size: 14px; margin-bottom: 8px; }
.nd-dot { width: 10px; height: 10px; border-radius: 50%; }
.nd-row { margin: 3px 0; font-size: 12px; color: #8a8a9a; }
.nd-row span { color: #c0c4cc; margin-right: 6px; }

.graph-toolbar {
  position: absolute;
  bottom: 12px;
  right: 12px;
  display: flex;
  gap: 6px;
}
.graph-hint {
  position: absolute;
  bottom: 12px;
  left: 12px;
  color: #484f58;
  font-size: 11px;
}
</style>
