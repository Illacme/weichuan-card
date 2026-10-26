// 微信隐私授权（Privacy Authorization）。
//
// 背景：微信自 2023-10-17 起强制——凡调用「隐私接口」（getUserProfile、
// chooseMedia、saveImageToPhotosAlbum、setClipboardData 等）前，必须先取得
// 用户对隐私政策的明示同意，否则真机直接 fail（"require privacy authorize"），
// 且提审必被驳回。
//
// 实现路线（按微信官方《小程序隐私协议开发指南》demo1 的「主动查询」法）：
//
//	① wx.getPrivacySetting() → needAuthorization 表示「用户还没同意过」
//	② 需要同意时，弹出我们自己的授权弹窗，用户点「同意」
//	③ 同意动作必须由 <button open-type="agreePrivacyAuthorization"> 承载
//	   —— 基础库会校验该按钮确实被点击过（buttonId 与被点按钮 id 一致），
//	   用普通按钮 + 自己的状态标记是**无效**的
//	④ 同意后微信侧状态即已同步，后续隐私接口可直接调用
//
// 为什么不用 wx.onNeedPrivacyAuthorization（被动监听）路线：
// 该监听是**覆盖式注册**（重复注册只有最后一次生效），且注册后**必须**调用
// resolve，否则触发事件的原隐私接口会永久挂在 pending（用户看到的是
// 「点了没反应」）。我们有多处隐私接口调用点（选图 / 存相册 / 复制），
// 走主动查询路线可让所有调用点共用同一份状态，且漏网之处由微信官方弹窗兜底
// （官方文档：开发者未响应时微信会主动弹出官方隐私弹窗）。
//
// 状态是一次性的：needAuthorization 为 true 时只要同意一次，
// 之后所有已声明的隐私接口都能直接调用（微信侧记录，不存在"每次都问"）。

// 微信侧隐私接口声明状态（与 app.json 的 __usePrivacyCheck__ 配套）。
interface PrivacySetting {
  needAuthorization: boolean
  privacyContractName: string
}

interface WxPrivacyApi {
  getPrivacySetting?: (opt: any) => void
  openPrivacyContract?: (opt: any) => void
  requirePrivacyAuthorize?: (opt: any) => void
}

// 缓存微信侧返回的协议名称（用于弹窗里展示「《xxx》」）。
let contractName = ''
// 同意状态缓存：微信侧同步后不会再触发 needAuthorization，
// 但我们仍需知道「本会话是否已同意过」——用户拒绝后不应反复弹窗骚扰。
let agreed = false

// 隐私能力是否可用：基础库 < 2.32.3 没有这套接口（wx.getPrivacySetting 不存在）。
// 此时微信不会拦截隐私接口调用（官方文档：低于 2.32.3 未集成隐私相关功能），
// 所以直接视为「无需授权」，不能因此把功能卡死。
function wxPrivacy(): WxPrivacyApi {
  return (typeof wx !== 'undefined' ? (wx as any) : {}) as WxPrivacyApi
}

export function privacySupported(): boolean {
  return typeof wxPrivacy().getPrivacySetting === 'function'
}

// queryPrivacy 查询微信侧的授权状态。
// 返回 agreed=true 表示「无需再问用户」。
// 任何异常（接口不存在 / 调用失败）都按「已同意」处理：
// 拦用户一次功能比放行一次更糟，且旧基础库本就不会拦截。
export function queryPrivacy(): Promise<{ agreed: boolean; contractName: string }> {
  const api = wxPrivacy()
  if (typeof api.getPrivacySetting !== 'function') {
    return Promise.resolve({ agreed: true, contractName: '' })
  }
  return new Promise((resolve) => {
    api.getPrivacySetting!({
      success: (res: any) => {
        contractName = (res && res.privacyContractName) || contractName
        agreed = !(res && res.needAuthorization)
        resolve({ agreed, contractName })
      },
      fail: () => resolve({ agreed: true, contractName }),
    })
  })
}

