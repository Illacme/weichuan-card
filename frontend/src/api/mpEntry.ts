import http from './request'
import { listEmployees } from './employees'

// 小程序「默认进入」设置（0034）：裸启动落到名片墙还是某个员工的名片。
//
// 裸启动 = 扫微信「体验版二维码」/ 直接打开小程序，微信不带任何参数，
// 端上只能靠服务端告诉它该去哪个页面。所以这一项直接决定**所有访客**的第一眼。

export type MpEntryMode = 'wall' | 'staff'

export interface MpEntry {
  /** 配置原值：管理员设的（wall / staff） */
  mode: MpEntryMode
  /** mode=staff 时指定的员工；0 表示未指定 */
  staffId: number
  /**
   * 实际生效值。与 mode 不一致说明配置被服务端回落了——最常见的原因是
   * 指定员工已停用。界面必须把这件事说出来，否则管理员会以为设置生效了。
   */
  effectiveMode: MpEntryMode
  staffName: string
  staffTitle: string
  staffAvatar: string
  /** 指定员工的当前状态（enabled / disabled）；空串=未指定 */
  staffStatus: string
  /** 指定的员工记录已不存在（被删除），与「停用」分开提示 */
  staffMissing: boolean
}

export function getMpEntry() {
  return http.get('/admin/mp-entry').then((r: any) => (r.data ?? {}) as MpEntry)
}

// mode=staff 时 staffId 必填，且必须是本企业启用中的员工（服务端校验）
export function saveMpEntry(p: { mode: MpEntryMode; staffId?: number }) {
  return http.put('/admin/mp-entry', p)
}

// 员工候选：只列启用中的（停用员工不能作为入口），按关键字远程搜索。
// size 取 20：下拉里再多也翻不动，关键字才是主要检索方式。
export function searchEntryStaff(keyword: string) {
  return listEmployees({ keyword, status: 'enabled', size: 20 }).then((p) => p.list as any[])
}
