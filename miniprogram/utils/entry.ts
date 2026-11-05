// 「默认进入指定员工名片」的本地记忆（企业后台配的 entry）。
//
// 为什么需要：名片墙（pages/team/team）是小程序的 entryPagePath。裸启动（扫体验版
// 二维码 / 直接打开小程序）时微信不带任何参数，而「要不要改道去某个人的名片」
// 只有后端知道——于是每一轮冷启动都得先等一次 /api/team 往返。
// 弱网下这一等就是 1~3 秒，访客停在一个自己根本不会用的页面上（还顶着「加载中」
// 和平台品牌名），然后才被 reLaunch 到真正想看的名片页。白等整整一次往返。
//
// 记住上次的结果，下次裸启动直接进名片页。
//
// 两条安全边界，缺一条都会出事：
//
//  1. **按租户分键**。平台模式下本机可能粘滞着不同企业的 companyId（扫 A 家的码
//     再扫 B 家的码）。不分键会把 A 企业的默认员工套到 B 企业头上——点进去是别人家公司。
//
//  2. **过期 + 自愈，两道都要有**。管理员换人、员工停用之后，缓存会指向一个已经
//     失效的名片。过期时间兜住「一直跳错」（慢，24 小时），名片页侧的自愈兜住
//     「当场跳错」（快，见 pages/index/index.ts 的 fromEntryCache 分支）：
//     名片不存在时清缓存并退回名片墙，名片墙拿 skipEntryCache=1 重新问后端。
//     只有过期没有自愈，访客要卡一天；只有自愈没有过期，只是慢一点——但两者
//     各自覆盖不同的失败面，所以都留着。
//
// `now` / `at` 做成可注入参数：涉及时间的用例必须能被一个固定时钟完全驱动，
// 否则用例只在「写它的那天」成立（项目里有过这种教训）。

// 缓存键前缀。真正的键还要拼租户 id，见 key()。
const KEY_PREFIX = 'entryStaff'

// 有效期。取 24 小时：既能让「每天打开一次」的访客基本都命中，
// 又能保证管理员改了配置最迟一天内生效。
export const ENTRY_TTL_MS = 24 * 60 * 60 * 1000

interface EntryRecord {
  staffId: number
  at: number
}

function key(companyId: number): string {
  return KEY_PREFIX + ':' + (Number(companyId) > 0 ? Math.floor(Number(companyId)) : 0)
}

// 读缓存的默认员工编号。没有 / 过期 / 结构坏了都返回 0（= 没有缓存，走网络）。
export function readEntryStaff(companyId: number, now?: number): number {
  try {
    const raw = wx.getStorageSync(key(companyId))
    if (!raw) return 0
    // 存储里理论上永远是 writeEntryStaff 写的 JSON 串；但 storage 可能被旧版本
    // 或人工写入过别的形状，解析失败就当成没有，不要让它把页面主流程带崩。
    const rec: EntryRecord = typeof raw === 'string' ? JSON.parse(raw) : raw
    const staffId = Number(rec && rec.staffId) || 0
    const at = Number(rec && rec.at) || 0
    if (staffId <= 0 || at <= 0) return 0
    const t = typeof now === 'number' ? now : Date.now()
    // t < at 说明本机时钟被往回拨过：按过期处理，宁可多问一次后端
    if (t < at || t - at > ENTRY_TTL_MS) return 0
    return staffId
  } catch {
    return 0
  }
}

// 记下「这家企业默认进这位员工的名片」。
export function writeEntryStaff(companyId: number, staffId: number, at?: number): void {
  const sid = Number(staffId) || 0
  if (sid <= 0) return
  try {
    const rec: EntryRecord = { staffId: sid, at: typeof at === 'number' ? at : Date.now() }
    wx.setStorageSync(key(companyId), JSON.stringify(rec))
  } catch {
    // 存储满 / 隐私模式：只影响「下次要不要多等一次往返」，不影响功能
  }
}

// 清掉缓存。管理员取消默认入口、或缓存指向的员工已经失效时调用。
export function clearEntryStaff(companyId: number): void {
  try {
    wx.removeStorageSync(key(companyId))
  } catch {
    // 同上：清不掉最多导致下次多问一次后端
  }
}
