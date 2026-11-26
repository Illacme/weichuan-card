// 0067 展示配置版本号自动刷新。
//
// 这里只测纯逻辑（探针返回 → 是否触发刷新），用请求 mock 驱动不同响应：
//   - 版本变化才刷新，没变不刷新
//   - 首次读到版本只记下不刷新（首屏本来就是最新的，别浪费一次请求）
//   - 旧后端不下发版本 / 探针失败：静默不刷新，绝不影响名片页
//   - 按租户分键：A 家企业保存配置不该让 B 家企业也去刷新
//
// 源码级守门钉住「探针 no-store」做不到（那是后端），由后端测试负责；
// 这里钉住端上必须真的在 onShow 调 probe、变化时必须 silent 重拉。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { installWx } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({ code: 0, configVersion: 1 })),
  companyId: 1,
}))

vi.mock('../utils/request', () => ({ request: mocks.request }))
vi.mock('../utils/tenant', () => ({ getCompanyId: () => mocks.companyId }))

let storage: Map<string, any>
const INDEX_TS = resolve(dirname(fileURLToPath(import.meta.url)), '../pages/index/index.ts')

beforeEach(() => {
  vi.resetModules()
  storage = installWx().storage
  mocks.companyId = 1
  mocks.request.mockReset()
  mocks.request.mockResolvedValue({ code: 0, configVersion: 1 })
})

async function load() {
  return await import('../utils/configVersion')
}

describe('configVersionChanged', () => {
  it('首次读到版本：记录但不刷新（首屏已是最新拉取）', async () => {
    const { configVersionChanged } = await load()
    expect(await configVersionChanged()).toBe(false)
    expect(storage.get('homeLayoutConfigVersion:1')).toBe(1)
  })

  it('版本变化：返回 true 触发刷新，并更新本地记录', async () => {
    const { configVersionChanged } = await load()
    await configVersionChanged() // 首次 1，只记录
    mocks.request.mockResolvedValue({ code: 0, configVersion: 2 })
    expect(await configVersionChanged()).toBe(true)
    expect(storage.get('homeLayoutConfigVersion:1')).toBe(2)
  })

  it('版本没变：返回 false，不重复刷新', async () => {
    const { configVersionChanged } = await load()
    await configVersionChanged()
    expect(await configVersionChanged()).toBe(false)
    expect(await configVersionChanged()).toBe(false)
  })

  it('旧后端不下发 configVersion：静默当没变', async () => {
    mocks.request.mockResolvedValue({ code: 0 })
    const { configVersionChanged } = await load()
    expect(await configVersionChanged()).toBe(false)
  })

  it('探针失败（请求 reject）：静默不刷新，不抛异常', async () => {
    mocks.request.mockRejectedValue(new Error('network'))
    const { configVersionChanged } = await load()
    expect(await configVersionChanged()).toBe(false)
  })

  it('按租户分键：A 家记录的版本不影响 B 家', async () => {
    const { configVersionChanged } = await load()
    mocks.companyId = 1
    await configVersionChanged() // A 记录 1
    mocks.companyId = 2
    expect(await configVersionChanged()).toBe(false) // B 首次，只记录
    mocks.request.mockResolvedValue({ code: 0, configVersion: 2 })
    expect(await configVersionChanged()).toBe(true) // B 独立变 2
    expect(storage.get('homeLayoutConfigVersion:1')).toBe(1)
    expect(storage.get('homeLayoutConfigVersion:2')).toBe(2)
  })
})

describe('首页接入守门（源码级）', () => {
  it('onShow 必须调用版本探针，且变化时静默重拉（silent）', () => {
    const src = readFileSync(INDEX_TS, 'utf8')
    expect(src).toMatch(/import\s*\{[^}]*configVersionChanged[^}]*\}\s*from\s*'[^']*configVersion'/)
    // onShow 里调用探针
    const onShow = src.slice(src.indexOf('onShow()'), src.indexOf('onShow()') + 600)
    expect(onShow).toMatch(/configVersionChanged\(\)/)
    // 变化后 silent 重拉，不闪加载态
    expect(onShow).toMatch(/loadAll\(\{\s*silent:\s*true\s*\}\)/)
  })

  it('探针路径必须是独立的 /api/home-layout/version（不能塞进被缓存的 /api/home-layout）', () => {
    const src = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), '../utils/configVersion.ts'), 'utf8')
    expect(src).toMatch(/'\/api\/home-layout\/version'/)
  })
})
