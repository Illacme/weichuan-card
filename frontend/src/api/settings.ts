import http from './request'

// 系统设置：把产品运行参数从「代码写死 / 环境变量」收敛为后台可维护的表数据。
// 与后端 sys_setting 表 + internal/setting 的 registry 一一对应。
export interface SettingItem {
  key: string
  /** 中文名（界面主标签；旧响应可能缺省，界面回落显示 key） */
  name?: string
  group: string
  value: string
  // int | string | bool | string_slice（后端按此校验，前端据此渲染控件）
  type: string
  description: string
  // --- 生效方式元数据（后端 registry 下发，前端据此渲染提示与控件形态）---
  /** 敏感项：value 恒为掩码 '******'，真实值不回传；留空提交表示「回退环境变量」 */
  secret: boolean
  /** 保存后需重启服务才生效（云存储 / CDN 客户端在启动时构造） */
  restart: boolean
  /** 只读项：只能改环境变量后重启，界面禁用编辑 */
  readOnly: boolean
  /** 对应环境变量名（未设置时回退它，界面也据此提示改法） */
  envVar: string
  /** 枚举候选值（仅枚举项有，如 storage_driver 的 local/minio/oss/s3）；
      界面据此渲染下拉而非文本框，与后端 oneOf 校验同源 */
  options?: string[]
  /** 服务端路径类配置（node_bin / module_dir / source_dir）：驱动界面在文本框旁
      渲染一个「浏览服务器目录」按钮，弹出选择器在**服务器**文件系统上挑路径
      （这些路径指向后端要 exec 的服务器，浏览器原生文件框选的是客户端机器，没意义） */
  pathKind?: string
  /** 当前是否已有有效值（库值或环境变量） */
  configured: boolean
}

// 服务端路径选择器结果（GET /api/admin/settings/paths）。
// 与后端 internal/pathpick 的 Result 结构一致：列举当前目录、徽标、自动探测候选、
// 以及「当前路径作为该类目标」的实时校验（前端三处校验口径与后端 CheckPath 同源）。
export interface PathEntry {
  name: string
  path: string
  type: 'dir' | 'file'
  marker: string
}
export interface PathSuggestion {
  path: string
  label: string
  reason: string
}
export interface PathStatus {
  ok: boolean
  message: string
}
export interface PathBrowseResult {
  kind: string
  /** 实际列举的目录（已解析符号链接）——面包屑与列表都以它为准 */
  path: string
  /** 当前「待选中」的路径：node_bin 下可能是目录里的某个文件，其余就是目录本身 */
  selected: string
  /** selected 是否目录（node_bin 借此区分「还没选到文件」） */
  selectedIsDir: boolean
  parent: string
  home: string
  entries: PathEntry[]
  truncated: boolean
  current: PathStatus
  suggestions: PathSuggestion[]
}

// 浏览服务器目录 / 挑选可执行文件（GET /api/admin/settings/paths?kind=&dir=）。
// 平台超管鉴权（后端 PlatformAdminMiddleware 拦截）。dir 缺省时后端给一个合理起点。
export function browseServerPaths(kind: string, dir?: string) {
  const params: Record<string, string> = { kind }
  if (dir) params.dir = dir
  // __silent：错误由选择器就地展示（状态条文案 + 列表为空），不要再弹全局红条——
  // 否则一次失败会同时出现「弹窗内提示」和「右上角 toast」两处，看着像两个错。
  return http.get<{ code: number; msg?: string; data?: PathBrowseResult }>('/admin/settings/paths', {
    params,
    __silent: true,
  } as any)
}

// 平台超管读取全部可配置项（GET /api/admin/settings）
export function getSettings() {
  return http.get<{ code: number; data?: SettingItem[] }>('/admin/settings')
}

// 平台超管批量更新（PUT /api/admin/settings）。
// 值统一为字符串（与后端 map[string]string 契约一致），由后端按 type 校验。
// 只提交变更项：敏感项不带入掩码，未修改的项不会误清成空值。
// 回包会点名「需重启才生效」的项（restartKeys），前端据此提示，避免「改了没反应」的误解。
export interface UpdateSettingsResp {
  code: number
  msg?: string
  data?: { restartKeys?: string[] }
}
export function updateSettings(values: Record<string, string>) {
  return http.put<UpdateSettingsResp>('/admin/settings', values)
}

// 公开配置（GET /api/public/settings）：仅返回非敏感项（品牌名 / 小程序基址 / 锁定租户），
// 无需登录——小程序冷启动与后台品牌展示都走这个接口。
// company：登录页白标码（?company=），命中 enabled 企业时回传 login_brand_name（该企业简称）；
// 未命中（不传 / 不匹配 / 企业停用）则不回传该键，前端据此回落全局品牌名。
export function getPublicSettings(company?: string) {
  const params: Record<string, string> = {}
  if (company) params.company = company
  return http.get<{ code: number; data?: Record<string, string> }>('/public/settings', { params })
}
