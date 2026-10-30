// 「存入通讯录」的隐私前置（utils/contact.ts）。
//
// 真实事故一（2026-10-30 上午）：首页既没挂 privacy-consent 组件，saveCard 也没调
// ensurePrivacy，于是 addPhoneContact 直接以 privacy 失败，代码弹出「请前往设置中开启后重试」——
// 可隐私授权根本不在系统设置里，而是在小程序内的弹窗点「同意」。
//
// 真实事故二（2026-10-30 晚）：修完之后变成了死循环——点「存入通讯录」弹「保存名片」，
// 确定后弹「需要隐私授权，请重新点击并选择同意」，点了确定什么也没发生，再点还是这两下。
// 根因是把所有 errMsg 带 "privacy" 的失败都当成「未授权」：
// 其中「平台未声明隐私类型」（errno 112）再怎么让用户点同意也永远不会成功。
//
// 所以这里钉住五件事：
//  1. 未同意隐私政策时不得调用 addPhoneContact；
//  2. 同意后先走「保存名片」确认，确认了才调 addPhoneContact；
//  3. 未授权失败要**主动**拉起授权并重试一次，不能只提示用户再点一遍（那就是死循环）；
//  4. 未声明（errno 112）必须单独提示，文案不能引导用户去「同意」；
//  5. 通讯录权限被拒（deny）才该引导去微信设置——三种 fail 不能混。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  ensurePrivacy: vi.fn(async (..._a: any[]): Promise<boolean> => true),
  requireAuthorize: vi.fn(async (): Promise<boolean> => true),
}))

vi.mock('../utils/privacy', async () => {
  // privacyFailKind 是纯分类函数，用真实现（用假实现等于没测到分类逻辑）
  const actual: any = await vi.importActual('../utils/privacy')
  return {
    ensurePrivacy: mocks.ensurePrivacy,
    requireAuthorize: mocks.requireAuthorize,
    privacyFailKind: actual.privacyFailKind,
  }
})

let wxStub: any

beforeEach(() => {
  vi.resetModules()
  wxStub = installWx().wx
  mocks.ensurePrivacy.mockReset()
  mocks.ensurePrivacy.mockResolvedValue(true)
  mocks.requireAuthorize.mockReset()
  mocks.requireAuthorize.mockResolvedValue(true)
})

const emp = {
  name: '高立新',
  title: '业务经理',
  phone: '13832603521',
  landline: '',
  email: 'gao@example.com',
}
const comp = {
  fullName: '廊坊市朗汇机电设备有限公司',
  address: '廊坊市广阳区紫金城商住楼1-2-1911',
  tel: '0316-8023950',
}

async function loadContact() {
  return await import('../utils/contact')
}

/** 让 showModal 一律按「用户点了确认」应答。
    失败分支还会再弹一个纯提示 modal（没有 success 回调），要跳过它。 */
function confirmAll() {
  wxStub.showModal.mockImplementation((o: any) => {
    if (typeof o.success === 'function') o.success({ confirm: true })
  })
}

/** 取最后一次 showModal 的入参 */
function lastModal() {
  const calls = wxStub.showModal.mock.calls
  return calls[calls.length - 1][0]
}

describe('saveCard：隐私前置', () => {
  it('未同意隐私政策：不弹保存确认，也不调 addPhoneContact', async () => {
    mocks.ensurePrivacy.mockResolvedValue(false)
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)

    expect(mocks.ensurePrivacy).toHaveBeenCalledTimes(1)
    expect(wxStub.showModal).not.toHaveBeenCalled()
    expect(wxStub.addPhoneContact).not.toHaveBeenCalled()
  })

  it('页面实例会透传给 ensurePrivacy（它要靠 page.selectComponent 找弹窗）', async () => {
    const page = { selectComponent: vi.fn() }
    const { saveCard } = await loadContact()

    await saveCard(page, emp, comp)

    expect(mocks.ensurePrivacy).toHaveBeenCalledWith(page)
  })

  it('已同意：先弹「保存名片」确认，确认后才调 addPhoneContact', async () => {
    confirmAll()
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)

    expect(wxStub.showModal).toHaveBeenCalledTimes(1)
    expect(wxStub.showModal.mock.calls[0][0].title).toBe('保存名片')
    expect(wxStub.addPhoneContact).toHaveBeenCalledTimes(1)
  })

  it('确认框里点取消：不调 addPhoneContact', async () => {
    wxStub.showModal.mockImplementation((o: any) => o.success({ confirm: false }))
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)

    expect(wxStub.addPhoneContact).not.toHaveBeenCalled()
  })
})

