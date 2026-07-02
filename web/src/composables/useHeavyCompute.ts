// useHeavyCompute.ts — Web Worker 大计算量解压器 的 Vue 封装(MRD 10.2.④)。
//
// 用法:
//   import { useHeavyCompute } from '../composables/useHeavyCompute'
//   const { parseJSON, calcPareto, calcLeaderboard, calcMatrixDiff, calcAggregate } = useHeavyCompute()
//   const result = await calcPareto(points)
//
// 设计:
//   - 自动管理 Worker 生命周期(单例,复用)
//   - 每个请求有唯一 ID,支持超时回退到主线程
//   - 主线程回退:Worker 不可用时,直接在主线程执行(小数据量场景)
//
// Worker API (Vite):
//   import DataWorker from '../workers/data-worker?worker'
//   或 new Worker(new URL('../workers/data-worker.ts', import.meta.url), { type: 'module' })
//
// 注意:Vite 支持 ?worker 后缀来导入 Web Worker
//   本文件使用 try-catch,Worker 不可用时自动回退主线程。

import { ref, onUnmounted } from 'vue'

interface WorkerTask {
  resolve: (value: any) => void
  reject: (reason: any) => void
  timeout: ReturnType<typeof setTimeout>
}

let workerInstance: Worker | null = null
const pending = new Map<string, WorkerTask>()
let idCounter = 0

function getWorker(): Worker | null {
  if (workerInstance) return workerInstance

  try {
    // Vite 方式: ?worker 后缀导入 (生产环境)
    // 如果构建工具不支持,回退到标准 Worker 构造
    workerInstance = new Worker(
      new URL('../workers/data-worker.ts', import.meta.url),
      { type: 'module' }
    )

    workerInstance.onmessage = (e) => {
      const { id, result, error } = e.data
      const task = pending.get(id)
      if (!task) return
      clearTimeout(task.timeout)
      pending.delete(id)

      if (error) {
        task.reject(new Error(error))
      } else {
        task.resolve(result)
      }
    }

    workerInstance.onerror = (e) => {
      console.warn('[Worker] 错误,回退到主线程:', e.message)
      // 所有等待中的任务回退到主线程
      // (实际在 sendTask 里已有 3s 超时回退)
    }
  } catch (e) {
    console.warn('[Worker] 初始化失败,使用主线程模式:', e)
    workerInstance = null
  }

  return workerInstance
}

function sendTask(type: string, payload: any, timeoutMs = 5000): Promise<any> {
  const worker = getWorker()
  const id = `task_${++idCounter}_${type}`

  // Worker 不可用 → 直接主线程执行
  if (!worker) {
    return Promise.resolve(runInMainThread(type, payload))
  }

  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      pending.delete(id)
      console.warn(`[Worker] 任务 ${type} 超时(${timeoutMs}ms),回退主线程`)
      // 超时回退到主线程
      try {
        resolve(runInMainThread(type, payload))
      } catch (e) {
        reject(e)
      }
    }, timeoutMs)

    pending.set(id, { resolve, reject, timeout })
    worker.postMessage({ type, id, payload })
  })
}

