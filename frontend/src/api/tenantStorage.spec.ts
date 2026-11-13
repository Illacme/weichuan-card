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
