// data-worker.ts — Web Worker 大计算量数据解压器(MRD 10.2.④)。
//
// MRD 原话:
//   "跑批测试完成后,后端会一口气甩给前端一个包含上百道题、多维指标的巨型矩阵数据包。
//    引入 Web Workers 架构:数据解析、帕累托散点计算、排行榜排序全在 Worker 线程
//    静默完成,再送回主线程由 ECharts 绘图。浏览器全程保持 60 帧流畅。"
//
// 消息协议:
//   Main → Worker: { type, payload }
//   Worker → Main: { type, id, result, error?, timing? }
//
// 支持的任务类型:
//   - parse_json:   解析巨型 JSON(>100KB 的任务报告)
//   - pareto:       计算帕累托边界(多目标优化:性能 vs 成本)
//   - leaderboard:  多维度排名排序
//   - matrix_diff:  矩阵对比计算(回归 Diff 需要的得分差)
//   - aggregate:    大规模数据聚合(多题多维度统计)

export interface WorkerMessage {
  type: string
  id: string
  payload: any
}

export interface WorkerResponse {
  type: string
  id: string
  result: any
  error?: string
  timing?: { start: number; end: number; duration: number }
}

// ── 消息路由 ──

self.onmessage = (e: MessageEvent<WorkerMessage>) => {
  const { type, id, payload } = e.data
  const start = performance.now()
  let result: any
  let error: string | undefined

  try {
    switch (type) {
      case 'parse_json':
        result = handleParseJSON(payload)
        break
      case 'pareto':
        result = handlePareto(payload)
        break
      case 'leaderboard':
        result = handleLeaderboard(payload)
        break
      case 'matrix_diff':
        result = handleMatrixDiff(payload)
        break
      case 'aggregate':
        result = handleAggregate(payload)
        break
      default:
        error = `Unknown task type: ${type}`
    }
  } catch (err: any) {
    error = err.message || String(err)
  }

  const end = performance.now()
  const response: WorkerResponse = {
    type, id, result, error,
    timing: { start, end, duration: Math.round(end - start) },
  }
  self.postMessage(response)
}

// ── 处理函数 ──

// handleParseJSON 解析大型 JSON 数据(在 Worker 中避免主线程卡顿)
function handleParseJSON(payload: { text: string }): any {
  return JSON.parse(payload.text)
}

// handlePareto 计算帕累托边界(性能 vs 成本)
// 输入: { points: Array<{ name: string, performance: number, cost: number }> }
// 输出: Pareto 前沿面上的点 + 被支配的点 + 排序
function handlePareto(payload: { points: Array<{ name: string; performance: number; cost: number }> }) {
  const { points } = payload
  if (!points?.length) return { frontier: [], dominated: [], sorted: [] }

  // 基于成本和性能排序
  const sorted = [...points].sort((a, b) => {
    // 性价比优先: 性能/成本
    const roiA = a.cost > 0 ? a.performance / a.cost : 0
    const roiB = b.cost > 0 ? b.performance / b.cost : 0
    return roiB - roiA
  })

  // 计算帕累托前沿(非支配解)
  const frontier = findParetoFrontier(points)
  const frontierNames = new Set(frontier.map((p) => p.name))
  const dominated = points.filter((p) => !frontierNames.has(p.name))

  return { frontier, dominated, sorted }
}

// findParetoFrontier 找出非支配解(在多目标优化中,一个点不被任何其他点支配)
// 支配定义: A 支配 B 当且仅当 A.performance >= B.performance && A.cost <= B.cost
//          且至少有一个严格不等
function findParetoFrontier(points: Array<{ name: string; performance: number; cost: number }>) {
  const frontier: typeof points = []

  for (const a of points) {
    let dominated = false
    for (const b of points) {
      if (a === b) continue
      // b 支配 a?
      if (b.performance >= a.performance && b.cost <= a.cost &&
          (b.performance > a.performance || b.cost < a.cost)) {
        dominated = true
        break
      }
    }
    if (!dominated) {
      frontier.push(a)
    }
  }
  return frontier
}

