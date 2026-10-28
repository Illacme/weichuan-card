// 埋点上报统一入口（后端 POST /api/stat/track：公开接口、按 IP 限流 60/分）。
//
// 事件类型与 targetId 约定必须与后端 internal/handler/stat.go 的 statEventTypes
// 保持一致（后端是权威白名单，端上多打一个不认的类型只会在统计表里留脏行）：
//
//   visit        访问名片（首页，页面实例级去重）   targetId=0
//   call         点击拨号                           targetId=0
//   save         点击保存（存通讯录）                targetId=0
//   project_view 浏览案例详情                       targetId=案例ID（后端强制存在+同租户）
//   service_view 浏览服务详情                       targetId=服务ID（同上）
//   video_play   播放公司视频（组件实例级去重）       targetId=0
//   share        转发                               targetId=0（名片）或案例/服务ID
//   poster_save  保存海报                           targetId=0
//
// 三条纪律：
// 1. 失败静默：埋点是旁路观察，不 toast、不 throw、不 await 阻塞渲染——
//    断网或撞限流（429）时丢就丢了，绝不能影响用户当前操作；
// 2. 白名单外类型就近丢弃：少一次注定被后端拒的无用请求；
// 3. targetId 为 0 时省略该字段：保持 visit/call/save 的老载荷形状不变
//    （后端缺省即 0），项目早期抓包的旧数据字段也保持一致。
//
// 流量来源（source）纪律（与后端 stat.go 的 statSourceTypes 一一对应）：
// 4. source 只在 visit 事件上携带（入口事件才有「从哪来」的语义）；其余事件
//    不传，后端归一为 unknown，绝不污染「来源分布」；
// 5. source 由端上根据微信 scene / 海报标记推导，后端再次白名单归一——
//    端上推导错了也最多归类偏差，不会进任何权限 / 租户判断。
import { request } from './request'

export type TrackEvent =
  | 'visit'
  | 'call'
  | 'save'
  | 'project_view'
  | 'service_view'
  | 'video_play'
  | 'share'
  | 'poster_save'

const TRACK_EVENTS: TrackEvent[] = [
  'visit',
  'call',
  'save',
  'project_view',
  'service_view',
  'video_play',
  'share',
  'poster_save',
]

// SOURCE_LABELS 来源类别 → 中文标签（后端只回英文类别，端上做展示翻译）。
// 取值与后端 stat.go 的 statSourceTypes、前端 api/stats.ts 的 SOURCE_LABELS
// **必须三处一致**：对不上时页面会出现一行裸英文，而统计数字本身完全正常——
// 典型的「不报错、但看着像数据缺失」。
export const SOURCE_LABELS: { [k: string]: string } = {
  scan: '扫码',
  share_card: '转发',
  search: '搜索',
  poster: '海报',
  unknown: '未知',
  other: '其他',
}

// source 中文标签（未知类别原样返回，不吞掉——新类别出现时要能看见它，
// 而不是显示成空白让人以为是数据丢了）。
export function sourceLabel(s: string): string {
  return SOURCE_LABELS[s] || s
}

// 微信启动场景值 → 流量来源。只覆盖「明确可判定」的区间，识别不了就返回**空串**
// （宁可少分一类，也绝不把扫描误判成转发、把搜索误判成扫码）。
//
// 返回空串而不是 'unknown' 是有意的：无法识别 = 本次「没有来源」，按 track() 的
// 纪律（只在确有来源时才带 source）就不该往载荷里塞字段，老载荷形状保持不变；
// 后端对空串同样归一为 unknown（stat.go 的 normalizeSource），统计口径完全一致。
// 来源类别与后端 stat.go 的 statSourceTypes 必须一致，否则分布图对不上。
//
//	scan        扫码（二维码 / 小程序码，含海报上的码）
//	share_card  转发卡片（聊天/群聊/App/小程序互跳，scene 1007/1008/1036/...）
//	search      微信搜索 / 发现 / 我的小程序等入口
//	（poster 不靠 scene 判，而靠分享/海报二维码里的 from=poster 标记，见 deriveSource）
const shareScenes = mapInt(1007, 1008, 1036, 1037, 1038, 1044, 1045, 1058, 1084, 1091, 1095, 1100, 1106, 1126, 1131)
const scanScenes = mapInt(1011, 1012, 1013, 1014, 1017, 1029, 1030, 1031, 1032, 1047, 1048, 1049, 1052, 1072, 1073, 1074, 1111, 1112, 1113, 1114, 1115, 1116, 1133, 1135, 1136, 1137, 1138, 1139, 1140, 1141, 1142, 1143, 1144, 1145, 1146, 1147, 1148)
const searchScenes = mapInt(1001, 1020, 1022, 1023, 1024, 1025, 1026, 1027, 1028, 1034, 1035, 1039, 1042, 1043, 1046, 1050, 1051, 1053, 1054, 1055, 1056, 1057, 1059, 1060, 1064, 1065, 1067, 1068, 1069, 1071, 1077, 1078, 1079, 1081, 1082, 1089, 1090, 1096, 1097, 1101, 1102, 1103, 1104, 1107, 1118, 1119, 1120, 1121, 1124, 1125, 1129, 1130)

