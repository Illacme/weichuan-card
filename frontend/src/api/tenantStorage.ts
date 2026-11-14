import http from './request'

// 租户自有对象桶配置（平台超管专属，迁移 0044 / 设计稿 §1.1 §3.2）。
//
// 这条配置里装的是**客户的云凭据**：企业管理员一旦能改，就能把自己企业的对象
// 写到任意桶、把别人的桶指到自己账号下，而平台从业务侧察觉不到。
// 所以四个接口后端全挂 PlatformAdminMiddleware，企业管理员连读都拿不到（403）。
// 前端这边是 experience 层：页面挂在「企业管理」里，本身只有平台超管能进。
//
// 一句话说清边界：**配置能不能生效由后端的 TENANT_STORAGE_ROUTE 决定**。
// 出参 `trafficEnabled` 就是这个开关的实况（后端读环境变量后原样回报），
// 界面按它切换顶部提示——不要在前端写死「尚未生效」，那句话会过期。

export type StorageDriver = 'local' | 'minio' | 'oss' | 's3'
export type StorageStatus = 'unknown' | 'ok' | 'error'

export interface TenantStorageConfig {
  companyId: number
  /** 是否已配置。false 时不代表出错，只代表这家企业走平台存储 */
  configured: boolean
  driver: StorageDriver
  /** host[:port]，**不带 scheme**（它同时喂给 nginx snippet） */
  endpoint: string
  bucket: string
  region: string
  useSsl: boolean
  /** virtual-hosted-style 的桶名在这里，也是 TLS SNI 取值；留空则退回 endpoint */
  hostHeader: string
  publicRead: boolean
  enabled: boolean
  /** 客户自有访问域名（纯 origin，如 https://img.cust.com）；空串 = 走平台统一入口 */
  publicBaseUrl: string
  /**
   * 自有域名此刻是否真的改变了出参 URL。第六十八轮**恒为 false**：
   * 出参侧按租户选 origin（storage.ResolveRef 的 origin 参数）尚未接入，
   * 这一列目前只做登记 + 校验 + 如实回报。界面绝不许按 true 渲染成「已切换」。
   */
  publicBaseUrlActive: boolean
  /** 非空时是可直接展示给超管的「为什么没生效 / 生效前还要做什么」 */
  publicBaseUrlNote: string
  /** 连通性自检结果。unknown=配了但没验过，不要渲染成「正常」 */
  status: StorageStatus
  lastError: string
  lastCheckedAt: string | null
  updatedAt: string | null
  accessKeyConfigured: boolean
  secretKeyConfigured: boolean
  /** 前4****后4；仅为让运维核对「生效的是不是新那把」，永不回明文 */
  accessKeyMasked: string
  /** 非空说明库里的密文解不开（PII_ENC_KEY 换过），需重新填一次 */
  credentialError: string
  /** 本进程是否开启了租户存储路由（TENANT_STORAGE_ROUTE）。进程级，不是按企业 */
  trafficEnabled: boolean
  /**
   * 这家企业此刻的文件是否真的写进自己的桶。
   * 与 `trafficEnabled` **不是一回事**：开关开着但这家的 enabled=false 时它仍是 false。
   */
  activeForTenant: boolean
  /** activeForTenant=false 时的原因，可直接展示给超管（五种成因处置方式不同） */
  inactiveReason: string
}

export interface TenantStorageSavePayload {
  driver: StorageDriver
  endpoint: string
  bucket: string
  region?: string
  useSsl?: boolean
  hostHeader?: string
  publicRead?: boolean
  enabled?: boolean
  /**
   * 客户自有访问域名，纯 origin（http(s)://host[:port]），不要带路径 / 查询。
   * **不传** = 保留原值、空串 = 清空、有值 = 覆盖（服务端归一化后落库）。
   * 与凭据一样是三态：界面「只看不改点保存」时提交的是表单里回显的原值，
   * 不是空串——否则一次改 bucket 的操作会悄悄清掉已登记的域名。
   * 目前只登记，不会改变出参 URL，见 TenantStorageConfig.publicBaseUrlActive。
   */
  publicBaseUrl?: string
  /**
   * 凭据三态：**不传** = 保留原值、空串 = 清空、有值 = 覆盖。
   * 界面回显的是掩码，所以「只看不改点保存」必须走「不传」这一态——
   * 否则一次改 bucket 的操作会把凭据抹掉，而接口照样回成功。
   */
  accessKey?: string
  secretKey?: string
}

export interface TenantStorageCheckResult {
  ok: boolean
  detail: string
  error: string
  status: StorageStatus
  lastCheckedAt: string
  /** 这次自检验了什么、没验什么（不含 nginx 侧 Host/SNI、CDN、证书链） */
  checkedScope: string
}

export function getTenantStorage(companyId: number) {
  return http
    .get(`/admin/companies/${companyId}/storage`)
    .then((r: any) => (r.data ?? {}) as TenantStorageConfig)
}

export function saveTenantStorage(companyId: number, p: TenantStorageSavePayload) {
  return http
    .put(`/admin/companies/${companyId}/storage`, p)
    .then((r: any) => (r.data ?? {}) as TenantStorageConfig)
}

/** 切回平台存储：删掉这一行。已上传的对象仍留在客户桶里，不会被搬回来。 */
export function resetTenantStorage(companyId: number) {
  return http.delete(`/admin/companies/${companyId}/storage`)
}

/**
 * 连通性自检。带凭据去探桶（HEAD bucket），因此能分辨「凭据被拒」与「桶不存在」——
 * 不带签名的探测对 S3 没有判别力（存在但无权访问与不存在都回 403）。
 */
export function checkTenantStorage(companyId: number) {
  return http
    .post(`/admin/companies/${companyId}/storage/check`)
    .then((r: any) => (r.data ?? {}) as TenantStorageCheckResult)
}
