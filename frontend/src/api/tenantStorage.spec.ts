// 请求层打桩：tenantStorage.ts 只做 URL 与「凭据三态」的组装，
// 真去 import ./request 会牵出 router / store / element-plus 一整条链，
// 本机内存跑 vitest 容易被杀（与 audit.spec.ts 同一处理）。
vi.mock('./request', () => {
  const get = vi.fn(() => Promise.resolve({ code: 0, data: { configured: false } }))
  const put = vi.fn(() => Promise.resolve({ code: 0, data: { configured: true } }))
  const post = vi.fn(() =>
    Promise.resolve({ code: 0, data: { ok: true, status: 'ok', checkedScope: 'scope' } })
  )
  const del = vi.fn(() => Promise.resolve({ code: 0, msg: 'ok' }))
  return { default: { get, put, post, delete: del } }
})

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import http from './request'
import {
  checkTenantStorage,
  getTenantStorage,
  resetTenantStorage,
  saveTenantStorage,
  type TenantStorageSavePayload,
} from './tenantStorage'

const m = http as unknown as {
  get: ReturnType<typeof vi.fn>
  put: ReturnType<typeof vi.fn>
  post: ReturnType<typeof vi.fn>
  delete: ReturnType<typeof vi.fn>
}

describe('租户存储配置 api', () => {
  it('四个接口都挂在 /admin/companies/:id/storage 下', async () => {
    await getTenantStorage(7)
    expect(m.get).toHaveBeenCalledWith('/admin/companies/7/storage')

    await saveTenantStorage(7, { driver: 'minio', endpoint: 'a.b', bucket: 'b' })
    expect(m.put).toHaveBeenCalledWith('/admin/companies/7/storage', expect.any(Object))

    await checkTenantStorage(7)
    expect(m.post).toHaveBeenCalledWith('/admin/companies/7/storage/check')

    await resetTenantStorage(7)
    expect(m.delete).toHaveBeenCalledWith('/admin/companies/7/storage')
  })

  it('读出的是信封里的 data，不是整包', async () => {
    const cfg = await getTenantStorage(1)
    expect(cfg).toEqual({ configured: false })
  })

  it('自检返回结果同样解包到 data', async () => {
    const res = await checkTenantStorage(1)
    expect(res.ok).toBe(true)
    expect(res.checkedScope).toBe('scope')
  })
})

// 凭据三态是这一层最容易被写错的契约，单独一组守门：
//   不传  = 保留原值（界面回显掩码时点保存走这条）
//   ""    = 清空
//   有值  = 覆盖
// 把「不传」写成「传空串」，一次改 bucket 的操作就会抹掉客户的凭据，
// 而接口照样回 code:0 —— 配置看上去保存成功，上传时才全线签名失败。
describe('凭据三态不进请求体里的空字段', () => {
  it('没有传凭据时，payload 里不该出现 accessKey / secretKey 键', () => {
    const p: TenantStorageSavePayload = {
      driver: 'minio',
      endpoint: 'play.min.io',
      bucket: 'b',
      enabled: true,
    }
    expect(Object.prototype.hasOwnProperty.call(p, 'accessKey')).toBe(false)
    expect(Object.prototype.hasOwnProperty.call(p, 'secretKey')).toBe(false)
  })

  it('显式空串表示清空，与「不传」必须能区分', () => {
    const clearing: TenantStorageSavePayload = { driver: 'minio', endpoint: 'a', bucket: 'b', accessKey: '', secretKey: '' }
    expect(clearing.accessKey).toBe('')
    expect(clearing.accessKey === undefined).toBe(false)
  })
})