// mapInt 把一串场景值收成 int→bool 的查表，供 sceneToSource O(1) 判定。
function mapInt(...vals: number[]): { [k: number]: boolean } {
  const m: { [k: number]: boolean } = {}
  for (const v of vals) m[v] = true
  return m
}

// sceneToSource 微信启动场景值 → 来源类别（见上方三张表注释）。
// 识别不了返回空串（= 不携带 source，后端归一为 unknown），详见表上方注释。
export function sceneToSource(scene: number): string {
  if (shareScenes[scene]) return 'share_card'
  if (scanScenes[scene]) return 'scan'
  if (searchScenes[scene]) return 'search'
  return ''
}

// deriveSource 综合判定本次进入的来源：优先认海报标记（from=poster），
// 否则用微信场景值（scene）归类。poster 不靠 scene 判——扫海报上的小程序码
// 与扫普通码在 scene 上都是「scan」，只有海报二维码里埋的 from=poster 能区分。
//
// 参数：
//   - scene：微信启动场景值（wx.getEnterOptionsSync().scene，最可靠的数字 scene）；
//   - query：页面 onLoad 的 options，可能带 from 路径参数或 scene 字符串
//     （小程序码场景下 query.scene 是 "staffId=12&from=poster" 这类编码串）。
export function deriveSource(scene: number, query?: any): string {
  if (query) {
    if (query.from === 'poster') return 'poster'
    const raw = String(query.scene ?? '')
    // 微信把 scene 串**整体 URL 编码**后才放进 query.scene
    // （后端写的 "staffId=42&from=poster" 到手是 "staffId%3D42%26from%3Dposter"）。
    // 不解码就匹配 from=poster 会永远命中不了——扫海报进来被当成普通扫码，
    // 海报渠道在来源分布里恒为 0，且不报任何错（静默失真）。
    // 解码失败（本身含非法 % 序列）时退回原串，不至于把整个来源判定带崩。
    let s = raw
    try {
      s = decodeURIComponent(raw)
    } catch {
      s = raw
    }
    if (/from=poster/.test(s)) return 'poster'
  }
  return sceneToSource(scene)
}

// track 上报一条埋点。staffId 为 0（未携带员工编号的路径）时直接丢弃——
// 统计按员工聚合，没有归属的事件进了后台也只是对不上账的噪声。
// 注意 project_view / service_view 必须带目标 id：后端对这两类强制校验
// 「存在且与员工同租户」，漏带会回 code:1（页面无感，但统计里没这条）。
// source 仅 visit 事件携带（见文件头纪律 4）。
export function track(event: TrackEvent, staffId: number, targetId = 0, source = ''): void {
  if (!staffId || staffId <= 0) return
  if (TRACK_EVENTS.indexOf(event) < 0) return
  const payload: { staffId: number; eventType: TrackEvent; targetId?: number; source?: string } = {
    staffId,
    eventType: event,
  }
  if (targetId > 0) payload.targetId = targetId
  // 只在确有来源时才带 source：visit 入口带（由 deriveSource 推出），
  // 其余事件省略——后端对空串归一为 unknown，不会污染分布。
  if (source) payload.source = source
  request('POST', '/api/stat/track', payload).catch(() => {
    // 断网 / 限流：静默丢弃（见文件头纪律 1）
  })
}
