import http from './request'

// 租户自有对象桶配置（平台超管专属，迁移 0044 / 设计稿 §1.1 §3.2）。
//
// 这条配置里装的是**客户的云凭据**：企业管理员一旦能改，就能把自己企业的对象
// 写到任意桶、把别人的桶指到自己账号下，而平台从业务侧察觉不到。
// 所以四个接口后端全挂 PlatformAdminMiddleware，企业管理员连读都拿不到（403）。
// 前端这边是 experience 层：页面挂在「企业管理」里，本身只有平台超管能进。
//
// 一句话说清本期边界：**配置写进去了，但还不承载流量**。
// 上传与出参仍走平台那一个实例，要等按租户路由（P2）落地后才真正按桶分流。
// 界面上必须显示这句（trafficEnabled=false），否则超管填完会以为已经生效。

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
  /** 本期恒为 false：配置尚未参与上传路由 */
  trafficEnabled: boolean
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