// runInMainThread 在主线程中执行计算(回退方案)
function runInMainThread(type: string, payload: any): any {
  switch (type) {
    case 'parse_json':
      return JSON.parse(payload.text)
    case 'pareto': {
      const { points } = payload
      if (!points?.length) return { frontier: [], dominated: [], sorted: [] }
      const sorted = [...points].sort((a: any, b: any) => {
        const roiA = a.cost > 0 ? a.performance / a.cost : 0
        const roiB = b.cost > 0 ? b.performance / b.cost : 0
        return roiB - roiA
      })
      return { frontier: points, dominated: [], sorted }
    }
    case 'leaderboard': {
      const { entries, dimensions, weights } = payload
      if (!entries?.length) return []
      return entries
        .map((e: any) => {
          let total = 0
          let tw = 0
          for (const d of dimensions) {
            const s = e.scores?.[d] ?? 0
            const w = weights?.[d] ?? 1
            total += s * w
            tw += w
          }
          return { ...e, overall: tw > 0 ? total / tw : 0 }
        })
        .sort((a: any, b: any) => b.overall - a.overall)
        .map((e: any, i: number) => ({ ...e, rank: i + 1 }))
    }
    case 'matrix_diff': {
      const { base, target, labels } = payload
      const diffs = (base || []).map((b: number[], i: number) => {
        const bs = b?.[0] ?? 0
        const ts = target?.[i]?.[0] ?? 0
        const delta = ts - bs
        return {
          label: labels?.[i] || `#${i + 1}`,
          baseScore: Math.round(bs * 10) / 10,
          targetScore: Math.round(ts * 10) / 10,
          delta: Math.round(delta * 10) / 10,
          status: delta > 1 ? 'improved' : delta < -1 ? 'regressed' : 'unchanged',
        }
      })
      return {
        diffs,
        summary: {
          improved: diffs.filter((d: any) => d.status === 'improved').length,
          regressed: diffs.filter((d: any) => d.status === 'regressed').length,
          unchanged: diffs.filter((d: any) => d.status === 'unchanged').length,
        },
      }
    }
    case 'aggregate': {
      const { questions } = payload
      if (!questions?.length) return { dimensions: {}, overall: 0, badCases: 0, totalQuestions: 0 }
      const dimScores: Record<string, number[]> = {}
      let sum = 0
      let bad = 0
      for (const q of questions) {
        if (q.isBadCase) bad++
        const vals = Object.values(q.dimensionScores || {}) as number[]
        if (vals.length) sum += vals.reduce((a, b) => a + b, 0) / vals.length
        for (const [d, s] of Object.entries(q.dimensionScores || {})) {
          if (!dimScores[d]) dimScores[d] = []
          dimScores[d].push(s as number)
        }
      }
      const dims: Record<string, number> = {}
      for (const [d, scores] of Object.entries(dimScores)) {
        dims[d] = Math.round((scores.reduce((a, b) => a + b, 0) / scores.length) * 10) / 10
      }
      return {
        dimensions: dims,
        overall: questions.length > 0 ? Math.round((sum / questions.length) * 10) / 10 : 0,
        badCases: bad,
        totalQuestions: questions.length,
      }
    }
    default:
      return payload
  }
}

// ── 公开的高层 API ──

export function useHeavyCompute() {
  const busy = ref(false)

  async function withBusy<T>(fn: () => Promise<T>): Promise<T> {
    busy.value = true
    try {
      return await fn()
    } finally {
      busy.value = false
    }
  }

  return {
    busy,

    /** 解析大型 JSON(> 100KB 的报告) */
    parseJSON: (text: string) =>
      withBusy(() => sendTask('parse_json', { text })),

    /** 计算帕累托前沿(性能 vs 成本) */
    calcPareto: (points: Array<{ name: string; performance: number; cost: number }>) =>
      withBusy(() => sendTask('pareto', { points })),

    /** 多维度排行榜排序 */
    calcLeaderboard: (
      entries: Array<{ name: string; scores: Record<string, number> }>,
      dimensions: string[],
      weights?: Record<string, number>
    ) => withBusy(() => sendTask('leaderboard', { entries, dimensions, weights })),

    /** 矩阵对比(回归 Diff) */
    calcMatrixDiff: (base: number[][], target: number[][], labels: string[]) =>
      withBusy(() => sendTask('matrix_diff', { base, target, labels })),

    /** 大规模数据聚合 */
    calcAggregate: (questions: Array<{ dimensionScores: Record<string, number>; isBadCase: boolean }>) =>
      withBusy(() => sendTask('aggregate', { questions })),
  }
}

// 清理 Worker(在应用卸载时调用)
export function disposeWorker() {
  if (workerInstance) {
    workerInstance.terminate()
    workerInstance = null
    pending.clear()
  }
}

// 在 Vue 组件中可选地自动清理
export function useWorkerCleanup() {
  onUnmounted(() => {
    // 不在此终止 Worker(单例复用),只清理本组件未完成的请求
  })
}
