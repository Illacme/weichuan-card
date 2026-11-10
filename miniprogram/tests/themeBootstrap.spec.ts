import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { prefetchTheme } from '../utils/themeBootstrap'

// 冷启动主题预热：验证「任意入口页都能拿到本租户模板」，以及
// 不复用 request.ts（模块加载期 getApp() 的时序坑）这两条硬约束。
const mocks = vi.hoisted(() => ({ applyTheme: vi.fn() }))
vi.mock('../utils/theme', () => ({ applyTheme: mocks.applyTheme }))

const HERE = dirname(fileURLToPath(import.meta.url))
const MP = resolve(HERE, '..')

let request: ReturnType<typeof vi.fn>

beforeEach(() => {
  request = vi.fn()
  vi.stubGlobal('wx', { request })
  mocks.applyTheme.mockClear()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('utils/themeBootstrap 冷启动预热', () => {
  it('拉公开配置成功后应用模板，且带上当前租户', () => {
    prefetchTheme('https://card.example', 7)

    expect(request).toHaveBeenCalledTimes(1)
    const opt = request.mock.calls[0][0]
    expect(opt.url).toBe('https://card.example/api/home-layout?companyId=7')
    expect(opt.method).toBe('GET')
    opt.success({ data: { code: 0, templateId: 'teal' } })
    expect(mocks.applyTheme).toHaveBeenCalledWith('teal')
  })

  it('平台模式无租户绑定时不带 companyId；基址末尾斜杠不会拼出 //api', () => {
    prefetchTheme('https://card.example///', 0)

    const opt = request.mock.calls[0][0]
    expect(opt.url).toBe('https://card.example/api/home-layout')
    expect(opt.url).not.toContain('//api')
  })

  it('基址为空不发请求：非法 URL 会白等一个超时，白屏更糟', () => {
    prefetchTheme('   ', 0)
    expect(request).not.toHaveBeenCalled()
    expect(mocks.applyTheme).not.toHaveBeenCalled()
  })

  it('业务码非 0 / 断网 / 旧后端都静默保留缓存，不抛错不误设主题', () => {
    prefetchTheme('https://card.example', 1)
    const ok = request.mock.calls[0][0]
    ok.success({ data: { code: 1, msg: 'fail' } })
    expect(mocks.applyTheme).not.toHaveBeenCalled()

    ok.success({ data: { code: 0 } }) // 老后端不下发 templateId
    expect(mocks.applyTheme).toHaveBeenCalledWith(undefined)

    mocks.applyTheme.mockClear()
    prefetchTheme('https://card.example', 1)
    const bad = request.mock.calls[1][0]
    expect(() => bad.fail(new Error('offline'))).not.toThrow()
    expect(mocks.applyTheme).not.toHaveBeenCalled()
  })
})

describe('冷启动预热的源码级守门', () => {
  it('预热门绝不 import request.ts（它在模块加载期取 getApp）', () => {
    const src = readFileSync(resolve(MP, 'utils/themeBootstrap.ts'), 'utf-8')
    expect(src).not.toMatch(/from\s+['"][^'"]*request['"]/)
    expect(src).toContain("from './theme'")
    expect(src).toContain('wx.request(')
  })

  it('app.ts 在 onLaunch 内预热：租户绑定之后、空基址守卫之下', () => {
    const src = readFileSync(resolve(MP, 'app.ts'), 'utf-8')
    expect(src).toContain('prefetchTheme(globalData.baseUrl, getCompanyId())')
    expect(src).toContain('if (hasBaseUrl(globalData.baseUrl))')
    const launch = src.indexOf('onLaunch(')
    const bind = src.indexOf('bindCompanyFromScene(')
    const prefetch = src.indexOf('prefetchTheme(globalData.baseUrl')
    expect(launch).toBeGreaterThan(-1)
    expect(bind).toBeGreaterThan(launch)
    expect(prefetch).toBeGreaterThan(bind, '租户绑定之前预热会按缺省租户取错模板')
  })
})
