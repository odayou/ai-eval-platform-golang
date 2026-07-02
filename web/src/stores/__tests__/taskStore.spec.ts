import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'

vi.mock('axios', () => {
  const mockAxios = {
    get: vi.fn().mockResolvedValue({ data: [] }),
    post: vi.fn().mockResolvedValue({ data: {} }),
    put: vi.fn().mockResolvedValue({ data: {} }),
    delete: vi.fn().mockResolvedValue({ data: {} }),
    interceptors: {
      request: { use: vi.fn(), eject: vi.fn() },
      response: { use: vi.fn((cb: any) => cb({ data: {} })), eject: vi.fn() },
    },
    create: vi.fn(() => mockAxios),
  }
  return { default: mockAxios }
})

describe('taskStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('store 可创建', async () => {
    const { useTaskStore } = await import('../taskStore')
    const store = useTaskStore()
    expect(store).toBeDefined()
  })

  it('loadTasks/createTask/deleteTask/selectTask 是函数', async () => {
    const { useTaskStore } = await import('../taskStore')
    const store = useTaskStore()
    expect(typeof store.loadTasks).toBe('function')
    expect(typeof store.createTask).toBe('function')
    expect(typeof store.deleteTask).toBe('function')
    expect(typeof store.selectTask).toBe('function')
  })

  it('selectTask 更新 selectedId', async () => {
    const { useTaskStore } = await import('../taskStore')
    const store = useTaskStore()
    store.selectTask('test-id')
    expect(store.$state.selectedId).toBe('test-id')
  })
})
