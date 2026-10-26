import http, { toPaged, type Paged } from './request'

// 企业管理（多租户第一期，仅平台超级管理员可用）：
// 开通/停用企业、企业管理员管理。企业管理员调用会被后端 403 拒绝。
// 列表走服务端分页（page/size/keyword/status），见 api/request.ts 的 Paged 约定。

export interface CompanyItem {
  id: number
  name: string
  shortName: string
  status: 'enabled' | 'disabled'
  createdAt: string
  employeeCount: number
  adminCount: number
  /** 该企业是否已配好独立小程序凭据（AppID + AppSecret 成对非空）。
   *  false 时企业员工生成海报码会回落到平台全局小程序——需要平台代填。 */
  wxConfigured?: boolean
  /** 已用存储（字节）。注意口径：**自本功能启用起累计的上传量**，
   *  不是磁盘真实占用（历史文件无法可靠归属租户，见后端 model.StorageUsage）。 */
  storageBytes?: number
  /** 是否有授权行。false = 未配置 = 不限制（存量企业升级后就是这种）。 */
  licenseExists?: boolean
  /** 版本标识：trial / platform / private，空串 = 未设置 */
  licenseEdition?: string
  /** 员工席位上限；0 = 不限制 */
  licenseSeats?: number
  /** 到期日 YYYY-MM-DD；空串 = 不过期 */
  licenseExpiresAt?: string
  /** 是否已过期（过期后该企业后台只读：能登录看，不能改） */
  licenseExpired?: boolean
}

export interface CompanyWXApp {
  /** 明文 AppID（只读回传，供界面展示当前值） */
  appId: string
  /** 是否成配好。false 包含两种情况：从没配过、只填了 AppID 而 Secret 被清空 */
  configured: boolean
  /** 该企业是否允许平台代发小程序版本。企业关掉后，后台一切提审/发布/撤回都会被拒 */
  releaseEnabled?: boolean
  /** 是否配了小程序「代码上传密钥」。与 AppSecret 是两把不同的钥匙：
   *  AppSecret 只能调提审/发布接口，上传代码只认这把。永不明文回传 */
  uploadKeyConfigured?: boolean
}

export interface CompanyAdminItem {
  id: number
  username: string
  createdAt: string
}

// 企业列表（含员工/管理员数量），服务端分页 + 关键字（名称/简称）+ 状态筛选
export function listCompanies(params: {
  page?: number
  size?: number
  keyword?: string
  status?: string
} = {}): Promise<Paged<CompanyItem>> {
  return http.get('/admin/companies', { params }).then((r) => toPaged<CompanyItem>(r))
}

// 开通企业：name 必填，shortName 为企业简称（侧边栏品牌名 + 公司信息简称初始值）；
// adminUsername + adminPassword 要么都填（一并建企业管理员），要么都不填
export function createCompany(payload: {
  name: string
  shortName?: string
  adminUsername?: string
  adminPassword?: string
}) {
  return http.post<{ code: number; data?: { id: number; name: string; shortName: string } }>(
    '/admin/companies',
    payload
  )
}

// 编辑企业（名称 / 简称）：简称改后同步侧边栏品牌名，但不回写公司信息里的简称
// （公司信息简称仅用于小程序展示，由企业管理员在「公司信息」页自行维护）
export function updateCompany(id: number, payload: { name: string; shortName?: string }) {
  return http.put(`/admin/companies/${id}`, payload)
}

// 停用 / 启用企业（停用后该企业的管理员与员工均不能登录）
export function setCompanyStatus(id: number, status: 'enabled' | 'disabled') {
  return http.post(`/admin/companies/${id}/status`, { status })
}

// 企业管理员列表：单个企业下的管理员数量天然有界（几个），不做分页，
// 保持返回数组——分页只为「会随时间增长」的列表服务，别为了统一而统一。
export function listCompanyAdmins(id: number) {
  return http.get<{ code: number; data?: CompanyAdminItem[] }>(`/admin/companies/${id}/admins`)
}

// 新增企业管理员
export function createCompanyAdmin(id: number, username: string, password: string) {
  return http.post(`/admin/companies/${id}/admins`, { username, password })
}

