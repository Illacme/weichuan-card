import http from './request'

// ===== 公司信息（id=1 单行，GET 空值兜底，PUT 自动建首行） =====
export function getCompanyInfo() {
  return http.get('/admin/company-info').then((r: any) => (r.data ?? { id: 1 }) as any)
}
export function saveCompanyInfo(p: {
  fullName: string
  shortName: string
  address: string
  tel: string
  logo: string
}) {
  return http.put('/admin/company-info', p).then((r: any) => r.data as any)
}

// ===== 公司简介 + 视频（多来源） =====
// videoSource 五档：none / channels（视频号）/ upload（云存储上传）/ url（外部直链）/ tencent（预留）
// videoUrl 由 upload 与 url 两档共用，并作为 channels / tencent 开启 videoFallbackUrl 时的兜底源。
// videoBackupUrl 是 upload / url 两档主 MP4 失败时的备用地址，独立一列，不与 videoUrl 共用。
// channels 档：feedId（同主体，可自动播放）与 feedToken（跨主体，强制不能自动播放）二选一，feedId 优先。
export function getCompanyProfile() {
  return http.get('/admin/company-profile').then((r: any) => (r.data ?? { id: 1 }) as any)
}
export function saveCompanyProfile(p: {
  introText: string
  videoSource: string
  videoChannelId: string
  videoFeedId: string
  videoFeedToken: string
  videoVid: string
  videoUrl: string
  videoFallbackUrl: boolean
  // 自托管 MP4 档的备用地址（主源加载失败时切换）；其余档位传空串即可
  videoBackupUrl: string
  // 腾讯视频插件 autoplay 默认 true（与原生 video 相反），必须显式下发
  videoAutoplay: boolean
  cover: string
}) {
  return http.put('/admin/company-profile', p).then((r: any) => r.data as any)
}

// 地址可达性探测：服务端发一次**受限** HEAD（协议白名单 / 私网拦截 / 禁重定向 /
// 超时 / 不读响应体，见后端 internal/urlprobe），用于排查「填了 CDN 地址但小程序黑屏」。
// __silent：不管通不通都由按钮旁的行内结果自己说明，别再弹一条全局红条。
// 注意它只回答「应用服务器能不能取到」，不等于「用户手机一定能播」——真机仍要验。
export interface UrlProbeResult {
  reachable: boolean
  status: number
  contentType: string
  contentLength: number
  message: string
}
export function probeVideoUrl(url: string): Promise<UrlProbeResult> {
  return http
    .post('/admin/url-probe', { url }, { __silent: true } as any)
    .then((r: any) => r.data as UrlProbeResult)
}

// 存储能力探测：决定「云存储上传」档能不能选。
// videoUpload=false 时界面应禁用该档并展示 reason（本地磁盘存储 / 不支持预签名）。
export interface UploadCapability {
  driver: string
  presign: boolean
  videoUpload: boolean
  videoMaxMb: number
  reason: string
}
const CAPABILITY_FALLBACK: UploadCapability = {
  driver: 'unknown',
  presign: false,
  videoUpload: false,
  videoMaxMb: 100,
  reason: '存储能力探测失败，已按「不可用」处理',
}
export function getUploadCapability(): Promise<UploadCapability> {
  return http
    .get('/admin/upload/capability')
    .then((r: any) => ({ ...CAPABILITY_FALLBACK, ...(r.data ?? {}) }) as UploadCapability)
    .catch(() => CAPABILITY_FALLBACK)
}