// 源码级守门必须先剥注释：注释里解释「历史上写死过 false / 旧文案是误导」
// 不是一次真实实现。第六十八轮后端守门就踩过（朴素 Contains 把注释判成违规），
// 这里对 Go 的 // 行注释、TS 的 /** */ 与 Vue 的 <!-- --> 一视同仁。
function stripComments(src: string): string {
  return src
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .filter((line) => !/^\s*\/\//.test(line))
    .join('\n')
}

// 第六十九轮：客户自有访问域名的跨端契约。
// 端上类型只写真正消费的字段，所以这里直接对账后端 gin.H 的键，
// 少一个字段界面就会拿到 undefined，却仍按「已生效 / 未生效」去渲染。
//
// 这一组还要盯住「不许写死结论」：第六十八轮守的是 `const publicBaseURLActive = false`，
// 第六十九轮出参侧真的接上后，两个方向（继续写死 false / 前端继续写「尚未接入」）
// 都会让超管误判切量状态，所以两条都必须由断言钉死。
describe('自有访问域名的跨端字段契约', () => {
  // cwd 是 frontend（vitest 项目根），跨到 backend / miniprogram 读源码。
  const backend = (p: string) => readFileSync(resolve(process.cwd(), '../backend', p), 'utf8')
  const mini = (p: string) => readFileSync(resolve(process.cwd(), '../miniprogram', p), 'utf8')
  const src = {
    go: backend('internal/handler/tenant_storage.go'),
    origin: backend('internal/handler/publicorigin.go'),
    publicGet: backend('internal/handler/handler.go'),
    admin: backend('internal/handler/content.go'),
    settings: backend('internal/handler/settings.go'),
    ts: readFileSync(resolve(process.cwd(), 'src/api/tenantStorage.ts'), 'utf8'),
    dialog: readFileSync(resolve(process.cwd(), 'src/components/TenantStorageDialog.vue'), 'utf8'),
    mp: mini('utils/request.ts'),
  }
  // 所有断言一律看**剥注释后的代码**，不是注释文本。
  const code = {
    go: stripComments(src.go),
    origin: stripComments(src.origin),
    publicGet: stripComments(src.publicGet),
    admin: stripComments(src.admin),
    settings: stripComments(src.settings),
    ts: stripComments(src.ts),
    dialog: stripComments(src.dialog),
    mp: stripComments(src.mp),
  }

  it('量尺自己有读数（避免源码路径写错后空跑通过）', () => {
    expect(code.go).toContain('func tenantStorageView(')
    expect(code.origin).toContain('func publicOriginState(')
    expect(code.publicGet.length).toBeGreaterThan(1000)
    expect(code.ts).toContain('export interface TenantStorageConfig')
    expect(code.dialog.length).toBeGreaterThan(1000)
  })

  for (const key of ['publicBaseUrl', 'publicBaseUrlActive', 'publicBaseUrlNote'] as const) {
    it(`后端出参与前端类型都有 ${key}`, () => {
      expect(code.go).toContain(`"${key}"`)
      expect(code.ts).toContain(`${key}:`)
    })
  }

  it('后端不再写死 false：active 由 publicOriginState 实况计算', () => {
    expect(code.go).not.toContain('const publicBaseURLActive = false')
    expect(code.go).toContain('publicOriginState(row)')
    expect(code.go).toContain('view["publicBaseUrlActive"] = active')
    expect(code.go).toContain('publicBaseURLNote(row.PublicBaseURL, active, reason)')
  })

  it('生效判定与 9 条公开 GET 的 origin 出同一个函数', () => {
    expect(code.origin).toContain('func publicOriginStateFor(')
    expect(code.origin).toContain('return publicOriginState(row)')
    expect(code.origin).toContain('tenantStorageActive(row)')
    // 单点：publicOriginFor 不许自己另写一套「只看域名有没有值」的判定
    expect(code.origin).toContain('base, active, _ := publicOriginStateFor(db, cid)')
    expect(code.origin).toContain('if !active || base == ""')
  })

  it('9 条公开 GET 都按租户取 origin，/api/card 用员工归属企业', () => {
    expect(code.publicGet).toContain('origin := publicOriginFor(db, e.CompanyID)')
    expect(code.publicGet).not.toContain('ResolveRefs(model.PlatformOrigin())')
    expect(
      (code.publicGet.match(/publicOriginFor\(db, cid\)/g) || []).length,
      '8 条按 companyId 的公开 GET 少了接线',
    ).toBeGreaterThanOrEqual(8)
  })

  it('后台出口一律用平台零值，不许接租户域名', () => {
    expect(code.admin).toContain('.ResolveRefs(model.PlatformOrigin())')
    expect(code.admin).not.toContain('publicOriginFor(')
  })

  it('/api/public/settings 只在显式 companyId 时回传诊断态', () => {
    expect(code.settings).toContain('c.Query("companyId")')
    expect(code.settings).toContain('resp["assetOriginActive"]')
    // 无条件下发会在冷启动（拿不到 companyId）时按缺省企业串号
    expect(code.settings).toContain('if q, err := strconv.ParseInt')
  })

  it('前端按 active 切换两态提示，不再写死「尚未接入」', () => {
    expect(code.dialog).toContain('publicBaseUrlNotice')
    expect(code.dialog).not.toContain('尚未接入')
    expect(code.dialog).toContain("active ? '自有域名已生效'")
    expect(code.ts).not.toContain('恒为 false')
    expect(code.ts).not.toContain('不会改变出参 URL')
  })

  it('端上 assetUrl 对绝对 URL 原样返回（出参换域名不需要改小程序）', () => {
    expect(code.mp).toContain('export function assetUrl(')
    expect(code.mp).toContain('return p')
    expect(code.mp).toMatch(/\^https\?:/)
  })
})

describe('自有访问域名三态不进请求体里的空字段', () => {
  it('不传时 payload 里没有该键，空串则必须真的出现', () => {
    const keep: TenantStorageSavePayload = { driver: 'minio', endpoint: 'a', bucket: 'b' }
    expect(Object.prototype.hasOwnProperty.call(keep, 'publicBaseUrl')).toBe(false)
    const clearing: TenantStorageSavePayload = { ...keep, publicBaseUrl: '' }
    expect(clearing.publicBaseUrl).toBe('')
    expect(Object.prototype.hasOwnProperty.call(clearing, 'publicBaseUrl')).toBe(true)
  })

  it('保存时 publicBaseUrl 会原样随 payload 发出', async () => {
    await saveTenantStorage(7, {
      driver: 'minio', endpoint: 'a.b', bucket: 'b', publicBaseUrl: 'https://img.cust.com',
    })
    expect(m.put).toHaveBeenCalledWith(
      '/admin/companies/7/storage',
      expect.objectContaining({ publicBaseUrl: 'https://img.cust.com' })
    )
  })
})
