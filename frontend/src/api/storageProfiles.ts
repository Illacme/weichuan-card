import http from './request'

/** 存储档案（迁移 0054）：连接信息只存一份，绑定它的企业共享。 */
export interface StorageProfile {
  id: number
  name: string
  driver: string
  endpoint: string
  bucket: string
  region: string
  baseUrl: string
  /** S3 路径风格由下面的 hostHeader 单点推导，不单独配置（0058 已删该列）。 */
  hostHeader: string
  useSsl: boolean
  localDir: string
  publicRead: boolean
  status: string
  lastError: string
  lastProbeAt: string | null
  version: number
  builtin: boolean
  /** 内置档案用途：platform_primary=平台自身存储，tenant_fallback=租户退路，platform_mirror=平台镜像；普通档案为空。 */
  purpose?: '' | 'platform_primary' | 'tenant_fallback' | 'platform_mirror'
  accessKeyConfigured: boolean
  secretKeyConfigured: boolean
  accessKeyMasked: string
}

export interface DeliveryProfile {
  id: number
  name: string
  provider: string
  domain: string
  pathPrefix: string
  endpoint: string
  resourceId: string
  status: string
  lastError: string
  version: number
  /** 内置档案：启动时生成，不可删除、不可绑定给企业（迁移 0060） */
  builtin?: boolean
  /** platform_primary = 平台自身 CDN：只服务 p/ 平台对象，域名不在这里配 */
  purpose?: string
  accessKeyConfigured: boolean
  secretKeyConfigured: boolean
  accessKeyMasked: string
}

export interface StorageBinding {
  tenantId: number
  storageProfileId: number
  storageProfileName: string
  deliveryProfileId: number
  deliveryProfileName: string
  /** 切换前的那一套档案，供一键回滚。0=从未切换过（不可回滚）。 */
  previousStorageProfileId: number
  previousDeliveryProfileId: number
  version: number
  updatedBy: string
  updatedAt: string
  source: string
}

export interface MigrationJob {
  id: number
  tenantId: number
  sourceProfileId: number
  targetProfileId: number
  status: string
  total: number
  done: number
  failed: number
  lastKey: string
  dryRun: boolean
  autoSwitch: boolean
  createdBy: string
  error: string
  createdAt: string
  finishedAt: string | null
}

export interface MigrationError {
  id: number
  jobId: number
  objectKey: string
  error: string
  createdAt: string
}

const unwrap = <T>(r: any): T => (r.data ?? {}) as T

export function listStorageProfiles() {
  return http.get('/admin/storage-profiles').then((r: any) => unwrap<StorageProfile[]>(r))
}
export function saveStorageProfile(id: number | null, p: Partial<StorageProfile> & { accessKey?: string; secretKey?: string }) {
  return http[id ? 'put' : 'post'](`/admin/storage-profiles${id ? `/${id}` : ''}`, p).then((r: any) => unwrap<StorageProfile>(r))
}
export function deleteStorageProfile(id: number) {
  return http.delete(`/admin/storage-profiles/${id}`)
}
export function checkStorageProfile(id: number) {
  return http.post(`/admin/storage-profiles/${id}/check`).then((r: any) => unwrap<{ ok: boolean; detail: string; error: string; checkedScope: string }>(r))
}

export function listDeliveryProfiles() {
  return http.get('/admin/delivery-profiles').then((r: any) => unwrap<DeliveryProfile[]>(r))
}
export function saveDeliveryProfile(id: number | null, p: Partial<DeliveryProfile> & { accessKey?: string; secretKey?: string }) {
  return http[id ? 'put' : 'post'](`/admin/delivery-profiles${id ? `/${id}` : ''}`, p).then((r: any) => unwrap<DeliveryProfile>(r))
}
export function deleteDeliveryProfile(id: number) {
  return http.delete(`/admin/delivery-profiles/${id}`)
}
export function checkDeliveryProfile(id: number) {
  return http.post(`/admin/delivery-profiles/${id}/check`).then((r: any) => unwrap<{ ok: boolean; detail: string; error: string; checkedScope: string }>(r))
}

export function listStorageBindings() {
  return http.get('/admin/storage-bindings').then((r: any) =>
    unwrap<{
      global: StorageBinding | { bound: boolean; source: string; note: string }
      companies: StorageBinding[]
      storageProfiles: Record<string, string>
      deliveryProfiles: Record<string, string>
    }>(r)
  )
}
export function saveGlobalBinding(storageProfileId: number, deliveryProfileId: number, force = false) {
  return http.put('/admin/storage-bindings/global', { storageProfileId, deliveryProfileId, force })
}
export function saveCompanyBinding(companyId: number, storageProfileId: number, deliveryProfileId: number, force = false) {
  return http.put(`/admin/storage-bindings/companies/${companyId}`, { storageProfileId, deliveryProfileId, force })
}
export function resetCompanyBinding(companyId: number) {
  return http.delete(`/admin/storage-bindings/companies/${companyId}`)
}
/** 一键回滚到切换前的档案（交换语义，可反复回滚来回切）。 */
export function rollbackGlobalBinding() {
  return http.post('/admin/storage-bindings/global/rollback')
}
export function rollbackCompanyBinding(companyId: number) {
  return http.post(`/admin/storage-bindings/companies/${companyId}/rollback`)
}

export function listMigrationJobs() {
  return http.get('/admin/storage-migration-jobs').then((r: any) => unwrap<MigrationJob[]>(r))
}
export function createMigrationJob(p: {
  tenantId: number
  sourceProfileId: number
  targetProfileId: number
  dryRun: boolean
  autoSwitch: boolean
}) {
  return http.post('/admin/storage-migration-jobs', p).then((r: any) => unwrap<MigrationJob>(r))
}
export function runMigrationJob(id: number) {
  return http.post(`/admin/storage-migration-jobs/${id}/run`)
}
export function cancelMigrationJob(id: number) {
  return http.post(`/admin/storage-migration-jobs/${id}/cancel`)
}
export function listMigrationErrors(id: number) {
  return http.get(`/admin/storage-migration-jobs/${id}/errors`).then((r: any) => unwrap<MigrationError[]>(r))
}
