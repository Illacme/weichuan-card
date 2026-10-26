// 访客自助开通申请页（POST /api/apply/open）：对外公开，没有登录态兜底，
// 所以「校验漏一条 = 脏数据进审核队列」「上传崩了 = 用户填的东西全丢」都不允许。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
  nickname: vi.fn(async (): Promise<string> => ''),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))
vi.mock('../utils/wechat', () => ({
  getOpenid: vi.fn(async () => 'openid-x'),
  getWechatNickname: mocks.nickname,
}))

let wxStub: any

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  wxStub = installWx().wx
  mocks.request.mockClear()
  mocks.nickname.mockClear()
})

async function page(company: any = { code: 0, data: { name: '朗汇机电有限公司', shortName: '朗汇机电' } }) {
  mocks.request.mockResolvedValueOnce(company)
  await import('../pages/apply/open/open')
  const p = makeInstance(capturedPage())
  p.onLoad()
  await flush()
  return p
}

function lastToast(): string {
  const calls = wxStub.showToast.mock.calls
  return calls.length ? calls[calls.length - 1][0].title : ''
}

function uploadWith(body: string) {
  wxStub.chooseMedia.mockImplementationOnce((opt: any) =>
    opt.success({ tempFiles: [{ tempFilePath: '/tmp/a.jpg' }] })
  )
  wxStub.uploadFile.mockImplementationOnce((opt: any) => opt.success({ data: body }))
}

