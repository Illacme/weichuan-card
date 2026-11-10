// 时间格式化：后端 Go time.Time 序列化为 RFC3339（2026-10-16T15:34:12.123+08:00），
// 直接绑到 wxml 会把 T／纳秒／时区后缀一起显示出来。
// 与 PC 后台 frontend/src/utils/datetime.ts 同一套口径，两端展示一致。
// 空值返回「—」；非法时间原样回显，不静默吞掉，便于暴露脏数据。

const pad = (n: number) => String(n).padStart(2, '0')

// 把入参统一成 Date；null=没值（由调用方决定显示什么），string=非法值原样回显。
// 抽出来是为了让三个 format 函数的空值／脏值口径完全一致——各自写一遍必然走偏。
function toDate(value: string | number | Date | null | undefined): Date | string | null {
  if (value === null || value === undefined || value === '') return null
  const d = value instanceof Date ? value : new Date(value)
  return Number.isNaN(d.getTime()) ? String(value) : d
}

/** 只到日：2026-11-10 */
export function formatDate(value: string | number | Date | null | undefined): string {
  const d = toDate(value)
  if (d === null) return '—'
  if (typeof d === 'string') return d
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** 到分：2026-11-10 14:34 */
export function formatMinute(value: string | number | Date | null | undefined): string {
  const d = toDate(value)
  if (d === null) return '—'
  if (typeof d === 'string') return d
  return `${formatDate(d)} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 到秒：2026-11-10 14:34:12 */
export function formatDateTime(value: string | number | Date | null | undefined): string {
  const d = toDate(value)
  if (d === null) return '—'
  if (typeof d === 'string') return d
  return `${formatMinute(d)}:${pad(d.getSeconds())}`
}

// 详情页「更新于」的文本，不显示时返回空串（wxml 用 `wx:if` 整行隐藏）。
//
// 档位由后台按租户下发（项目案例 / 业务服务各一列，默认 date）：
//   hidden → 空串；date → 只到日；minute → 到分。
// 未知档位（端上比后端新、或库里被手工改坏）一律按 date 处理：宁可只显示日期，
// 也不要让这一行凭空消失——「内容看起来少了一块」比「格式不够精细」更难解释。
export function detailTimeText(
  mode: string | null | undefined,
  at: string | null | undefined
): string {
  if (!at) return ''
  if (mode === 'hidden') return ''
  return mode === 'minute' ? formatMinute(at) : formatDate(at)
}
