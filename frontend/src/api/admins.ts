import http, { toPaged, type Paged } from './request'

// 平台超管账号管理。与「企业管理 → 企业管理员」是两套：
// 这里只管 company_id=0 的平台超管账号，企业管理员仍归 api/tenants。
export interface PlatformAdmin {
  id: number
  username: string
  createdAt: string
  /** 是否绑定了小程序端免密登录用的微信。后端只回布尔与掩码，绝不回 openid 原文 */
  wechatBound?: boolean
  wxOpenidMasked?: string
}

// 平台超管列表（服务端分页）
export function listPlatformAdmins(params: { page?: number; size?: number } = {}): Promise<
  Paged<PlatformAdmin>
> {
  return http.get('/admin/platform-admins', { params }).then((r) => toPaged<PlatformAdmin>(r))
}

// 新增平台超管（用户名全表唯一，含企业管理员）
export function createPlatformAdmin(username: string, password: string) {
  return http.post<{ code: number; data?: { id: number; username: string } }>(
    '/admin/platform-admins',
    { username, password }
  )
}

// 改用户名。self=true 表示改的是当前登录账号（前端据此刷新顶栏展示）。
// 令牌里不含用户名，改名不会踢掉会话。
export function renamePlatformAdmin(id: number, username: string) {
  return http.put<{ code: number; self?: boolean }>(`/admin/platform-admins/${id}`, {
    username,
  })
}

// 重置密码（不需要原密码，用于给别人重置）。安全戳自增 → 对方所有会话立即失效。
// self=true 表示重置的是自己，前端应登出重登。
export function resetPlatformAdminPassword(id: number, password: string) {
  return http.post<{ code: number; self?: boolean }>(
    `/admin/platform-admins/${id}/password`,
    { password }
  )
}

// 删除平台超管。后端护栏：不能删自己、至少保留一个。
export function deletePlatformAdmin(id: number) {
  return http.delete<{ code: number }>(`/admin/platform-admins/${id}`)
}