describe('apply/open 页', () => {
  // 让访客知道在申请加入哪家企业：不知道该填给谁，申请就等于石沉大海
  it('onLoad 拉企业名：优先简称', async () => {
    const p = await page()

    expect(mocks.request).toHaveBeenCalledWith('GET', '/api/company')
    expect(p.data.companyName).toBe('朗汇机电')
  })

  it('没有简称时回落到全称', async () => {
    const p = await page({ code: 0, data: { name: '朗汇机电有限公司' } })

    expect(p.data.companyName).toBe('朗汇机电有限公司')
  })

  // 企业接口挂了不能挡住申请入口（这时才最需要有人能提交申请）
  it('企业接口失败：不阻断页面，企业名留空', async () => {
    mocks.request.mockClear()
    mocks.request.mockRejectedValueOnce(new Error('boom'))
    await import('../pages/apply/open/open')
    const p = makeInstance(capturedPage())

    p.onLoad()
    await flush()

    expect(p.data.companyName).toBe('')
  })

  it('输入框按 data-field 写回表单', async () => {
    const p = await page()

    p.onInput({ currentTarget: { dataset: { field: 'phone' } }, detail: { value: '138' } })

    expect(p.data.form.phone).toBe('138')
  })

  it('收起备用手机号时清空已填内容（避免留下暗数据）', async () => {
    const p = await page()
    p.togglePhone2() // 展开
    p.onInput({ currentTarget: { dataset: { field: 'phone2' } }, detail: { value: '139' } })

    p.togglePhone2() // 收起

    expect(p.data.showPhone2).toBe(false)
    expect(p.data.form.phone2).toBe('')
  })

  // ---- 头像 ----

  it('微信头像回调：直接上传并写回表单', async () => {
    const p = await page()
    wxStub.uploadFile.mockImplementationOnce((opt: any) =>
      opt.success({ data: JSON.stringify({ code: 0, data: { urls: ['/uploads/av.png'] } }) })
    )

    p.onChooseAvatar({ detail: { avatarUrl: '/tmp/wx-avatar.png' } })
    // 上传前要先取 openid（wx.login 静默换取），所以上传动作在 await 之后
    await flush()

    expect(wxStub.uploadFile.mock.calls[0][0].url).toBe('https://api.test/api/apply/open/avatar')
    expect(p.data.form.avatar).toBe('https://cdn.test/uploads/av.png')
    expect(lastToast()).toBe('头像已设置')
  })

  // 后端据此把图送微信内容安全检测：这张图上传成功即返回永久 URL，
  // 是立刻公开可访问的，不送检等于「违规图在人工审核之前已经挂网上」。
  it('上传头像时带上 openid（供后端送内容安全检测）', async () => {
    const p = await page()
    wxStub.uploadFile.mockImplementationOnce((opt: any) =>
      opt.success({ data: JSON.stringify({ code: 0, data: { urls: ['/uploads/av.png'] } }) })
    )

    p.onChooseAvatar({ detail: { avatarUrl: '/tmp/wx-avatar.png' } })
    await flush()

    expect(wxStub.uploadFile.mock.calls[0][0].formData).toEqual({ openid: 'openid-x' })
  })

  // chooseImage 现在是 async：调用隐私接口（chooseMedia）前要先过隐私授权检查，
  // 所以选图动作发生在 await 之后，断言前必须放行一轮微任务。
  it('相册选图：走同一个上传通道', async () => {
    const p = await page()
    uploadWith(JSON.stringify({ code: 0, data: { urls: ['/uploads/av2.png'] } }))

    await p.chooseImage()
    await flush()

    expect(p.data.form.avatar).toBe('https://cdn.test/uploads/av2.png')
  })

  it('上传业务错误：提示后端 msg，头像不变', async () => {
    const p = await page()
    uploadWith(JSON.stringify({ code: 1, msg: '图片超过 5MB' }))

    await p.chooseImage()
    await flush()

    expect(lastToast()).toBe('图片超过 5MB')
    expect(p.data.form.avatar).toBe('')
  })

  // 网关 502 时响应体是 HTML：这里已有 try/catch 兜底，别在重构时弄丢
  it('上传响应不是 JSON：不抛异常且提示失败', async () => {
    const p = await page()
    uploadWith('<html>502 Bad Gateway</html>')

    expect(() => p.chooseImage()).not.toThrow()
    await flush()

    expect(lastToast()).toBe('上传失败')
    expect(wxStub.hideLoading).toHaveBeenCalled()
  })

  it('上传通道失败：提示「上传失败」', async () => {
    const p = await page()
    wxStub.chooseMedia.mockImplementationOnce((opt: any) =>
      opt.success({ tempFiles: [{ tempFilePath: '/tmp/a.jpg' }] })
    )
    wxStub.uploadFile.mockImplementationOnce((opt: any) => opt.fail({ errMsg: 'fail' }))

    await p.chooseImage()
    await flush()

    expect(lastToast()).toBe('上传失败')
    expect(wxStub.hideLoading).toHaveBeenCalled()
  })

  // ---- 昵称 ----

  it('授权获取昵称：写回表单（提交时随表单上传）', async () => {
    const p = await page()
    mocks.nickname.mockResolvedValueOnce('小明')

    p.authorizeNickname()
    await flush()

    expect(p.data.form.nickname).toBe('小明')
  })

  // 昵称只是后台核实用途，拿不到要明确告诉用户可以跳过
  it('拿不到昵称：提示可跳过', async () => {
    const p = await page()
    mocks.nickname.mockResolvedValueOnce('')

    p.authorizeNickname()
    await flush()

    expect(lastToast()).toBe('未获取到昵称（可跳过）')
  })

  // ---- 提交 ----

  // 提交类用例都要先勾隐私政策同意（A6）：表单采集姓名/手机/邮箱等个人信息，
  // 未取得用户明示同意不得提交。fillBase 负责填必填项 + 勾同意。
  async function fillBase(p: any) {
    p.onInput({ currentTarget: { dataset: { field: 'name' } }, detail: { value: '张伟' } })
    p.onInput({ currentTarget: { dataset: { field: 'phone' } }, detail: { value: '138' } })
    if (!p.data.agreed) p.toggleAgree()
  }

  it('缺姓名或手机：提示且不发请求（脏申请会直接进审核队列）', async () => {
    const p = await page()
    mocks.request.mockClear()

    await p.onSubmit()
    expect(lastToast()).toBe('请填写姓名与手机')

    p.onInput({ currentTarget: { dataset: { field: 'name' } }, detail: { value: '张伟' } })
    await p.onSubmit()
    expect(mocks.request).not.toHaveBeenCalled()

    p.onInput({ currentTarget: { dataset: { field: 'phone' } }, detail: { value: '138' } })
    p.toggleAgree()
    mocks.request.mockResolvedValueOnce({ code: 0 })
    await p.onSubmit()
    expect(mocks.request).toHaveBeenCalledTimes(1)
  })

  // ---- 隐私政策明示同意（A6 / PIPL「告知—同意」） ----

  it('未勾选隐私政策：拒绝提交（采集个人信息必须先取得明示同意）', async () => {
    const p = await page()
    p.onInput({ currentTarget: { dataset: { field: 'name' } }, detail: { value: '张伟' } })
    p.onInput({ currentTarget: { dataset: { field: 'phone' } }, detail: { value: '138' } })
    mocks.request.mockClear()

    await p.onSubmit()

    expect(lastToast()).toBe('请先阅读并同意隐私政策')
    expect(mocks.request).not.toHaveBeenCalled()
  })

  it('勾选隐私政策后可提交', async () => {
    const p = await page()
    await fillBase(p)
    mocks.request.mockClear()
    mocks.request.mockResolvedValueOnce({ code: 0 })

    await p.onSubmit()

    expect(mocks.request).toHaveBeenCalledTimes(1)
  })

  it('同意勾选框可来回切换', async () => {
    const p = await page()

    expect(p.data.agreed).toBe(false)
    p.toggleAgree()
    expect(p.data.agreed).toBe(true)
    p.toggleAgree()
    expect(p.data.agreed).toBe(false)
  })

  // 「查看隐私政策」：点链接要跳到应用内的隐私政策页（微信审核要求可查阅）
  it('点《隐私政策》跳到隐私政策页', async () => {
    const p = await page()

    p.goPrivacy()

    expect(wxStub.navigateTo).toHaveBeenCalledWith({ url: '/pages/privacy/privacy' })
  })

  it('提交：带上表单全部字段与 openid', async () => {
    const p = await page()
    await fillBase(p)
    mocks.request.mockResolvedValueOnce({ code: 0 })

    await p.onSubmit()

    expect(mocks.request).toHaveBeenCalledWith('POST', '/api/apply/open', {
      name: '张伟',
      phone: '138',
      phone2: '',
      title: '',
      landline: '',
      email: '',
      avatar: '',
      nickname: '',
      openid: 'openid-x',
    })
  })

  it('提交成功：置 submitted 并复位 saving', async () => {
    const p = await page()
    await fillBase(p)
    mocks.request.mockResolvedValueOnce({ code: 0 })

    await p.onSubmit()

    expect(p.data.submitted).toBe(true)
    expect(p.data.saving).toBe(false)
  })

  it('提交失败：提示后端 msg，saving 必须落下（否则按钮永久转圈）', async () => {
    const p = await page()
    await fillBase(p)
    mocks.request.mockResolvedValueOnce({ code: 1, msg: '该手机号已提交过申请' })

    await p.onSubmit()

    expect(lastToast()).toBe('该手机号已提交过申请')
    expect(p.data.saving).toBe(false)
    expect(p.data.submitted).toBe(false)
  })

  it('返回「我的」：栈内无上一页时兜底 reLaunch', async () => {
    const p = await page()
    wxStub.navigateBack.mockImplementationOnce((opt: any) => opt.fail && opt.fail())

    p.backMe()

    expect(wxStub.reLaunch).toHaveBeenCalledWith({ url: '/pages/employee/me/me' })
  })
})
