import http, { toPaged, type Paged } from './request'

/**
 * 微信绑定的「可维护」层（P1）。
 *
 * 三条底线，改这里之前先读一遍 backend/internal/handler/wechat_admin.go 的文件头：
 *   - **绝不代绑**：后台只能解绑，绑定必须本人在小程序用一次性 code 换取；
 *     所以这个模块里没有 create / bind 之类的函数，也不要加。
 *   - **openid 只进不出**：只有 lookup 的请求体接受完整 openid；
 *     清单 / 反查的回包只有布尔状态与掩码，绝不铺开原文。
 *   - **解绑会踢会话**：后端同时自增 token_version，该账号已签发的所有令牌立即失效；
 *     self=true 表示解的是当前登录账号，前端必须登出重登。
 *
 * 清单 / 解绑两类管理员都可用（后端按身份收窄租户），反查只给平台超管。
 */
export type WechatBindingKind = 'admin' | 'employee'

export interface WechatBindingRow {
  kind: WechatBindingKind
  /** 管理员是 admins.id，员工是 employees.staff_id —— 两张表的主键，别混用 */
  id: number
  /** 管理员用户名 / 员工姓名 */
  name: string
  companyId: number
  companyName: string
  wechatBound: boolean
  /** 脱敏后的 openid，仅用于「大概是哪一个」的肉眼核对 */
  wxOpenidMasked: string
  createdAt: string
}

/** 绑定清单（服务端分页）。只列已绑定账号：这里是「可解绑对象」列表。 */
export function listWechatBindings(q: {
  page: number
  size: number
  keyword: string
  kind?: WechatBindingKind
}): Promise<Paged<WechatBindingRow>> {
  return http.get('/admin/wechat/bindings', { params: q }).then((r) => toPaged<WechatBindingRow>(r))
}

export interface LookupResult {
  code: number
  msg?: string
  /** 命中的账号；回包里没有 openid 原文 */
  data?: WechatBindingRow[]
  total?: number
}

/** 按完整 openid 反查它绑到了哪些账号（平台超管专属，只读但会记审计）。 */
export function lookupWechatBindings(openid: string): Promise<LookupResult> {
  return http.post<LookupResult>('/admin/wechat/lookup', { openid })
}

export interface UnbindResult {
  code: number
  msg?: string
  /** 解的是当前登录账号：安全戳已自增，前端必须登出 */
  self?: boolean
  companyId?: number
}

/** 代解绑。kind + id 定位账号，后端按 scopeCond 校验归属企业。 */
export function unbindWechat(kind: WechatBindingKind, id: number): Promise<UnbindResult> {
  return http.post<UnbindResult>('/admin/wechat/unbind', { kind, id })
}
