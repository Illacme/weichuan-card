import http from './request'

/**
 * 备份状态与产物（平台超管的「备份与恢复」面板）。
 *
 * 后端对应 backend/internal/handler/backup.go，三条边界在这里同样成立：
 *   1. 只能看状态、补一次、下载「库 + 图片」，没有「上传恢复」——恢复是破坏性操作，
 *      必须走 scripts/restore.sh（有确认、有恢复前安全网）。
 *   2. 密钥包不可下载（后端也会拒）。前端不画按钮只是省事，真正的约束在服务端。
 *   3. 备份是异步跑的，这里只轮询状态，不等一次 HTTP 请求跑完（GB 级图片会 504）。
 */

export interface BackupItem {
  kind: string // db / uploads / secrets
  label: string
  file: string
  bytes: number
  mtime: string // RFC3339
  ageHours: number
  fresh: boolean
}

export interface BackupRunResult {
  at: string
  ok: boolean
  message: string
}

export interface BackupStatus {
  dir: string
  driver: string // sqlite / mysql
  canRun: boolean // 仅 SQLite 形态可在后台触发
  running: boolean
  freshHours: number // 新鲜度阈值（小时），与后端告警阈值一致
  items: BackupItem[]
  lastRun: BackupRunResult | null
  notes: string[] // 缺件 / 不可下载等需要当面说清的话
  automaticTip: string
}

// 面板固定按这三行渲染。不能只渲染 items：缺失的那一件恰好最该被看见，
// 而 items 里没有它——「看不见的东西等于没有」正是这个面板要解决的问题。
export const BACKUP_KINDS: { kind: string; label: string; hint: string }[] = [
  { kind: 'db', label: '数据库', hint: '全部业务数据（含加密后的员工个人信息）' },
  { kind: 'uploads', label: '上传文件', hint: '名片图片 / 案例图，丢了页面会成片空白' },
  { kind: 'secrets', label: '运行时密钥', hint: 'PII_ENC_KEY 等：没有它，库里的姓名手机解不出来' },
]

const EMPTY_STATUS: BackupStatus = {
  dir: '',
  driver: '',
  canRun: false,
  running: false,
  freshHours: 26,
  items: [],
  lastRun: null,
  notes: [],
  automaticTip: '',
}

export function getBackupStatus(): Promise<BackupStatus> {
  // 后端异常时退化成空状态而不是抛错：面板本身挂了也要能显示「什么都没有」，
  // 这比整页白屏、让人以为备份正常要诚实。
  return http
    .get('/admin/backup/status')
    .then((r) => ({ ...EMPTY_STATUS, ...((r?.data ?? {}) as Partial<BackupStatus>) }))
    .catch(() => ({ ...EMPTY_STATUS }))
}

export function runBackup(): Promise<{ started: boolean }> {
  return http.post('/admin/backup/run').then((r) => (r?.data ?? { started: false }) as { started: boolean })
}

/** 下载地址。拼在前端而不是让后端下发 URL：面板与接口同源，少一层可被改写的输入。 */
export function backupDownloadUrl(name: string): string {
  const base = import.meta.env.VITE_API_BASE || '/api'
  return `${base}/admin/backup/download?name=${encodeURIComponent(name)}`
}

/**
 * 下载一份备份产物。
 *
 * 不走 http 实例：它在响应拦截里假定 body 是 JSON 信封（{code,msg,data}），
 * 而这里要的是二进制——一个 Blob 同样是 object，会被当成信封读到 code=undefined，
 * 于是「下载成功」变成弹一句「请求失败」。所以单独用 fetch。
 *
 * 失败时后端仍回 HTTP 200 + JSON（项目统一约定），因此必须看 content-type：
 * 直接把那段 JSON 存成 .gz，用户会得到一个解不开的假备份。
 */
export async function downloadBackup(name: string): Promise<void> {
  const resp = await fetch(backupDownloadUrl(name), { credentials: 'include' })
  const ct = resp.headers.get('content-type') || ''
  if (ct.includes('application/json')) {
    const body = (await resp.json().catch(() => ({}))) as { msg?: string }
    throw new Error(body?.msg || '下载失败')
  }
  if (!resp.ok) throw new Error(`下载失败（HTTP ${resp.status}）`)

  const blob = await resp.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  // 立刻 revoke 在部分浏览器上会让下载拿到空文件，放到下一轮事件循环
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

/** 人类可读的大小。备份动辄几百 MB，摊开成字节数没人读得懂。 */
export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

/** 「多久以前」。比绝对时间更能回答「这份备份还新鲜吗」。 */
export function describeAge(ageHours: number): string {
  if (!Number.isFinite(ageHours) || ageHours < 0) return '—'
  if (ageHours < 1) return '1 小时内'
  if (ageHours < 24) return `${Math.floor(ageHours)} 小时前`
  return `${Math.floor(ageHours / 24)} 天前`
}
