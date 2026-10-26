import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { FALLBACK_BRAND, applyBrandTitle, resolveBrandTitle } from '../utils/brand'

// 品牌名解析 + 「客户名不得写死在小程序源码里」的守门。
//
// 为什么要静态扫描而不只是单测函数：真正的事故形态是**某个文件里又出现了一个客户名**
// （app.json 全局标题、自绘顶栏的 wxml 兜底文案、分享标题兜底……）。
// 这些位置分散在 wxml / json / ts 三种文件里，没有任何一处会因为「函数返回对的值」而报错。
// 所以这里直接把整个小程序目录扫一遍：不许出现演示租户名。

// 演示租户名：只允许出现在 tests / 后端种子数据 / 文档里，不允许出现在小程序源码
const DEMO_TENANT = '朗汇机电'

const HERE = dirname(fileURLToPath(import.meta.url)) // miniprogram/tests
const MP_ROOT = join(HERE, '..')

// 递归收集小程序源码文件（排除 tests 自身：那里用客户名当测试数据是合理的）
function sourceFiles(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    if (name === 'tests' || name === 'node_modules' || name.startsWith('.')) continue
    const p = join(dir, name)
    if (statSync(p).isDirectory()) sourceFiles(p, out)
    else if (/\.(ts|js|json|wxml|wxss)$/.test(name)) out.push(p)
  }
  return out
}

function stubWx(overrides: Record<string, any> = {}) {
  const stub = { setNavigationBarTitle: vi.fn(), ...overrides }
  vi.stubGlobal('wx', stub)
  return stub
}

describe('brand', () => {
  beforeEach(() => {
    // getStorageSync 返回空 → cachedSettings() 为 null，即「后台配置还没拿到」
    vi.stubGlobal('wx', {
      setNavigationBarTitle: vi.fn(),
      getStorageSync: () => '',
    })
  })

  describe('resolveBrandTitle', () => {
    it('优先用当前企业简称', () => {
      expect(resolveBrandTitle({ shortName: '甲公司', fullName: '甲科技有限公司' })).toBe('甲公司')
    })

    it('没有简称时用全称', () => {
      expect(resolveBrandTitle({ fullName: '甲科技有限公司' })).toBe('甲科技有限公司')
    })

    it('企业信息为空串 / 纯空白时继续往下回落', () => {
      expect(resolveBrandTitle({ shortName: '   ', fullName: '' })).toBe(FALLBACK_BRAND)
      expect(resolveBrandTitle(null)).toBe(FALLBACK_BRAND)
      expect(resolveBrandTitle(undefined)).toBe(FALLBACK_BRAND)
    })

    it('没有企业信息时用后台下发的 brand_name', () => {
      vi.stubGlobal('wx', {
        setNavigationBarTitle: vi.fn(),
        getStorageSync: () => JSON.stringify({ brand_name: '部署方品牌' }),
      })
      expect(resolveBrandTitle(null)).toBe('部署方品牌')
    })

    it('企业信息优先于 brand_name（海报是替企业做的物料，不能顶平台名）', () => {
      vi.stubGlobal('wx', {
        setNavigationBarTitle: vi.fn(),
        getStorageSync: () => JSON.stringify({ brand_name: '部署方品牌' }),
      })
      expect(resolveBrandTitle({ shortName: '甲公司' })).toBe('甲公司')
    })
  })

  describe('applyBrandTitle', () => {
    it('把解析结果写进导航栏标题', () => {
      const wx = stubWx()
      applyBrandTitle({ shortName: '甲公司' })
      expect(wx.setNavigationBarTitle).toHaveBeenCalledWith({ title: '甲公司' })
    })

    it('setNavigationBarTitle 抛错时不冒泡（标题属装饰，不能带崩页面）', () => {
      vi.stubGlobal('wx', {
        setNavigationBarTitle: () => {
          throw new Error('page not ready')
        },
      })
      expect(() => applyBrandTitle({ shortName: '甲公司' })).not.toThrow()
    })
  })

  // ───── 守门：源码里不得再出现演示租户名 ─────
  describe('源码守门', () => {
    it('app.json 的全局标题是产品名（不是任何客户名）', () => {
      const app = JSON.parse(readFileSync(join(MP_ROOT, 'app.json'), 'utf8'))
      expect(app.window.navigationBarTitleText).toBe(FALLBACK_BRAND)
    })

    it(`${DEMO_TENANT} 不得出现在小程序源码（ts / js / json / wxml / wxss）里`, () => {
      const hits = sourceFiles(MP_ROOT).filter((f) =>
        readFileSync(f, 'utf8').includes(DEMO_TENANT)
      )
      // 报出具体文件，别只丢一句「匹配失败」——这类断言失败时必须一眼知道改哪
      const names = hits.map((f) => relative(MP_ROOT, f))
      expect(names).toEqual([])
    })
  })
})
