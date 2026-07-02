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
      response: { use: vi.fn((cb: any) => cb({ data: [] })), eject: vi.fn() },
    },
    create: vi.fn(() => mockAxios),
  }
  return { default: mockAxios }
})

describe('dictStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('store 可创建', async () => {
    const { useDictStore } = await import('../dictStore')
    const store = useDictStore()
    expect(store).toBeDefined()
  })

  it('ensure/refresh 方法是函数', async () => {
    const { useDictStore } = await import('../dictStore')
    const store = useDictStore()
    expect(typeof store.ensureAgents).toBe('function')
    expect(typeof store.ensureRuleSets).toBe('function')
    expect(typeof store.ensureQuestions).toBe('function')
    expect(typeof store.refreshAgents).toBe('function')
    expect(typeof store.refreshRuleSets).toBe('function')
  })
})
