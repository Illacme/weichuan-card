// 第六十九轮：出参侧按租户选 origin 之后，端上契约必须仍然成立。
//
// 公开 GET 现在可能返回两种形态：
//   相对 /uploads/...                 —— 平台统一入口（没配 / 开关 off / 平台对象）
//   绝对 https://img.cust.com/...    —— 客户自有域名（归属该企业的新形态对象键）
//
// assetUrl 必须对后者原样返回。否则会被拼成
//   https://api.test/https://img.cust.com/uploads/...
// 而且是「全站图裂、接口全 200」这种最难查的失败：后端每个接口都对，
// 后台也能预览，只有访客手机上的 <image> 加载不出来。
//
// 这里刻意 import **真实的** utils/request.ts，不用页面测试里那个 mock：
// mock 里写的是「我以为 assetUrl 会怎么做」，改坏真实实现它照样全绿。
import { beforeEach, describe, expect, it, vi } from 'vitest'

describe('端上 assetUrl 契约（客户自有域名出参）', () => {
  beforeEach(() => {
    vi.resetModules()
    // request.ts 模块级会取 getApp()，tenant.ts 也会读 storage；给最小运行时桩。
    vi.stubGlobal('wx', {
      getStorageSync: () => '',
      setStorageSync: () => {},
      removeStorageSync: () => {},
      getAccountInfoSync: () => ({ miniProgram: { envVersion: 'release' } }),
    })
    vi.stubGlobal('getApp', () => ({ globalData: { baseUrl: 'https://api.test' } }))
  })

  async function realAssetUrl() {
    const mod = await import('../utils/request')
    return mod.assetUrl
  }

  it('量尺自己有读数：相对路径确实被拼上了 baseUrl', async () => {
    const assetUrl = await realAssetUrl()
    const got = assetUrl('/uploads/t/5/logo.png')
    expect(got).toBe('https://api.test/uploads/t/5/logo.png')
    // 若 baseUrl 桩没生效（回落空串），上面会等于原值，这条兜底防止空对空全绿
    expect(got).not.toBe('/uploads/t/5/logo.png')
  })

  it('客户自有域名的绝对 URL 原样返回，不被二次拼接', async () => {
    const assetUrl = await realAssetUrl()
    const url = 'https://img.cust.com/uploads/t/5/logo.png'
    expect(assetUrl(url)).toBe(url)
    expect(assetUrl(url)).not.toContain('api.test')
  })

  it('http 明文客户域名也原样返回（自建桶常见）', async () => {
    const assetUrl = await realAssetUrl()
    const url = 'http://img.cust.com:9000/uploads/t/5/logo.png'
    expect(assetUrl(url)).toBe(url)
  })

  it('平台对象 / 历史裸键仍走平台入口（后端刻意不换域名）', async () => {
    const assetUrl = await realAssetUrl()
    expect(assetUrl('/uploads/p/library/logo.png')).toBe('https://api.test/uploads/p/library/logo.png')
    expect(assetUrl('/uploads/old-logo.png')).toBe('https://api.test/uploads/old-logo.png')
  })

  it('空值返回空串，不是 baseUrl 本身', async () => {
    const assetUrl = await realAssetUrl()
    expect(assetUrl('')).toBe('')
    expect(assetUrl(null)).toBe('')
    expect(assetUrl(undefined)).toBe('')
  })
})
