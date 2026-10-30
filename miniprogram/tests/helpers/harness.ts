// 小程序页面测试的公共桩。
//
// 原生小程序的页面是「全局 Page() 注册 + 框架驱动生命周期」，没有可 import 的类。
// 所以测试的做法是：桩掉全局 Page 捕获选项 → 手工造一个实例 → 按真机顺序调
// onLoad/onShow/事件处理函数。与 poster.spec.ts 里验证过的手法一致，
// 这里抽出来给其余页面复用（该文件当初内联，保留原样不动）。
import { vi } from 'vitest'

export type PageOptions = Record<string, any>

let captured: PageOptions | null = null

// installPageCapture 必须在 import 页面模块之前调用：Page() 在模块顶层执行
export function installPageCapture() {
  captured = null
  vi.stubGlobal('Page', (opts: PageOptions) => {
    captured = opts
  })
}

// capturedPage 取最近一次注册的页面选项；没注册说明页面模块没被加载（抓错路径）
export function capturedPage(): PageOptions {
  if (!captured) throw new Error('页面未调用 Page() 注册')
  return captured
}

// makeInstance 造页面实例：data 用页面声明的初值（深拷贝，避免用例间串数据），
// 方法绑定到实例上并让 setData 落回 data——与真机行为一致的最小子集。
// 真机 setData 的第二个参数是渲染完成回调，这里同步调用（测试只关心「回调被执行」）。
export function makeInstance(opts: PageOptions) {
  const inst: any = {
    data: JSON.parse(JSON.stringify(opts.data || {})),
    setData(patch: any, cb?: () => void) {
      Object.assign(this.data, patch)
      if (cb) cb()
    },
  }
  for (const k of Object.keys(opts)) {
    if (typeof opts[k] === 'function') inst[k] = opts[k].bind(inst)
  }
  return inst
}

// flush 等一轮 microtask/宏任务：页面里大量 `await request(...)` 与 .then 链，
// 断言前必须放行（setTimeout 0 会让微任务先跑完）。
export function flush() {
  return new Promise((r) => setTimeout(r, 0))
}

type WxStub = Record<string, any>

// installWx 装一个最小可用的 wx 全局。
// storage 用独立 Map 承载，返回给用例断言「登录令牌有没有真的写进去」。
export function installWx(overrides: WxStub = {}) {
  const storage = new Map<string, any>()
  const stub: WxStub = {
    // 微信里取不到 key 返回 ''（空字符串），不是 undefined——页面大量用 !token 判断
    getStorageSync: (k: string) => (storage.has(k) ? storage.get(k) : ''),
    setStorageSync: (k: string, v: any) => void storage.set(k, v),
    removeStorageSync: (k: string) => void storage.delete(k),
    showToast: vi.fn(),
    showModal: vi.fn(),
    showLoading: vi.fn(),
    hideLoading: vi.fn(),
    navigateTo: vi.fn(),
    navigateBack: vi.fn(),
    reLaunch: vi.fn(),
    redirectTo: vi.fn(),
    switchTab: vi.fn(),
    setNavigationBarTitle: vi.fn(),
    // 下拉刷新收尾：内容页的 onPullDownRefresh 一定会调，缺了会直接 TypeError
    stopPullDownRefresh: vi.fn(),
    pageScrollTo: vi.fn(),
    previewImage: vi.fn(),
    chooseMedia: vi.fn(),
    uploadFile: vi.fn(),
    // 存入通讯录：saveCard 调它，属微信隐私接口（未同意时 fail 且 errMsg 含 privacy）
    addPhoneContact: vi.fn(),
    // 隐私授权：requireAuthorize 用它拉起微信官方弹窗。
    // 默认按「用户同意」应答——绝大多数用例只关心「授权后能继续」，
    // 需要验证拒绝的分支自己覆写成 opt.fail({})。
    requirePrivacyAuthorize: vi.fn((o: any) => o && o.success && o.success({})),
    // 剪贴板：setClipboardData 属微信隐私接口，复制类用例会用到
    setClipboardData: vi.fn((o: any) => o && o.success && o.success({})),
    getClipboardData: vi.fn((o: any) => o && o.success && o.success({ data: '' })),
    request: vi.fn(),
    getSystemInfoSync: () => ({ statusBarHeight: 44, pixelRatio: 2 }),
    ...overrides,
  }
  vi.stubGlobal('wx', stub)
  return { wx: stub, storage }
}

// installApp 桩全局 getApp（首页 / 我的页会读 globalData.myStaffId）
export function installApp(globalData: Record<string, any> = {}) {
  const app = { globalData }
  vi.stubGlobal('getApp', () => app)
  return app
}
