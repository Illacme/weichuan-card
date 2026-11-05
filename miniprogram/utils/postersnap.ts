// 海报页「上次出图用到的三个图片地址」的本地快照。
//
// 为什么需要它：海报页的耗时结构是**两波串行**
//
//   Wave1  4 个接口并行（名片 / 小程序码 / 背景清单 / 个性化设置）  ≈ 0.5s
//   Wave2  背景 + 头像 + 二维码三张图并行（preload 三张齐了才开画） ≈ 1.0s
//
// Wave2 必须等 Wave1 回来才知道图片地址，于是用户的实际等待 = 两波相加。
//
// 实测（同一连接复用，已排除 TLS 握手；单位秒）：
//
//   /api/card            1.1 KB   0.46
//   /api/assets/posters  10.1 KB  0.46
//   头像 PNG              3.4 KB   0.78
//   背景原图              29.6 KB  1.10
//
// 3.4 KB 和 29.6 KB 差 8 倍体积、耗时只差 0.3 秒——**耗时由「一次网络往返」
// 决定，与体积几乎无关**。所以压缩素材、把图打进小程序包都治不了这个病：
//
//   * 59 张原图合计 2136 KB，超过主包 2 MB 上限，塞不进去；
//   * 能进包的缩略图只有 320×460，而画布按 dpr 放大到 640×920，必然糊
//     ——海报是要存相册、发群里的，糊了就废了；
//   * 就算背景变成包内文件，preload 仍在等头像和二维码那两次往返，
//     关键路径一秒都省不下来。
//
// 唯一有效的办法是**让 Wave2 不必等 Wave1**：把上次的地址记下来，下次进页面
// 在 onLoad 就拿着它们起跑，与 Wave1 并行；接口回来后 draw() 直接从内存缓存
// 取图，Wave2 被完全吸收掉。
//
// 三条边界：
//
//  1. **按员工分键**。同一台手机可能看不同同事的名片，串了就是画别人家的头像。
//  2. **有有效期，且过期只是「不预热」**。真正的数据仍以接口返回为准，快照
//     永远不参与出图——它只决定「提前下载哪三张」，所以即使全错也只是白跑一次
//     下载，不会画出错的东西。
//  3. **坏数据一律当没有**。storage 可能被旧版本或人工改过，解析失败就返回
//     null，不能让它把页面主流程带崩。
//
// `now` / `at` 做成可注入参数：涉及时间的用例必须能被一个固定时钟完全驱动，
// 否则用例只在「写它的那天」成立（项目里有过这种教训）。

// 缓存键前缀。真正的键还要拼员工 id，见 key()。
const KEY_PREFIX = 'posterSnap'

// 有效期。取 7 天：背景和版式是「用户自己的偏好」，一周内变动的概率很低；
// 而员工换了头像、二维码重新生成这类事，接口回来后画的是新图，
// 快照下错的那一次只是白跑一次预热，代价可以忽略。
export const POSTER_SNAP_TTL_MS = 7 * 24 * 60 * 60 * 1000

export interface PosterSnap {
  bg: string
  avatar: string
  qr: string
  at: number
}

function key(staffId: number): string {
  return KEY_PREFIX + ':' + (Number(staffId) > 0 ? Math.floor(Number(staffId)) : 0)
}

// 只认 http(s) 绝对地址。assetUrl 出来的一定是绝对地址；相对路径喂给
// canvas 的 createImage 会静默失败（不报错、不回调），比不预热还糟——
// 它会占住 3 秒失败锁，让真正该加载的那次也被跳过。
function isAbs(u: any): boolean {
  return typeof u === 'string' && (u.indexOf('http://') === 0 || u.indexOf('https://') === 0)
}

// 读快照。没有 / 过期 / 结构坏了都返回 null（= 这次不预热，行为与改造前一致）。
export function readPosterSnap(staffId: number, now?: number): PosterSnap | null {
  const sid = Number(staffId) || 0
  if (sid <= 0) return null
  try {
    const raw = wx.getStorageSync(key(sid))
    if (!raw) return null
    const rec: PosterSnap = typeof raw === 'string' ? JSON.parse(raw) : raw
    if (!rec || typeof rec !== 'object') return null
    const at = Number(rec.at) || 0
    if (at <= 0) return null
    const t = typeof now === 'number' ? now : Date.now()
    // t < at 说明本机时钟被往回拨过：按过期处理，宁可这一次不预热
    if (t < at || t - at > POSTER_SNAP_TTL_MS) return null
    const snap: PosterSnap = {
      bg: isAbs(rec.bg) ? rec.bg : '',
      avatar: isAbs(rec.avatar) ? rec.avatar : '',
      qr: isAbs(rec.qr) ? rec.qr : '',
    }
    // 三个都是空的（比如上次出图时三张全没加载出来）等于没有快照：
    // 留着只会让 warmFromSnap 白建一个离屏 canvas
    if (!snap.bg && !snap.avatar && !snap.qr) return null
    return { ...snap, at }
  } catch {
    return null
  }
}

// 记下这次出图用到的三个地址。传空串表示「这一项这次没有」（如白底海报没有背景）。
export function writePosterSnap(
  staffId: number,
  snap: { bg?: string; avatar?: string; qr?: string },
  at?: number,
): void {
  const sid = Number(staffId) || 0
  if (sid <= 0 || !snap) return
  try {
    const rec: PosterSnap = {
      bg: isAbs(snap.bg) ? String(snap.bg) : '',
      avatar: isAbs(snap.avatar) ? String(snap.avatar) : '',
      qr: isAbs(snap.qr) ? String(snap.qr) : '',
      at: typeof at === 'number' ? at : Date.now(),
    }
    if (!rec.bg && !rec.avatar && !rec.qr) return
    wx.setStorageSync(key(sid), JSON.stringify(rec))
  } catch {
    // 存储满 / 隐私模式：只影响「下次要不要多等一次往返」，不影响功能
  }
}

// 清掉快照。留作自愈用：万一某天发现快照里的地址长期失效，
// 页面可以主动清一次，回到「老老实实等接口」的行为。
export function clearPosterSnap(staffId: number): void {
  const sid = Number(staffId) || 0
  if (sid <= 0) return
  try {
    wx.removeStorageSync(key(sid))
  } catch {
    // 清不掉最多导致下次多跑一次预热
  }
}