// ===== 租户小程序凭据（平台超管代填）=====
// 小程序有两种形态：平台模式一个 appid 服务所有企业（用 sys_setting 的全局那份）；
// 企业独立版每企业一个 appid，共用同一套后端，必须按租户隔离——不隔离的话
// 企业员工生成的小程序码指向的是平台小程序，扫进去跳到别人的名片。
// AppSecret 永不回传（与 sys_setting.wx_appsecret 同策略），configured 只表示「成对且非空」。
export function getCompanyWXApp(id: number) {
  return http.get<{ code: number; data?: CompanyWXApp }>(`/admin/companies/${id}/wechat`)
}

// 保存租户小程序凭据。
// 注意后端语义：appSecret 空串 = 清空该企业凭据（回落到平台全局小程序）。
// 所以「只想改 AppID、Secret 不动」是做不到的，界面必须把这点写清楚，
// 否则运营顺手一保存就把 Secret 清了，企业第二天就来问码怎么不对。
// releaseEnabled 是「允许平台代发版本」开关：企业独立版的小程序归企业自己管，
// 关掉后后台登记/提审/发布/撤回全部 403。不传 = 保持原值（后端用指针区分
// 「没传」与「传了 false」，所以只改 AppID 的保存不会顺带关掉企业权限）。
// uploadKey 是小程序「代码上传密钥」（.key 文件内容），语义同 appSecret：
// 空串 = 清空并落回平台全局那把。它与 AppSecret 不是同一把钥匙——
// 上传代码只认它，见 api/mpRelease.ts 的 uploadMPRelease。
export function saveCompanyWXApp(
  id: number,
  payload: { appId: string; appSecret: string; releaseEnabled?: boolean; uploadKey?: string }
) {
  return http.put(`/admin/companies/${id}/wechat`, payload)
}

// ===== 租户授权（B4：试用 → 转正 / 延期 / 改席位）=====
// 平台模式下开通企业会自动写一条试用授权（席位 + 到期日，口径见系统设置 → 授权）。
// 到期后该企业后台自动降级只读——**降权是自动的，恢复不是**，所以必须有这组接口，
// 否则"续费"要落到人工改数据库，收钱闭环断在一半。
export interface CompanyLicense {
  companyId: number
  /** 是否已有授权行。false = 未配置 = 不限制（存量企业） */
  exists: boolean
  edition: string
  /** 员工席位上限；0 = 不限制 */
  seats: number
  /** 到期日 YYYY-MM-DD；空串 = 不过期 */
  expiresAt: string
  features: string
  expired: boolean
  employeeCount: number
  /** 当前试用口径：界面提示"新开通的企业会拿到 N 席 / M 天" */
  trial: { enabled: boolean; days: number; seats: number }
}

export function getCompanyLicense(id: number) {
  return http.get<{ code: number; data?: CompanyLicense }>(`/admin/companies/${id}/license`)
}

// seats=0 表示不限席位，expiresAt 留空表示不过期——两者都是正式客户的常态，
// 后端用 map 写库以保证「清空到期」真的落成 NULL（结构体零值会被写成 0000-00-00）。
export function saveCompanyLicense(
  id: number,
  payload: { edition: string; seats: number; expiresAt: string; features: string }
) {
  return http.put(`/admin/companies/${id}/license`, payload)
}

// ===== 租户存储用量（B3）=====
export interface StorageUsageInfo {
  companyId: number
  bytes: number
  files: number
  quotaBytes: number
  /** 该租户的配额覆盖（MB）；0 = 未单独设置，跟随全局 */
  quotaMb: number
  percent: number
  humanBytes: string
  humanQuota: string
  unlimited: boolean
  quotaGlobal: number
}

// 查某企业的用量与配额。不传 companyId 时后端按当前登录身份取（企业管理员即本企业）。
export function getStorageUsage(companyId?: number) {
  return http.get<{ code: number; data?: StorageUsageInfo }>('/admin/storage', {
    params: companyId ? { companyId } : {},
  })
}

// 设置某企业的配额覆盖（MB）。0 = 跟随全局设置。仅平台超管可用。
export function saveStorageQuota(companyId: number, quotaMb: number) {
  return http.put('/admin/storage/quota', { companyId, quotaMb })
}
