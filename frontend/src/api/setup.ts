import http from './request'

// 首次部署：状态查询 + 安装向导（测试连接 / 一键安装）。
//
// 背景：后端 cmd/server 在 DB_DSN 未提供时会进入「初始配置模式」——进程起来但
// 不加载业务，只提供 /setup 页。可生产环境前端由 nginx 托管（只反代 /api、
// /uploads），运维访问 80 端口看到的是本前端而不是后端那个页面，于是「打开浏览器
// 却不知道下一步」的问题并没解决。本模块让前端也能感知未配置状态，并把
// 「填表 → 测试连接 → 安装 → 自重启 → 进登录页」这条链路直接做在前端里。
//
// 两个易踩的坑（与项目其余 api 一致）：
//   - baseURL 已是 /api，这里不能再带 /api 前缀，否则变成 /api/api/...；
//   - 响应拦截器回传的是**信封本身** { code, data }，不是 axios response，
//     所以业务对象在 r.data，而不是 r.data.data。
//
// __silent 是必须的：安装向导自己要展示错误文案，不需要拦截器再弹一条红条；
// 后端旧版本没有这些接口时会 404，若不静默，每次启动都会弹「网络异常」。
export interface SetupMissing {
  env: string
  why: string
  value: string
}

export interface SetupMySQLHint {
  host: string
  port: string
  user: string
  database: string
  note: string
}

export interface SetupStatus {
  configured: boolean
  missing: SetupMissing[]
  envText: string
  hint: string
  // 以下为安装向导字段。旧版本后端不返回这些字段（undefined），
  // 此时 installable 判为 false，前端自动退回只读引导页而不是开一个装不了的向导。
  installable?: boolean
  engines?: string[]
  sqlitePath?: string
  configFile?: string
  adminUser?: string
  mysqlHint?: SetupMySQLHint | null
}

/** 安装表单（字段与后端 InstallReq 一一对应，扁平 JSON）。 */
export interface InstallPayload {
  engine: 'mysql' | 'sqlite'
  dbHost?: string
  dbPort?: string
  dbUser?: string
  dbPassword?: string
  dbName?: string
  createDatabase?: boolean
  sqlitePath?: string
  adminUser: string
  adminPass: string
  adminPass2: string
  jwtKey?: string
  cookieSecure?: boolean
  /**
   * 是否装载演示企业数据（后端 demoseed）。缺省 false：
   * 「没表态」与「明确不要」在这里不需要区分——安装是一次性动作，
   * 向导里是一个明确的勾选框，不勾就是不装。
   */
  loadDemoData?: boolean
}

export interface TestConnectionResult {
  driver: string
  dsn: string // 服务端已掩码
  willCreate: boolean
  note: string
}

export interface InstallResult {
  ok: boolean
  driver: string
  dsn: string
  configFile: string
  restarted: boolean
  message: string
}

function asStatus(d: any): SetupStatus {
  return {
    configured: !!d.configured,
    missing: Array.isArray(d.missing) ? d.missing : [],
    envText: typeof d.envText === 'string' ? d.envText : '',
    hint: typeof d.hint === 'string' ? d.hint : '',
    installable: d.installable === true,
    engines: Array.isArray(d.engines) ? d.engines : [],
    sqlitePath: typeof d.sqlitePath === 'string' ? d.sqlitePath : '',
    configFile: typeof d.configFile === 'string' ? d.configFile : '',
    adminUser: typeof d.adminUser === 'string' ? d.adminUser : '',
    mysqlHint: d.mysqlHint || null,
  }
}

/**
 * 查询安装状态。**失败会抛错**——调用方需要区分「请求失败」与「后端说未配置」：
 * 安装后轮询期间后端正在重启，请求必然失败，那既不是成功也不是「已配置」，
 * 只有抛错才能让轮询继续等下去（getSetupStatus 把失败折叠成 null，轮询不能用它）。
 */
export async function fetchSetupStatus(): Promise<SetupStatus> {
  const r = await http.get<{ code: number; data?: any }>('/setup/status', { __silent: true } as any)
  const d = r?.data
  if (!d || typeof d.configured !== 'boolean') {
    throw new Error('安装状态格式异常')
  }
  return asStatus(d)
}

/**
 * 启动期用的一次性查询：接口不存在 / 后端不可达一律按「已配置」处理，
 * 绝不因为一次网络抖动或旧版本后端挡住正常登录。
 */
export async function getSetupStatus(): Promise<SetupStatus | null> {
  try {
    return await fetchSetupStatus()
  } catch {
    return null
  }
}

/** 测试数据库连接（不建库、不建表、不写任何东西）。失败时 reject，msg 可直接展示。 */
export async function testConnection(payload: InstallPayload): Promise<TestConnectionResult> {
  try {
    const r = await http.post<{ code: number; msg?: string; data?: TestConnectionResult }>(
      '/setup/test',
      payload,
      { __silent: true } as any,
    )
    if (!r || r.code !== 0) throw new Error((r && r.msg) || '连接失败')
    return (r.data || {}) as TestConnectionResult
  } catch (e: any) {
    // 拦截器已把业务失败转成 Error(msg)；网络层失败没有 msg，补一句人话
    throw new Error((e && e.message) || '网络请求失败，请检查服务后重试')
  }
}

/** 执行安装：建库建表 + 建超管 + 写配置文件；服务端随后自重启。 */
export async function install(payload: InstallPayload): Promise<InstallResult> {
  try {
    const r = await http.post<{ code: number; msg?: string; data?: InstallResult }>(
      '/setup/install',
      payload,
      { __silent: true } as any,
    )
    if (!r || r.code !== 0) throw new Error((r && r.msg) || '安装失败')
    return (r.data || {}) as InstallResult
  } catch (e: any) {
    throw new Error((e && e.message) || '网络请求失败，请检查服务后重试')
  }
}
