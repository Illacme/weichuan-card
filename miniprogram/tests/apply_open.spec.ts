// 访客自助开通申请页（POST /api/apply/open）：对外公开，没有登录态兜底，
// 所以「校验漏一条 = 脏数据进审核队列」「上传崩了 = 用户填的东西全丢」都不允许。
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { installPageCapture, capturedPage, makeInstance, installWx, flush } from './helpers/harness'

const mocks = vi.hoisted(() => ({
  request: vi.fn(async (..._args: any[]): Promise<any> => ({})),
  // 隐私：默认「已同意」（与 harness 的 wx 桩一致——它没有 getPrivacySetting），
  // 需要走「未同意」分支的用例各自 mockResolvedValueOnce(true) 覆盖。
  ensurePrivacy: vi.fn(async (..._args: any[]): Promise<boolean> => true),
  needConsent: vi.fn(async (..._args: any[]): Promise<boolean> => false),
}))

vi.mock('../utils/request', () => ({
  request: mocks.request,
  assetUrl: (u: string) =>
    u ? (/^https?:\/\//i.test(u) ? u : `https://cdn.test${u.startsWith('/') ? '' : '/'}${u}`) : '',
  apiBaseUrl: () => 'https://api.test',
}))
vi.mock('../utils/wechat', () => ({
  getOpenid: vi.fn(async () => 'openid-x'),
}))
vi.mock('../utils/privacy', () => ({
  ensurePrivacy: mocks.ensurePrivacy,
  needConsent: mocks.needConsent,
}))

let wxStub: any

beforeEach(() => {
  vi.resetModules()
  installPageCapture()
  wxStub = installWx().wx
  mocks.request.mockClear()
  mocks.ensurePrivacy.mockReset()
  mocks.ensurePrivacy.mockResolvedValue(true)
  mocks.needConsent.mockReset()
  mocks.needConsent.mockResolvedValue(false)
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

  // 昵称改用官方「头像昵称填写能力」的原生输入框（type="nickname"）：
  // 微信自动填充或用户手填，都经 bindinput 写进 form.nickname，
  // 与姓名/手机等字段走同一条路，提交时随表单一起带上。
  it('昵称输入框：填入即写入表单（提交时随表单上传）', async () => {
    const p = await page()

    p.onInput({ currentTarget: { dataset: { field: 'nickname' } }, detail: { value: '小明' } })

    expect(p.data.form.nickname).toBe('小明')
  })

  // 旧实现是点「授权获取」调 wx.getUserProfile 弹窗取昵称——该接口已被平台收回，
  // 此后永远返回占位值「微信用户」，点了等于白点。页面不该再留这条路径。
  it('不再提供 authorizeNickname（getUserProfile 已被平台收回）', async () => {
    const p = await page()

    expect(typeof p.authorizeNickname).toBe('undefined')
  })

  // 源码守门：昵称必须是官方填写能力的原生输入框。
  // 上面两条只能验证「值能写进表单」，验证不了 wxml 用的是哪种控件——
  // 而「改回普通 input」这类回退在真机上的表现只是「不再自动填充昵称」，
  // 不报错、不失败，没人会察觉，只能靠读源码钉住。
  it('源码守门：昵称行用 type="nickname" 且不再挂旧的授权按钮', () => {
    // 路径解析方式与 brand.spec.ts 的源码守门保持一致（import.meta.url 在
    // 本环境不是 file 协议，直接 new URL(...) 会报 scheme 错）
    const HERE = dirname(fileURLToPath(import.meta.url)) // miniprogram/tests
    const wxml = readFileSync(join(HERE, '..', 'pages/apply/open/open.wxml'), 'utf8')
    // 必须先剥掉注释再匹配：说明性注释里会提到 type="nickname" 这个写法，
    // 不剥的话「把控件改回普通 input」这种真回退会被注释里的同名文本掩盖过去
    const body = wxml.replace(/<!--[\s\S]*?-->/g, '')
    expect(body).toMatch(/type="nickname"/)
    // 真正的回归信号是「又挂回旧的授权按钮」；注释里提到 API 名是允许的，
    // 所以这里查绑定而不是查字符串 getUserProfile
    expect(body).not.toMatch(/authorizeNickname/)
  })

  // ---- 昵称的隐私授权（errno 104 静默降级）----
  //
  // 这是本页最容易「悄悄坏掉」的一处：<input type="nickname"> 是唯一不会自己
  // 弹隐私授权窗的组件，未取得授权时它**静默降级成普通输入框**——不报错、不失败，
  // 用户只看到「填了没反应」，界面上完全看不出是授权没拿到。所以聚焦时由页面
  // 自己先补一次授权，这段逻辑必须有测试钉住。

  it('昵称聚焦：已同意过隐私 → 直接放行（不弹窗、不重新聚焦）', async () => {
    const p = await page()

    await p.onNicknameFocus()

    expect(mocks.ensurePrivacy).not.toHaveBeenCalled()
    expect(p.data.nicknameChecked).toBe(true)
    // 不驱动 focus，避免键盘白闪一次
    expect(p.data.nicknameFocus).toBe(false)
    expect(lastToast()).toBe('')
  })

  it('昵称聚焦：未同意隐私 → 先补授权，同意后重新聚焦', async () => {
    const p = await page()
    mocks.needConsent.mockResolvedValueOnce(true)

    await p.onNicknameFocus()

    expect(mocks.ensurePrivacy).toHaveBeenCalled()
    expect(p.data.nicknameChecked).toBe(true)
    expect(p.data.nicknameFocus).toBe(false) // 先失焦
    // 再等一轮宏任务：组件要在授权态下重新初始化才会拉起键盘上方的微信昵称
    await new Promise((r) => setTimeout(r, 60))
    expect(p.data.nicknameFocus).toBe(true)
  })

  it('昵称聚焦：用户不同意 → 提示且不标记，下次聚焦还能重试', async () => {
    const p = await page()
    mocks.needConsent.mockResolvedValueOnce(true)
    mocks.ensurePrivacy.mockResolvedValueOnce(false)

    await p.onNicknameFocus()

    expect(lastToast()).toBe('需同意隐私政策才能填入微信昵称')
    // 不置 checked：用户改主意后再点一次还能走一遍授权
    expect(p.data.nicknameChecked).toBe(false)
  })

  it('源码守门：昵称输入框挂了 bindfocus（漏了就静默降级为普通输入框）', () => {
    const HERE = dirname(fileURLToPath(import.meta.url))
    const wxml = readFileSync(join(HERE, '..', 'pages/apply/open/open.wxml'), 'utf8')
    const body = wxml.replace(/<!--[\s\S]*?-->/g, '')
    expect(body).toMatch(/bindfocus="onNicknameFocus"/)
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
