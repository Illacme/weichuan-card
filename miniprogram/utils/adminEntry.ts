// 企业管理员入口：决定「点待审申请」落在哪个页面，以及什么时候允许静默免密。
//
// 为什么单独抽这一层（2026-11-07 真机反馈）：
//   旧实现是「先 navigateTo 审核页 → 审核页 onShow 异步发现没令牌 → 自动发一次
//   /api/admin/login/wechat → 失败再 redirectTo 登录页」。三个后果：
//   ① 审核页 WXML 已经把「待处理」列表画出来了，真机上先闪一下再被踢走；
//   ② 每次进页面都消耗 login:<IP> 限流桶（和账号密码登录共用，默认 10 次/分），
//      自动请求把额度挤没，用户真正点「用当前微信免密进入」时反而收到 429；
//   ③ 员工微信登录态（empToken）与管理员身份（adminToken）被混为一谈，
//      「我在『我的』里已经微信登录了」并不等于「这个微信绑了管理员」。
//
//   现在：入口同步判定目标页；静默免密只做一次探测（首次）或只在「本机确实绑过
//   管理员微信 + 过了冷却窗口」时做，失败不弹窗；审核页自己绝不发登录请求。
import {
  adminRequest,
  saveAdminAuth,
  hasAdminToken,
  clearAdminAuth,
  markAdminWxBound,
  hasAdminWxBound,
  clearAdminWxBound,
} from './adminRequest'
import { wxLoginCode } from './wechat'
import { getCompanyId } from './tenant'

export const REVIEW_PAGE = '/pages/admin/apply/apply'
export const LOGIN_PAGE = '/pages/admin/login/login'

const SILENT_TRY_KEY = 'adminSilentLoginAt'
// 与后端登录限流窗口对齐（60s）：本机每分钟最多一次自动尝试，
// 页面反复 onShow / 用户连点都不会连发。共享出口 IP 下也不会被本机打满。
export const SILENT_COOLDOWN_MS = 60 * 1000

// canTrySilentLogin 的判据（两阶段）：
//  1) 从未探测过（last=0）：给**一次**「这个微信是不是管理员」的机会。
//     这是必须的——「本机绑过管理员」这个标记是本版才引入的，老用户升级后第一次
//     点入口标记必然为空；若直接当硬门槛，明明微信绑着管理员也会被挡在登录页。
//  2) 探测过之后：只有确实绑过管理员微信的设备才继续静默免密，且要过 60s 冷却。
//     探测失败（纯访客 / 只登过员工 / 429）的设备从此不再发登录请求，
//     不会再出现「我一下都没登，却提示登录尝试过于频繁」。
// 主动退出登录会把 last 写成当前时刻（而不是清成 0）：否则会被当成「从未探测」重新放行。
export function canTrySilentLogin(now = Date.now()): boolean {
  if (hasAdminToken()) return false
  const last = Number(wx.getStorageSync(SILENT_TRY_KEY)) || 0
  if (last === 0) return true
  if (!hasAdminWxBound()) return false
  return now - last >= SILENT_COOLDOWN_MS
}

export function clearSilentLoginCooldown(): void {
  wx.removeStorageSync(SILENT_TRY_KEY)
}

// trySilentAdminLogin 是一次「猜测」，猜错（未绑定 / 429 / 断网）就安静地返回 false，
// 绝不弹窗——用户没主动发起登录，不该被一句「登录失败」拦在半路。
export async function trySilentAdminLogin(now = Date.now()): Promise<boolean> {
  // 先记账再发请求：失败的尝试也要进冷却，否则 429 时会被反复重试。
  wx.setStorageSync(SILENT_TRY_KEY, now)
  try {
    const code = await wxLoginCode()
    if (!code) return false
    const r = await adminRequest('POST', '/api/admin/login/wechat', {
      code,
      companyId: getCompanyId(),
    })
    if (r.code !== 0) return false
    if (!saveAdminAuth(r as any)) return false
    markAdminWxBound()
    return true
  } catch {
    return false
  }
}

// endAdminSession 只给「用户主动退出」用，两件事必须同时成立：
//  · 401 不许调它：令牌过期不代表微信解绑，静默免密仍然有效，清掉绑定标记会把人
//    逼回「每次都要输密码」。
//  · last 写成当前时刻而不是清成 0：清成 0 会被 canTrySilentLogin 当成「从未探测」，
//    于是刚点完退出就又被静默免密带回审核页，退出按钮等于失效。
export function endAdminSession(): void {
  clearAdminAuth()
  clearAdminWxBound()
  wx.setStorageSync(SILENT_TRY_KEY, Date.now())
}

// goAdminArea 是「我的」页唯一入口：目标页在导航前就确定，绝不先打开审核页再异步踢人。
export async function goAdminArea(): Promise<void> {
  if (hasAdminToken()) {
    wx.navigateTo({ url: REVIEW_PAGE })
    return
  }
  if (!canTrySilentLogin()) {
    wx.navigateTo({ url: LOGIN_PAGE })
    return
  }
  wx.showLoading({ title: '正在验证管理员身份', mask: true })
  let ok = false
  try {
    ok = await trySilentAdminLogin()
  } finally {
    wx.hideLoading()
  }
  wx.navigateTo({ url: ok ? REVIEW_PAGE : LOGIN_PAGE })
}