// needConsent 判断当前是否需要用户明示同意。
// 已同意过（agreed=true）直接返回 false——不重复打扰。
export async function needConsent(): Promise<boolean> {
  if (agreed) return false
  const s = await queryPrivacy()
  return !s.agreed
}

// privacyContractName 取微信侧配置的《小程序用户隐私保护指引》名称。
export function privacyContractName(): string {
  return contractName
}

// markAgreed 标记本会话已同意。
// 由页面在 <button open-type="agreePrivacyAuthorization"> 的
// bindagreeprivacyauthorization 回调里调用——该事件触发即代表用户点了同意。
export function markAgreed(): void {
  agreed = true
}

// openContract 打开微信侧的隐私协议页（用户想看完整条款时的出口）。
// 未配置或基础库不支持时静默失败：这是补充出口，不该因此报错。
export function openContract(): void {
  const api = wxPrivacy()
  if (typeof api.openPrivacyContract !== 'function') return
  try {
    api.openPrivacyContract!({ fail: () => {} })
  } catch {
    // 忽略
  }
}

// ---- 页面侧编排：确保拿到授权后再继续 ----

// 待决的授权请求：同一时刻只应有一个弹窗在等用户选择。
// 多个隐私接口同时触发时（如用户连点两次「上传头像」），
// 后到的请求复用同一个 Promise，不会弹两个叠在一起的弹窗。
let pendingResolve: ((ok: boolean) => void) | null = null

// requestConsent 等待用户的授权选择。
//
// 返回 true 表示可以继续调用隐私接口；false 表示用户不同意，调用方应放弃本次操作
// （例如不再打开选图器、提示「需同意隐私政策才能继续」）。
//
// 关键点：**用户不同意时也要 resolve**，不能让它悬着——
// 调用方在 await 这里等着，不 resolve 就等于把这次点击吞了（按钮转圈、什么也不发生）。
export function requestConsent(): Promise<boolean> {
  if (pendingResolve) {
    // 已有弹窗在等：复用它的结果，不重复弹窗
    return new Promise<boolean>((resolve) => {
      const prev = pendingResolve!
      pendingResolve = (ok: boolean) => {
        prev(ok)
        resolve(ok)
      }
    })
  }
  return new Promise<boolean>((resolve) => {
    pendingResolve = resolve
  })
}

// settle 结束一次授权请求（用户点了同意或不同意）。
// 由 privacy-consent 组件在用户做出选择时调用。
export function settle(agreed: boolean): void {
  const r = pendingResolve
  pendingResolve = null
  if (r) r(agreed)
}

// ensurePrivacy 页面侧的统一入口：确保已获授权，未获授权则弹窗等待用户选择。
//
// 返回 true 表示可以继续调用隐私接口。
//
// 页面的用法（三步，缺一不可）：
//   1. data 里放 privacyVisible: false
//   2. wxml 里挂 <privacy-consent visible="{{privacyVisible}}" />
//   3. 调隐私接口前：if (!(await ensurePrivacy(this))) return
//
// 找不到弹窗组件时不阻塞调用方（页面漏挂组件是编码错误，不能变成
// 「用户点了没反应」）：按已同意放行，交由微信官方弹窗兜底。
export async function ensurePrivacy(page: any): Promise<boolean> {
  if (!(await needConsent())) return true
  // 页面没挂 privacy-consent 组件 → 无法自绘弹窗，走官方兜底
  const comp = page && page.selectComponent ? page.selectComponent('#privacy-consent') : null
  if (!comp) {
    markAgreed()
    return true
  }
  page.setData({ privacyVisible: true })
  const ok = await requestConsent()
  page.setData({ privacyVisible: false })
  return ok
}

// resetConsent 清空本会话的同意标记。
// 仅供测试使用：单测之间要能重新走一遍「未同意」的分支。
export function resetConsent(): void {
  agreed = false
  contractName = ''
  pendingResolve = null
}


