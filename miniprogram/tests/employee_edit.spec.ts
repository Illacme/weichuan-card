// 员工名片编辑页：回填 → 改字段 → 选图上传 → 提交待审。
// 这里的「审核中」语义容易做错：提交后对外仍显示旧资料，只有超管 approve 才写回，
// 所以页面必须在提交成功后把自己标成 pendingModify，否则用户会以为没提交成功而反复提交。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))

let wxStub: any
let storage: Map<string, any>

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  const installed = installWx()
  wxStub = installed.wx
  storage = installed.storage
  mocks.request.mockClear()
})

async function page(me: any = {}) {
  mocks.request.mockResolvedValueOnce(me)
  await import('../pages/employee/edit/edit')
  const p = makeInstance(capturedPage())
  p.onLoad()
  await flush()
  return p
}

function lastToast(): string {
  const calls = wxStub.showToast.mock.calls
  return calls.length ? calls[calls.length - 1][0].title : ''
}

const meData = {
  code: 0,
  pendingModify: false,
  data: { staffId: 42, name: '张伟', title: '工程师', phone: '138', phone2: '', avatar: '/uploads/a.png' },
}

describe('employee/edit 页', () => {
  it('令牌失效（401）：跳登录页', async () => {
    await page({ code: 401 })

    expect(wxStub.redirectTo).toHaveBeenCalledWith({ url: '/pages/employee/login/login' })
  })

  it('回填资料；头像拼绝对 URL', async () => {
    const p = await page(meData)

    expect(p.data.form.name).toBe('张伟')
    expect(p.data.form.avatar).toBe('https://cdn.test/uploads/a.png')
    expect(p.data.loading).toBe(false)
  })

  it('有待审修改时标 pendingModify（页面显示「审核中」）', async () => {
    const p = await page({ ...meData, pendingModify: true })

    expect(p.data.pendingModify).toBe(true)
  })

  // 存了备用号却不展示输入框，用户会以为「存的东西丢了」
  it('已有备用手机号时自动展开输入框', async () => {
    const p = await page({ ...meData, data: { ...meData.data, phone2: '139' } })

    expect(p.data.showPhone2).toBe(true)
    expect(p.data.form.phone2).toBe('139')
  })

  it('没有数据（非 401）：结束加载、不崩', async () => {
    const p = await page({ code: 1, msg: '无数据' })

    expect(p.data.loading).toBe(false)
    expect(p.data.form.name).toBe('')
  })

  // 收起时清空是一个刻意的决定：留着值会让用户以为「没显示就是没提交」，
  // 结果保存时把暗数据一起写进待审申请
  it('收起备用手机号时清空已填内容', async () => {
    const p = await page({ ...meData, data: { ...meData.data, phone2: '139' } })

    p.togglePhone2() // 收起
    expect(p.data.showPhone2).toBe(false)
    expect(p.data.form.phone2).toBe('')

    p.togglePhone2() // 再展开
    expect(p.data.showPhone2).toBe(true)
  })

  it('输入框按 data-field 写回表单', async () => {
    const p = await page(meData)

    p.onInput({ currentTarget: { dataset: { field: 'title' } }, detail: { value: '高级工程师' } })

    expect(p.data.form.title).toBe('高级工程师')
    expect(p.data.form.name).toBe('张伟') // 其它字段不受影响
  })

  // chooseAvatar 现在是 async：调用隐私接口（chooseMedia）前要先过隐私授权检查，
  // 所以选图动作发生在 await 之后，断言前必须放行一轮微任务。
  it('选图上传成功：头像换成上传后的绝对 URL 并提示', async () => {
    storage.set('empToken', 'tk-1')
    const p = await page(meData)
    wxStub.chooseMedia.mockImplementationOnce((opt: any) =>
      opt.success({ tempFiles: [{ tempFilePath: '/tmp/a.jpg' }] })
    )
    wxStub.uploadFile.mockImplementationOnce((opt: any) =>
      opt.success({ data: JSON.stringify({ code: 0, data: { urls: ['/uploads/new.png'] } }) })
    )

    await p.chooseAvatar()
    await flush()

    expect(wxStub.uploadFile.mock.calls[0][0].header.Authorization).toBe('Bearer tk-1')
    expect(p.data.form.avatar).toBe('https://cdn.test/uploads/new.png')
    expect(lastToast()).toBe('上传成功')
    expect(wxStub.hideLoading).toHaveBeenCalled()
  })

  it('上传返回业务错误：提示后端 msg，头像不变', async () => {
    const p = await page(meData)
    wxStub.chooseMedia.mockImplementationOnce((opt: any) =>
      opt.success({ tempFiles: [{ tempFilePath: '/tmp/a.jpg' }] })
    )
    wxStub.uploadFile.mockImplementationOnce((opt: any) =>
      opt.success({ data: JSON.stringify({ code: 1, msg: '文件太大' }) })
    )
    const before = p.data.form.avatar

    await p.chooseAvatar()
    await flush()

    expect(lastToast()).toBe('文件太大')
    expect(p.data.form.avatar).toBe(before)
  })

  it('上传通道失败（网络）：提示「上传失败」并收起 loading', async () => {
    const p = await page(meData)
    wxStub.chooseMedia.mockImplementationOnce((opt: any) =>
      opt.success({ tempFiles: [{ tempFilePath: '/tmp/a.jpg' }] })
    )
    wxStub.uploadFile.mockImplementationOnce((opt: any) => opt.fail({ errMsg: 'uploadFile:fail' }))

    await p.chooseAvatar()
    await flush()

    expect(lastToast()).toBe('上传失败')
    expect(wxStub.hideLoading).toHaveBeenCalled()
  })

  // 网关 502 / 反代返回 HTML 时响应对象不是 JSON：这里必须先兜住再解析，
  // 否则 JSON.parse 抛异常冒泡出 success 回调，用户只看到转圈的 loading
  // （apply/open 页早就有 try/catch，本页曾漏）
  it('上传响应不是 JSON（网关返回 HTML）：不抛异常且提示失败', async () => {
    const p = await page(meData)
    wxStub.chooseMedia.mockImplementationOnce((opt: any) =>
      opt.success({ tempFiles: [{ tempFilePath: '/tmp/a.jpg' }] })
    )
    wxStub.uploadFile.mockImplementationOnce((opt: any) =>
      opt.success({ data: '<html>502 Bad Gateway</html>' })
    )

    expect(() => p.chooseAvatar()).not.toThrow()
    await flush()

    expect(lastToast()).toBe('上传失败')
    expect(wxStub.hideLoading).toHaveBeenCalled()
  })

  it('提交成功：标 pendingModify 并提示等待审核', async () => {
    const p = await page(meData)
    mocks.request.mockResolvedValueOnce({ code: 0 })

    await p.onSubmit()

    expect(mocks.request).toHaveBeenCalledWith('PUT', '/api/employee/me', p.data.form)
    expect(p.data.pendingModify).toBe(true)
    expect(lastToast()).toBe('已提交，等待审核')
  })

  it('提交失败：提示后端 msg，不改 pendingModify', async () => {
    const p = await page(meData)
    mocks.request.mockResolvedValueOnce({ code: 1, msg: '手机号已被占用' })

    await p.onSubmit()

    expect(lastToast()).toBe('手机号已被占用')
    expect(p.data.pendingModify).toBe(false)
  })
})