// 视频来源能力：决定「腾讯视频」档能不能选。
// 该档依赖小程序侧静态声明腾讯视频插件（未授权会让整个小程序编译失败），
// 所以开关在部署侧（MP_TENCENT_VIDEO），这里只负责把「能不能选 + 为什么」搬给界面。
export interface VideoCapability {
  tencentVideo: boolean
  tencentReason: string
}
const VIDEO_CAP_FALLBACK: VideoCapability = { tencentVideo: false, tencentReason: '' }
export function getVideoCapability(): Promise<VideoCapability> {
  return http
    .get('/admin/video-capability')
    .then((r: any) => ({ ...VIDEO_CAP_FALLBACK, ...(r.data ?? {}) }) as VideoCapability)
    .catch(() => VIDEO_CAP_FALLBACK)
}

// ===== 业绩分类 =====
export function listCategories() {
  return http.get('/admin/categories').then((r: any) => (r.data ?? []) as any[])
}
export function createCategory(p: { name: string; icon?: string; sort: number; isLocked?: boolean }) {
  return http.post('/admin/categories', p).then((r: any) => r.data as any)
}
export function updateCategory(
  id: number,
  p: { name: string; icon?: string; sort: number; isLocked?: boolean }
) {
  return http.put(`/admin/categories/${id}`, p)
}
export function deleteCategory(id: number) {
  return http.delete(`/admin/categories/${id}`)
}
// 批量保存排序（拖拽 / 上下箭头调整顺序后一次性提交）
export function sortCategories(items: { id: number; sort: number }[]) {
  return http.put('/admin/categories/sort', items)
}

// ===== 项目案例 =====
export function listProjects(categoryId?: number) {
  return http
    .get('/admin/projects', { params: categoryId ? { categoryId } : {} })
    .then((r: any) => (r.data ?? []) as any[])
}
export function createProject(p: any) {
  return http.post('/admin/projects', p).then((r: any) => r.data as any)
}
export function updateProject(id: number, p: any) {
  return http.put(`/admin/projects/${id}`, p).then((r: any) => r.data as any)
}
export function deleteProject(id: number) {
  return http.delete(`/admin/projects/${id}`)
}
// 批量保存排序（拖拽 / 上下箭头调整顺序后一次性提交）
export function sortProjects(items: { id: number; sort: number }[]) {
  return http.put('/admin/projects/sort', items)
}

// ===== 业务服务 =====
export function listServices() {
  return http.get('/admin/services').then((r: any) => (r.data ?? []) as any[])
}
export function createService(p: { title: string; icon?: string; content: string; sort: number }) {
  return http.post('/admin/services', p).then((r: any) => r.data as any)
}
export function updateService(id: number, p: { title: string; icon?: string; content: string; sort: number }) {
  return http.put(`/admin/services/${id}`, p)
}
export function deleteService(id: number) {
  return http.delete(`/admin/services/${id}`)
}
// 批量保存排序（拖拽 / 上下箭头调整顺序后一次性提交）
export function sortServices(items: { id: number; sort: number }[]) {
  return http.put('/admin/services/sort', items)
}

// ===== 首页布局（板块顺序 + 首页背景） =====
// bgImage 背景图 URL（空串=不设背景）；bgMask 遮罩浓度 0~100（压暗背景保证白卡片可读）
// 详情页「更新于」的展示档位。与后端 model.TimeMode* 三档一一对应。
export type DetailTimeMode = 'hidden' | 'date' | 'minute'
export interface HomeLayoutData {
  order: string[]
  bgImage: string
  bgMask: number
  /** 项目案例详情页：不显示 / 只到日 / 到分 */
  projectTimeMode: DetailTimeMode
  /** 业务服务详情页：与案例分开控制（两者的「该不该显示时间」往往不是一个答案） */
  serviceTimeMode: DetailTimeMode
}
export function getHomeLayout() {
  return http
    .get('/admin/home-layout')
    .then(
      (r: any) =>
        (r.data ?? {
          order: [],
          bgImage: '',
          bgMask: 0,
          projectTimeMode: 'date',
          serviceTimeMode: 'date',
        }) as HomeLayoutData
    )
}
export function saveHomeLayout(p: Partial<HomeLayoutData>) {
  return http.put('/admin/home-layout', p)
}
