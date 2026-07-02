<script setup lang="ts">
/**
 * BaseChart.vue — ECharts 的通用封装组件。
 *
 * 设计目标:
 *   - 所有图表页统一用这个组件,避免每个页面重复写 init/dispose/resize 逻辑
 *   - 父组件只传 option,ECharts 实例的生命周期由本组件管理
 *   - 响应式:option 变化自动 setOption;窗口 resize 自动重绘
 *
 * 用法:
 *   <BaseChart :option="radarOption" height="320px" />
 */
import { ref, watch, onMounted, onBeforeUnmount, shallowRef } from 'vue'
import * as echarts from 'echarts'

const props = withDefaults(defineProps<{
  option: echarts.EChartsOption   // ECharts 配置对象
  height?: string                 // 图表容器高度,默认 300px
}>(), {
  height: '300px',
})

const el = ref<HTMLDivElement>()           // DOM 容器
// shallowRef 避免 ECharts 实例被 Vue 深度代理(实例是复杂对象,深度响应无意义且耗性能)
const chart = shallowRef<echarts.ECharts>()

// onMounted 时初始化图表实例并绑定 resize 监听
onMounted(() => {
  if (!el.value) return
  chart.value = echarts.init(el.value)
  chart.value.setOption(props.option)
  window.addEventListener('resize', handleResize)
})

// option 变化时增量更新(merge 模式,不重置整个图表)
watch(() => props.option, (opt) => {
  chart.value?.setOption(opt, true)
}, { deep: true })

// 窗口缩放时让图表自适应容器宽度
function handleResize() {
  chart.value?.resize()
}

// 组件销毁前释放 ECharts 实例 + 移除监听,防止内存泄漏
onBeforeUnmount(() => {
  window.removeEventListener('resize', handleResize)
  chart.value?.dispose()
})
</script>

<template>
  <div ref="el" class="base-chart" :style="{ height }"></div>
</template>

<style scoped>
.base-chart {
  width: 100%;
}
</style>
