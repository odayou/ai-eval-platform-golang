import { describe, it, expect } from 'vitest'

describe('router', () => {
  it('路由配置可加载', async () => {
    const mod = await import('../index')
    expect(mod.default).toBeDefined()
  })

  it('路由包含所有页面', async () => {
    const mod = await import('../index')
    const routes = mod.default.getRoutes()
    const paths = routes.map((r: any) => r.path)
    expect(paths).toContain('/')
    expect(paths).toContain('/create-agent')
    expect(paths).toContain('/resources')
    expect(paths).toContain('/questions')
    expect(paths).toContain('/standards')
    expect(paths).toContain('/dashboard')
    expect(paths).toContain('/cron')
    expect(paths).toContain('/clone')
    expect(paths).toContain('/terminal')
    expect(paths).toContain('/proxy')
    expect(paths).toContain('/regression')
    expect(paths).toContain('/leaderboard')
    expect(paths).toContain('/settings')
    expect(paths.length).toBe(19)
  })
})
