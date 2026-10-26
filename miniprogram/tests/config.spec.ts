import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  DEFAULT_BASE_URL,
  baseUrlOf,
  cachedSettings,
  cacheSettings,
  fetchPublicSettings,
  hasBaseUrl,
  pinnedCompanyOf,
} from '../utils/config'

// 小程序端「后台下发配置」的纯逻辑测试：
//   - 基址 / 锁定租户的归一化（去斜杠、容错非法值）
//   - Storage 缓存读写（冷启动首帧同步生效的关键）
// config.ts 只在函数内部使用 wx，模块顶层无副作用，故可直接导入；
// 这里按需给全局 wx 打桩即可覆盖缓存路径。

function stubWx(store: Record<string, any> = {}) {
  ;(globalThis as any).wx = {
    getStorageSync: (k: string) => store[k] ?? '',
    setStorageSync: (k: string, v: any) => {
      store[k] = v
    },
  }
}

describe('config', () => {
  beforeEach(() => {
    stubWx()
  })

  // 守门：引导种子基址**默认必须是空串**（源码里不放任何地址）。
  // 这条用例盯的是已经发生过的事故：默认值曾是某台开发机的
  // `http://192.168.31.224:8080`，随源码分发出去后，别人部署的小程序会拿它当
  // 首连地址——那是别人家的内网，请求必然超时；而种子基址是拉取后台配置的
  // **唯一入口**，超时后就再也拿不到正确基址（后台配 miniprogram_base_url 也救不回来），
  // 只能重新打包提审。同理，明文 http 在正式版也不被允许。
  describe('引导种子基址（守门）', () => {
    it('默认留空，不内置任何地址', () => {
      expect(DEFAULT_BASE_URL).toBe('')
    })

    it('若部署方已填写，只能是线上 https 域名或本机回环', () => {
      const seed = String(DEFAULT_BASE_URL || '')
      if (!seed) return
      // 内网 / 链路本地网段：只在自己那台机器所在局域网内可用
      expect(seed).not.toMatch(/\/\/(192\.168\.|10\.|172\.(1[6-9]|2\d|3[01])\.|169\.254\.)/)
      const isHttps = seed.indexOf('https://') === 0
      const isLoopback = /^http:\/\/(127\.0\.0\.1|localhost)(:\d+)?(\/|$)/.test(seed)
      expect(isHttps || isLoopback).toBe(true)
    })
  })

  describe('hasBaseUrl', () => {
    it('空串 / 纯空白 / null / undefined 一律视为未配置', () => {
      expect(hasBaseUrl('')).toBe(false)
      expect(hasBaseUrl('   ')).toBe(false)
      expect(hasBaseUrl(null)).toBe(false)
      expect(hasBaseUrl(undefined)).toBe(false)
    })

    it('有地址即视为已配置', () => {
      expect(hasBaseUrl('https://card.example.com')).toBe(true)
    })
  })

  describe('fetchPublicSettings', () => {
    it('基址为空时不发请求（否则拼出 /api/... 非法 URL，白等 5s 超时）', async () => {
      const request = vi.fn()
      ;(globalThis as any).wx = { request }
      await expect(fetchPublicSettings()).resolves.toBeNull()
      await expect(fetchPublicSettings('')).resolves.toBeNull()
      await expect(fetchPublicSettings('   ')).resolves.toBeNull()
      expect(request).not.toHaveBeenCalled()
    })

    it('基址可用时请求 /api/public/settings，末尾斜杠被归一（不拼双斜杠）', async () => {
      const request = vi.fn((o: any) =>
        o.success({ data: { code: 0, data: { brand_name: '甲公司', pinned_company_id: '3' } } })
      )
      ;(globalThis as any).wx = { request }

      const got = await fetchPublicSettings('https://card.example.com/')

      expect(request.mock.calls[0][0].url).toBe('https://card.example.com/api/public/settings')
      expect(got?.brand_name).toBe('甲公司')
      expect(pinnedCompanyOf(got)).toBe(3)
    })

    it('后端返回非 0 / 网络失败时按「没拿到」处理，不抛异常', async () => {
      ;(globalThis as any).wx = {
        request: vi.fn((o: any) => o.success({ data: { code: 1, msg: 'boom' } })),
      }
      await expect(fetchPublicSettings('https://card.example.com')).resolves.toBeNull()

      ;(globalThis as any).wx = { request: vi.fn((o: any) => o.fail(new Error('timeout'))) }
      await expect(fetchPublicSettings('https://card.example.com')).resolves.toBeNull()
    })
  })

  describe('baseUrlOf', () => {
    it('未拿到配置时返回空串（由调用方保持原值，不误覆盖）', () => {
      expect(baseUrlOf(null)).toBe('')
      expect(baseUrlOf({})).toBe('')
      expect(baseUrlOf({ miniprogram_base_url: '   ' })).toBe('')
    })

    it('去掉末尾斜杠与首尾空白，避免拼出双斜杠', () => {
      expect(baseUrlOf({ miniprogram_base_url: 'https://api.example.com///' })).toBe(
        'https://api.example.com'
      )
      expect(baseUrlOf({ miniprogram_base_url: '  https://api.example.com  ' })).toBe(
        'https://api.example.com'
      )
    })
  })

  describe('pinnedCompanyOf', () => {
    it('未配置 / 非法值一律回落 0（平台模式）', () => {
      expect(pinnedCompanyOf(null)).toBe(0)
      expect(pinnedCompanyOf({})).toBe(0)
      expect(pinnedCompanyOf({ pinned_company_id: 'abc' })).toBe(0)
      expect(pinnedCompanyOf({ pinned_company_id: '-1' })).toBe(0)
      expect(pinnedCompanyOf({ pinned_company_id: '0' })).toBe(0)
    })

    it('正整数解析为锁定租户', () => {
      expect(pinnedCompanyOf({ pinned_company_id: '7' })).toBe(7)
    })
  })

  describe('Storage 缓存', () => {
    it('空缓存 / 非法 JSON 返回 null，不抛异常', () => {
      expect(cachedSettings()).toBeNull()
      stubWx({ publicSettingsCache: '{ not json' })
      expect(cachedSettings()).toBeNull()
    })

    it('写入后可原样读回（下次冷启动首帧即用上次值）', () => {
      cacheSettings({ miniprogram_base_url: 'https://api.example.com', pinned_company_id: '5' })
      const got = cachedSettings()
      expect(got?.miniprogram_base_url).toBe('https://api.example.com')
      expect(pinnedCompanyOf(got)).toBe(5)
    })
  })
})