describe('saveCard：字段映射', () => {
  it('本人没座机时，工作电话回退公司总机（与名片展示同一套规则）', async () => {
    confirmAll()
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)

    const arg = wxStub.addPhoneContact.mock.calls[0][0]
    expect(arg.workPhoneNumber).toBe('0316-8023950')
    expect(arg.firstName).toBe('高立新')
    expect(arg.mobilePhoneNumber).toBe('13832603521')
    expect(arg.organization).toBe('廊坊市朗汇机电设备有限公司')
  })

  it('本人有座机时优先用本人的', async () => {
    confirmAll()
    const { saveCard } = await loadContact()

    await saveCard({}, { ...emp, landline: '0316-1111111' }, comp)

    expect(wxStub.addPhoneContact.mock.calls[0][0].workPhoneNumber).toBe('0316-1111111')
  })
})

describe('saveCard：失败分支的文案', () => {
  it('保存成功：toast「已保存」', async () => {
    confirmAll()
    wxStub.addPhoneContact.mockImplementation((o: any) => o.success())
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)

    expect(wxStub.showToast).toHaveBeenCalledWith(expect.objectContaining({ title: '已保存' }))
  })

  // 死循环事故的直接回归：未授权失败必须**主动**再拉起一次授权并重试，
  // 而不是弹一句「请重新点击」把球踢回给用户（用户点了还是同一个失败）。
  it('未授权失败：主动拉起授权并重试一次', async () => {
    confirmAll()
    let n = 0
    wxStub.addPhoneContact.mockImplementation((o: any) => {
      n += 1
      // 第一次未授权 → 补授权 → 第二次成功
      if (n === 1) o.fail({ errMsg: 'addPhoneContact:fail privacy permission is not authorized' })
      else o.success()
    })
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)
    await flush()

    expect(mocks.requireAuthorize).toHaveBeenCalledTimes(1)
    expect(wxStub.addPhoneContact).toHaveBeenCalledTimes(2)
    expect(wxStub.showToast).toHaveBeenCalledWith(expect.objectContaining({ title: '已保存' }))
  })

  it('补授权仍失败：只重试一次就停，弹「需要隐私授权」且不提设置', async () => {
    confirmAll()
    wxStub.addPhoneContact.mockImplementation((o: any) =>
      o.fail({ errMsg: 'addPhoneContact:fail privacy permission is not authorized' })
    )
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)
    await flush()

    // 只重试一次：无限重试就是另一个死循环
    expect(wxStub.addPhoneContact).toHaveBeenCalledTimes(2)
    const m = lastModal()
    expect(m.title).toBe('需要隐私授权')
    expect(m.content).toContain('隐私政策')
    // 隐私授权不在系统设置里，指向设置等于把用户带进死胡同
    expect(m.content).not.toContain('设置')
  })

  it('用户在官方弹窗里拒绝授权：不再重试，直接提示', async () => {
    confirmAll()
    mocks.requireAuthorize.mockResolvedValue(false)
    wxStub.addPhoneContact.mockImplementation((o: any) =>
      o.fail({ errMsg: 'addPhoneContact:fail privacy permission is not authorized' })
    )
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)
    await flush()

    expect(mocks.requireAuthorize).toHaveBeenCalledTimes(1)
    expect(wxStub.addPhoneContact).toHaveBeenCalledTimes(1)
    expect(lastModal().title).toBe('需要隐私授权')
  })

  // errno 112：平台没在《用户隐私保护指引》里勾「通讯录（仅写入）」。
  // 这不是用户能解决的问题，提示里再出现「同意」就是把人往死路上带。
  it('平台未声明（errno 112）：提示补声明，不能引导用户去「同意」', async () => {
    confirmAll()
    wxStub.addPhoneContact.mockImplementation((o: any) =>
      o.fail({
        errno: 112,
        errMsg: 'addPhoneContact:fail api scope is not declared in the privacy agreement',
      })
    )
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)
    await flush()

    // 未声明不是授权问题，别再去拉授权弹窗
    expect(mocks.requireAuthorize).not.toHaveBeenCalled()
    expect(wxStub.addPhoneContact).toHaveBeenCalledTimes(1)
    const m = lastModal()
    expect(m.title).toBe('功能暂不可用')
    expect(m.content).toContain('声明')
    expect(m.content).not.toContain('同意')
  })

  it('deny（通讯录权限被拒）：才引导去微信设置', async () => {
    confirmAll()
    wxStub.addPhoneContact.mockImplementation((o: any) =>
      o.fail({ errMsg: 'addPhoneContact:fail auth deny' })
    )
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)
    await flush()

    const m = lastModal()
    expect(m.title).toBe('需要通讯录权限')
    expect(m.content).toContain('设置')
  })

  it('其余失败（用户主动取消）：按「已取消」处理，不弹 modal', async () => {
    confirmAll()
    wxStub.addPhoneContact.mockImplementation((o: any) => o.fail({ errMsg: 'addPhoneContact:fail cancel' }))
    const { saveCard } = await loadContact()

    await saveCard({}, emp, comp)
    await flush()

    expect(wxStub.showToast).toHaveBeenCalledWith(expect.objectContaining({ title: '已取消' }))
    // 只有「保存名片」那一次 modal，失败没有再弹第二个
    expect(wxStub.showModal).toHaveBeenCalledTimes(1)
  })
})
