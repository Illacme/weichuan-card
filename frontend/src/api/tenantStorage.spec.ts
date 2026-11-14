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

// 第六十八轮：客户自有访问域名的三态与「如实回报」字段。
// 端上类型只写真正消费的字段，所以这里直接对账后端 gin.H 的键，
// 少一个字段界面就会拿到 undefined，却仍按「已生效 / 未生效」去渲染。
describe('自有访问域名的跨端字段契约', () => {
  // cwd 是 frontend（vitest 项目根），跨到 backend 读 Go 源码。
  const goSrc = readFileSync(
    resolve(process.cwd(), '../backend/internal/handler/tenant_storage.go'),
    'utf8'
  )
  const tsSrc = readFileSync(resolve(process.cwd(), 'src/api/tenantStorage.ts'), 'utf8')

  it('量尺自己有读数（避免源码路径写错后空跑通过）', () => {
    expect(goSrc).toContain('func tenantStorageView(')
    expect(goSrc.length).toBeGreaterThan(1000)
    expect(tsSrc).toContain('export interface TenantStorageConfig')
  })

  for (const key of ['publicBaseUrl', 'publicBaseUrlActive', 'publicBaseUrlNote'] as const) {
    it(`后端出参与前端类型都有 ${key}`, () => {
      expect(goSrc).toContain(`"${key}"`)
      expect(tsSrc).toContain(`${key}:`)
    })
  }

  it('后端明确写死 publicBaseUrlActive = false（不许假装已接入）', () => {
    expect(goSrc).toContain('const publicBaseURLActive = false')
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
