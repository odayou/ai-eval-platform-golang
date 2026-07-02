import { describe, it, expect } from 'vitest'

describe('BaseChart.vue', () => {
  it('组件文件存在且可导入', async () => {
    const mod = await import('../BaseChart.vue')
    expect(mod.default).toBeDefined()
  })

  it('组件定义包含 props 选项', async () => {
    const mod = await import('../BaseChart.vue')
    const component = mod.default
    expect(component).toBeDefined()
    expect(component.props).toBeDefined()
  })

  it('组件接收 option 和 height props', async () => {
    const mod = await import('../BaseChart.vue')
    const props = mod.default.props || {}
    const propNames = Object.keys(props)
    expect(propNames).toContain('option')
    expect(propNames).toContain('height')
  })
})
