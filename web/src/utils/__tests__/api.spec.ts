import { describe, it, expect, vi } from 'vitest'

vi.mock('axios', () => {
  const mockAxiosInstance = {
    get: vi.fn().mockResolvedValue({ data: { test: 'ok' } }),
    post: vi.fn().mockResolvedValue({ data: {} }),
    put: vi.fn().mockResolvedValue({ data: {} }),
    delete: vi.fn().mockResolvedValue({ data: {} }),
    interceptors: {
      request: { use: vi.fn(), eject: vi.fn() },
      response: { use: vi.fn((cb: any) => cb({ data: 'ok' })), eject: vi.fn() },
    },
  }
  return {
    default: {
      create: vi.fn(() => mockAxiosInstance),
    },
  }
})

describe('api utility', () => {
  it('API 模块可导入', async () => {
    const api = await import('../api')
    expect(api.default).toBeDefined()
    expect(typeof api.default.get).toBe('function')
    expect(typeof api.default.post).toBe('function')
    expect(typeof api.default.put).toBe('function')
    expect(typeof api.default.delete).toBe('function')
  })
})