// handleLeaderboard 多维度排行榜排序
// 输入: { entries: Array<{ name, scores: Record<string, number> }>, dimensions: string[], weights?: Record<string, number> }
function handleLeaderboard(payload: {
  entries: Array<{ name: string; scores: Record<string, number> }>
  dimensions: string[]
  weights?: Record<string, number>
}) {
  const { entries, dimensions, weights } = payload
  if (!entries?.length) return []

  // 加权计算总分
  const scored = entries.map((entry) => {
    let total = 0
    let totalWeight = 0
    for (const dim of dimensions) {
      const score = entry.scores[dim] ?? 0
      const w = weights?.[dim] ?? 1.0
      total += score * w
      totalWeight += w
    }
    const overall = totalWeight > 0 ? total / totalWeight : 0
    return { name: entry.name, overall, scores: entry.scores, rank: 0 }
  })

  // 排序
  scored.sort((a, b) => b.overall - a.overall)

  // 赋排名
  scored.forEach((item, i) => {
    item.rank = i + 1
  })

  return scored
}

// handleMatrixDiff 矩阵对比(回归 Diff 的核心计算)
// 输入: { base: number[][], target: number[][], labels: string[] }
function handleMatrixDiff(payload: {
  base: number[][]
  target: number[][]
  labels: string[]
}) {
  const { base, target, labels } = payload
  if (!base?.length) return { diffs: [], summary: { improved: 0, regressed: 0, unchanged: 0 } }

  const diffs: Array<{ label: string; baseScore: number; targetScore: number; delta: number; status: string }> = []
  let improved = 0
  let regressed = 0
  let unchanged = 0

  for (let i = 0; i < base.length; i++) {
    const baseScore = base[i]?.[0] ?? 0
    const targetScore = target[i]?.[0] ?? 0
    const delta = targetScore - baseScore
    let status = 'unchanged'
    if (delta > 1) {
      status = 'improved'
      improved++
    } else if (delta < -1) {
      status = 'regressed'
      regressed++
    } else {
      unchanged++
    }
    diffs.push({
      label: labels[i] || `#${i + 1}`,
      baseScore: Math.round(baseScore * 10) / 10,
      targetScore: Math.round(targetScore * 10) / 10,
      delta: Math.round(delta * 10) / 10,
      status,
    })
  }

  return { diffs, summary: { improved, regressed, unchanged } }
}

// handleAggregate 大规模数据聚合(从逐题数据聚合到维度汇总)
// 输入: { questions: Array<{ dimensionScores: Record<string, number>; isBadCase: boolean }> }
function handleAggregate(payload: {
  questions: Array<{ dimensionScores: Record<string, number>; isBadCase: boolean }>
}) {
  const { questions } = payload
  if (!questions?.length) return { dimensions: {}, overall: 0, badCases: 0, totalQuestions: 0 }

  const dimScores: Record<string, number[]> = {}
  let overallSum = 0
  let badCases = 0

  for (const q of questions) {
    if (q.isBadCase) badCases++
    for (const [dim, score] of Object.entries(q.dimensionScores)) {
      if (!dimScores[dim]) dimScores[dim] = []
      dimScores[dim].push(score)
    }
    // 该题综合分: 各维度平均
    const dimVals = Object.values(q.dimensionScores)
    if (dimVals.length > 0) {
      overallSum += dimVals.reduce((a, b) => a + b, 0) / dimVals.length
    }
  }

  // 维度聚合(取平均)
  const dimensions: Record<string, number> = {}
  for (const [dim, scores] of Object.entries(dimScores)) {
    dimensions[dim] = Math.round((scores.reduce((a, b) => a + b, 0) / scores.length) * 10) / 10
  }

  const overall = questions.length > 0
    ? Math.round((overallSum / questions.length) * 10) / 10
    : 0

  return { dimensions, overall, badCases, totalQuestions: questions.length }
}
