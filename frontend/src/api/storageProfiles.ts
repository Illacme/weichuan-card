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
  pathStyle: boolean
  hostHeader: string
  useSsl: boolean
  localDir: string
  publicRead: boolean
  status: string
  lastError: string
  lastProbeAt: string | null
  version: number
  builtin: boolean
  /** 内置档案用途（迁移 0056）：tenant_fallback=租户退路，platform_mirror=平台镜像；普通档案为空。 */
  purpose?: '' | 'tenant_fallback' | 'platform_mirror'
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
export function saveGlobalBinding(storageProfileId: number, deliveryProfileId: number) {
  return http.put('/admin/storage-bindings/global', { storageProfileId, deliveryProfileId })
}
export function saveCompanyBinding(companyId: number, storageProfileId: number, deliveryProfileId: number) {
  return http.put(`/admin/storage-bindings/companies/${companyId}`, { storageProfileId, deliveryProfileId })
}
export function resetCompanyBinding(companyId: number) {
  return http.delete(`/admin/storage-bindings/companies/${companyId}`)
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
