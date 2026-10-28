import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  DEFAULT_BASE_URL,
  DEV_BASE_URL,
  baseUrlOf,
  baseUrlOverride,
  cachedSettings,
  cacheSettings,
  fetchPublicSettings,
  hasBaseUrl,
  pinnedCompanyOf,
  resolveBaseUrl,
  wxEnvVersion,
} from '../utils/config'

// 小程序端「后台下发配置」的纯逻辑测试：
//   - 基址 / 锁定租户的归一化（去斜杠、容错非法值）
//   - Storage 缓存读写（冷启动首帧同步生效的关键）
// config.ts 只在函数内部使用 wx，模块顶层无副作用，故可直接导入；
// 这里按需给全局 wx 打桩即可覆盖缓存路径。

function stubWx(store: Record<string, any> = {}, accountInfo?: any) {
  ;(globalThis as any).wx = {
    getStorageSync: (k: string) => store[k] ?? '',
    setStorageSync: (k: string, v: any) => {
      store[k] = v
    },
    getAccountInfoSync: () => accountInfo ?? { miniProgram: { envVersion: 'release' } },
  }
}

describe('config', () => {
  beforeEach(() => {
    stubWx()
  })

  // 守门：引导种子基址**不得是内网地址 / 明文 http / 占位域名**。
  // 背景：种子基址是首次冷启动拉取后台配置（/api/public/settings）的唯一入口，
  // 超时后后台怎么配 miniprogram_base_url 都救不回来。本项目已内置生产 https 域名
  // weichuan.ziyuanxingqiu.cn；本地联调可临时置空或填本机回环，但**提交前必须**是
  // 线上 https 公网域名（preflight 第 3 节同判）。
  describe('引导种子基址（守门）', () => {
    it('为空（本机联调）或为线上 https 公网域名 / 本机回环', () => {
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
    it('基址仅空白时不发请求（否则拼出 /api/... 非法 URL，白等 5s 超时）', async () => {
      const request = vi.fn()
      ;(globalThis as any).wx = { request }
      await expect(fetchPublicSettings('   ')).resolves.toBeNull()
      expect(request).not.toHaveBeenCalled()
    })

    it('不传参 / 空串回退到 DEFAULT_BASE_URL（生产种子）并发起请求', async () => {
      const request = vi.fn((o: any) =>
        o.success({ data: { code: 0, data: { brand_name: '种子', pinned_company_id: '0' } } })
      )
      ;(globalThis as any).wx = { request }

      const got = await fetchPublicSettings()

      expect(request.mock.calls[0][0].url).toBe('https://weichuan.ziyuanxingqiu.cn/api/public/settings')
      expect(got?.brand_name).toBe('种子')
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

  // 运行环境识别：开发者工具（develop）→ 测试地址；体验版 / 正式版（trial/release）→ 生产种子。
  // 守门点：非微信环境（单测）必须回落 release，绝不可误走测试地址把生产请求打歪。
  describe('运行环境识别与基址解析', () => {
    it('getAccountInfoSync 返回 develop/trial/release 时正确识别', () => {
      ;(globalThis as any).wx = { getAccountInfoSync: () => ({ miniProgram: { envVersion: 'develop' } }) }
      expect(wxEnvVersion()).toBe('develop')
      ;(globalThis as any).wx = { getAccountInfoSync: () => ({ miniProgram: { envVersion: 'trial' } }) }
      expect(wxEnvVersion()).toBe('trial')
      ;(globalThis as any).wx = { getAccountInfoSync: () => ({ miniProgram: { envVersion: 'release' } }) }
      expect(wxEnvVersion()).toBe('release')
    })

    it('getAccountInfoSync 抛错或返回未知值时回落 release（按正式处理）', () => {
      ;(globalThis as any).wx = { getAccountInfoSync: () => { throw new Error('not wx') } }
      expect(wxEnvVersion()).toBe('release')
      ;(globalThis as any).wx = { getAccountInfoSync: () => ({ miniProgram: { envVersion: 'unknown' } }) }
      expect(wxEnvVersion()).toBe('release')
      ;(globalThis as any).wx = { getAccountInfoSync: () => ({}) }
      expect(wxEnvVersion()).toBe('release')
    })

    it('baseUrlOverride 读取 Storage 的手动覆盖（已 trim），无则返回空串', () => {
      stubWx({ mpBaseUrlOverride: '  https://override.example.com  ' })
      expect(baseUrlOverride()).toBe('https://override.example.com')
      stubWx({})
      expect(baseUrlOverride()).toBe('')
    })

    it('resolveBaseUrl：手动覆盖优先级最高（develop 下也生效）', () => {
      stubWx({ mpBaseUrlOverride: 'https://manual.example.com' }, { miniProgram: { envVersion: 'develop' } })
      expect(resolveBaseUrl()).toBe('https://manual.example.com')
    })

    it('resolveBaseUrl：无覆盖时 develop 走 DEV_BASE_URL，release 走 DEFAULT_BASE_URL', () => {
      stubWx({}, { miniProgram: { envVersion: 'develop' } })
      expect(resolveBaseUrl()).toBe(DEV_BASE_URL)
      stubWx({}, { miniProgram: { envVersion: 'release' } })
      expect(resolveBaseUrl()).toBe(DEFAULT_BASE_URL)
    })
  })
})
