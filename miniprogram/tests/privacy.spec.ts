// 微信隐私授权（utils/privacy.ts）。
//
// 这组用例守的是三条「错了就出事」的规则：
//   1. 用户点「不同意」必须 resolve(false)——不 resolve 就等于把这次点击吞了
//      （调用方 await 着一个永不落地的 Promise，表现为「点了没反应」）。
//   2. 同意后必须走 markAgreed——否则下次调隐私接口又要重新弹窗，反复打扰。
//   3. 基础库不支持（无 getPrivacySetting）时必须放行——旧基础库本就不拦截，
//      拦用户一次功能比放行一次更糟。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installWx, flush } from './helpers/harness'

// 模块级状态（agreed / contractName / pendingResolve）跨用例留存，
// 所以每个用例前要重置模块 + 重置状态。
let privacy: any

beforeEach(async () => {
  vi.resetModules()
  privacy = await import('../utils/privacy')
  privacy.resetConsent()
})

function wxWithPrivacy(needAuthorization: boolean, contractName = '《测试隐私指引》') {
  return installWx({
    getPrivacySetting: vi.fn((opt: any) =>
      opt.success({ needAuthorization, privacyContractName: contractName })
    ),
    openPrivacyContract: vi.fn((opt: any) => opt.success && opt.success({})),
  }).wx
}

describe('utils/privacy', () => {
  // ---- 状态查询 ----

  it('needAuthorization=true：需要用户同意', async () => {
    installWx({
      getPrivacySetting: vi.fn((opt: any) => opt.success({ needAuthorization: true, privacyContractName: '《X》' })),
    })

    expect(await privacy.needConsent()).toBe(true)
  })

  it('needAuthorization=false：不需要同意', async () => {
    installWx({
      getPrivacySetting: vi.fn((opt: any) => opt.success({ needAuthorization: false, privacyContractName: '《X》' })),
    })

    expect(await privacy.needConsent()).toBe(false)
  })

  // 平台收回/未声明隐私类型时，微信侧可能不返回 needAuthorization 字段
  it('微信没返回 needAuthorization：按已同意处理（不拦用户）', async () => {
    installWx({ getPrivacySetting: vi.fn((opt: any) => opt.success({})) })

    expect(await privacy.needConsent()).toBe(false)
  })

  // 旧基础库（< 2.32.3）没有这套接口，微信本就不会拦截
  it('基础库不支持（无 getPrivacySetting）：放行', async () => {
    installWx({})

    expect(privacy.privacySupported()).toBe(false)
    expect(await privacy.needConsent()).toBe(false)
  })

  it('getPrivacySetting 调用失败：放行（不能因查询失败把功能卡死）', async () => {
    installWx({ getPrivacySetting: vi.fn((opt: any) => opt.fail({ errMsg: 'fail' })) })

    expect(await privacy.needConsent()).toBe(false)
  })

  it('记录并返回微信侧的协议名称', async () => {
    installWx({
      getPrivacySetting: vi.fn((opt: any) =>
        opt.success({ needAuthorization: true, privacyContractName: '《微传名片隐私保护指引》' })
      ),
    })

    await privacy.queryPrivacy()
    expect(privacy.privacyContractName()).toBe('《微传名片隐私保护指引》')
  })

  // ---- 同意状态 ----

  it('markAgreed 后不再需要同意（不重复打扰）', async () => {
    installWx({
      getPrivacySetting: vi.fn((opt: any) => opt.success({ needAuthorization: true, privacyContractName: '' })),
    })
    expect(await privacy.needConsent()).toBe(true)

    privacy.markAgreed()
    expect(await privacy.needConsent()).toBe(false)
  })

  // ---- 授权请求的结算 ----

  // 这是最关键的一条：用户点「不同意」时必须 resolve(false)。
  // 不 resolve → 调用方 await 永不落地 → 用户点了「不同意」却什么也没发生。
  it('用户不同意：requestConsent 必须 resolve(false)', async () => {
    const p = privacy.requestConsent()
    privacy.settle(false)

    expect(await p).toBe(false)
  })

  it('用户同意：requestConsent 必须 resolve(true)', async () => {
    const p = privacy.requestConsent()
    privacy.settle(true)

    expect(await p).toBe(true)
  })

  // 并发调用（用户连点两次「上传头像」）不能弹两个弹窗，也不能让先来的那个悬着
  it('并发请求：后到的复用同一个结果，先到的也会被结算', async () => {
    const p1 = privacy.requestConsent()
    const p2 = privacy.requestConsent()
    privacy.settle(true)

    expect(await p1).toBe(true)
    expect(await p2).toBe(true)
  })

  it('settle 在没有待决请求时是安全的空操作', () => {
    expect(() => privacy.settle(true)).not.toThrow()
    expect(() => privacy.settle(false)).not.toThrow()
  })

  // ---- 页面侧编排 ----

  // 页面漏挂 privacy-consent 组件是编码错误，但不能变成「用户点了没反应」——
  // 此时按已同意放行，交由微信官方弹窗兜底。
  it('ensurePrivacy：页面没挂弹窗组件时放行（不吞掉这次点击）', async () => {
    installWx({
      getPrivacySetting: vi.fn((opt: any) => opt.success({ needAuthorization: true, privacyContractName: '' })),
    })
    const page: any = { setData: vi.fn(), selectComponent: () => null }

    expect(await privacy.ensurePrivacy(page)).toBe(true)
  })

  it('ensurePrivacy：已同意时不弹窗，直接放行', async () => {
    installWx({
      getPrivacySetting: vi.fn((opt: any) => opt.success({ needAuthorization: false, privacyContractName: '' })),
    })
    const show = vi.fn()
    const page: any = { setData: vi.fn(), selectComponent: () => ({ id: 'privacy-consent', show }) }

    expect(await privacy.ensurePrivacy(page)).toBe(true)
    expect(page.setData).not.toHaveBeenCalled()
  })

  it('ensurePrivacy：未同意时显示弹窗，等用户同意后放行', async () => {
    installWx({
      getPrivacySetting: vi.fn((opt: any) => opt.success({ needAuthorization: true, privacyContractName: '《X》' })),
    })
    const page: any = { setData: vi.fn(), selectComponent: () => ({ id: 'privacy-consent' }) }

    const p = privacy.ensurePrivacy(page)
    // ensurePrivacy 里先 await needConsent()（一次微任务）才显示弹窗，
    // 所以必须放行一轮微任务再断言，否则查到的还是「还没显示」
    await flush()
    expect(page.setData).toHaveBeenCalledWith({ privacyVisible: true })

    privacy.settle(true)
    expect(await p).toBe(true)
    // 弹窗已收起
    expect(page.setData).toHaveBeenCalledWith({ privacyVisible: false })
  })

  it('ensurePrivacy：用户不同意时返回 false（调用方据此放弃本次操作）', async () => {
    installWx({
      getPrivacySetting: vi.fn((opt: any) => opt.success({ needAuthorization: true, privacyContractName: '《X》' })),
    })
    const page: any = { setData: vi.fn(), selectComponent: () => ({ id: 'privacy-consent' }) }

    const p = privacy.ensurePrivacy(page)
    await flush()
    privacy.settle(false)

    expect(await p).toBe(false)
  })
})
