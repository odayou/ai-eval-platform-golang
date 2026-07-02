import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

vi.mock('axios', () => {
  const m = { get: vi.fn().mockResolvedValue({ data: [] }), post: vi.fn().mockResolvedValue({ data: {} }), put: vi.fn().mockResolvedValue({ data: {} }), delete: vi.fn().mockResolvedValue({ data: {} }), interceptors: { request: { use: vi.fn(), eject: vi.fn() }, response: { use: vi.fn((cb: any) => cb({ data: {} })), eject: vi.fn() } }, create: vi.fn(() => m) }
  return { default: m }
})
vi.mock('element-plus', () => ({ ElMessage: { success: vi.fn(), warning: vi.fn(), error: vi.fn() }, ElMessageBox: { confirm: vi.fn().mockResolvedValue(true) } }))

describe('Tasks.vue', () => {
  beforeEach(() => { setActivePinia(createPinia()); vi.clearAllMocks() })

  it('组件可导入', async () => {
    const mod = await import('../../views/Tasks.vue')
    expect(mod.default).toBeDefined()
  })

  it('页面标题渲染', async () => {
    const { mount } = await import('@vue/test-utils')
    const Tasks = (await import('../../views/Tasks.vue')).default
    const wrapper = mount(Tasks, {
      global: {
        plugins: [createPinia()],
        stubs: { TaskList: { template: '<div>TaskList</div>' }, TaskDetail: { template: '<div>TaskDetail</div>' }, TaskCreateDialog: { template: '<div />' }, 'el-tabs': { template: '<div><slot /></div>' }, 'el-tab-pane': { template: '<div><slot /></div>' }, 'el-row': { template: '<div><slot /></div>' }, 'el-col': { template: '<div><slot /></div>' }, 'el-tag': { template: '<span><slot /></span>' }, 'el-badge': { template: '<span><slot /></span>' }, 'el-icon': { template: '<i><slot /></i>' }, 'el-button': { template: '<button><slot /></button>' }, 'el-dialog': { template: '<div v-if="modelValue"><slot /></div>', props: ['modelValue'] }, 'el-form': { template: '<div><slot /></div>' }, 'el-form-item': { template: '<div><slot /></div>' }, 'el-input': { template: '<input />' }, 'el-select': { template: '<div><slot /></div>' }, 'el-option': { template: '<div><slot /></div>' } },
      },
    })
    await wrapper.vm.$nextTick()
    expect(wrapper.find('h2').text()).toContain('评测任务')
  })

  it('健康状态标签在 API 响应后更新', async () => {
    const { mount } = await import('@vue/test-utils')
    const axios = (await import('axios')).default as any
    axios.get.mockImplementation((url: string) => {
      if (url === '/health') return Promise.resolve({ status: 'ok' })
      return Promise.resolve([])
    })
    const Tasks = (await import('../../views/Tasks.vue')).default
    const wrapper = mount(Tasks, {
      global: {
        plugins: [createPinia()],
        stubs: { TaskList: { template: '<div />' }, TaskDetail: { template: '<div />' }, TaskCreateDialog: { template: '<div />' }, 'el-tabs': { template: '<div><slot /></div>' }, 'el-tab-pane': { template: '<div><slot /></div>' }, 'el-row': { template: '<div><slot /></div>' }, 'el-col': { template: '<div><slot /></div>' }, 'el-tag': { template: '<span><slot /></span>' }, 'el-icon': { template: '<i><slot /></i>' }, 'el-button': { template: '<button><slot /></button>' } },
      },
    })
    await new Promise(r => setTimeout(r, 100))
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Backend')
  })
})
