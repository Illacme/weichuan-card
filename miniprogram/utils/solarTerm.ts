// 二十四节气：判断「今天是不是节气日」，并给出对应的海报背景。
//
// 为什么要自己判而不用日历组件：小程序端只需要一个布尔 + 一个素材 id，
// 引入整个农历库（几十 KB + 1900-2100 的月历表）不划算。这里只留
// 「24 个节气的公历日期」这一张表，判等即可。
//
// 日期是怎么来的、有多可信：
// 表由 chinese-days（寿星公式 + 例外校正）导出，并用 Meeus《Astronomical
// Algorithms》的太阳视黄经公式独立复算过一遍——2020-2040 共 504 个节气日
// 两者完全一致。残差风险只有一种：节气交割时刻落在北京时间午夜前后一两小时内
// （约每年两三个），此时 ±15 分钟的算法误差可能让日期差一天。2020-2040 内
// 没有这种临界个例；2045 小暑（23:57）即为一例，已通过把表截到 2040 避开。
//
// 表外年份（<2020 或 >2040）返回 null：不猜、不插值。届时的表现是
// 「不自动选节气图」，用户仍可在背景条里手动挑，不会白屏也不会报错。
// 到期后按上面两条来源重新生表即可，格式见 TERM_DAYS 注释。

/** 一个节气：key 与素材 id 后缀一致（poster-lichun），name 是界面用的中文名 */
export interface SolarTerm {
  key: string
  name: string
  /** 内置海报素材 id（backend/internal/assets/library 里 poster-<key>） */
  posterId: string
  /** 交节月份（1-12），用于在 TERM_DAYS 的年度串里定位 */
  month: number
}

// 固定顺序：小寒 → 大寒 → 立春 → … → 冬至。
// 这个顺序即 TERM_DAYS 年度串里的下标顺序，改顺序必须同步改表。
export const SOLAR_TERMS: SolarTerm[] = [
  { key: 'xiaohan', name: '小寒', posterId: 'poster-xiaohan', month: 1 },
  { key: 'dahan', name: '大寒', posterId: 'poster-dahan', month: 1 },
  { key: 'lichun', name: '立春', posterId: 'poster-lichun', month: 2 },
  { key: 'yushui', name: '雨水', posterId: 'poster-yushui', month: 2 },
  { key: 'jingzhe', name: '惊蛰', posterId: 'poster-jingzhe', month: 3 },
  { key: 'chunfen', name: '春分', posterId: 'poster-chunfen', month: 3 },
  { key: 'qingming', name: '清明', posterId: 'poster-qingming', month: 4 },
  { key: 'guyu', name: '谷雨', posterId: 'poster-guyu', month: 4 },
  { key: 'lixia', name: '立夏', posterId: 'poster-lixia', month: 5 },
  { key: 'xiaoman', name: '小满', posterId: 'poster-xiaoman', month: 5 },
  { key: 'mangzhong', name: '芒种', posterId: 'poster-mangzhong', month: 6 },
  { key: 'xiazhi', name: '夏至', posterId: 'poster-xiazhi', month: 6 },
  { key: 'xiaoshu', name: '小暑', posterId: 'poster-xiaoshu', month: 7 },
  { key: 'dashu', name: '大暑', posterId: 'poster-dashu', month: 7 },
  { key: 'liqiu', name: '立秋', posterId: 'poster-liqiu', month: 8 },
  { key: 'chushu', name: '处暑', posterId: 'poster-chushu', month: 8 },
  { key: 'bailu', name: '白露', posterId: 'poster-bailu', month: 9 },
  { key: 'qiufen', name: '秋分', posterId: 'poster-qiufen', month: 9 },
  { key: 'hanlu', name: '寒露', posterId: 'poster-hanlu', month: 10 },
  { key: 'shuangjiang', name: '霜降', posterId: 'poster-shuangjiang', month: 10 },
  { key: 'lidong', name: '立冬', posterId: 'poster-lidong', month: 11 },
  { key: 'xiaoxue', name: '小雪', posterId: 'poster-xiaoxue', month: 11 },
  { key: 'daxue', name: '大雪', posterId: 'poster-daxue', month: 12 },
  { key: 'dongzhi', name: '冬至', posterId: 'poster-dongzhi', month: 12 },
]

// 每年 24 个「几号」，按 SOLAR_TERMS 的固定顺序，两位数字零填充（3 号写成 03）。
// 例：2026 = '05 20 | 04 18 | 05 20 | 03 05 | 05 21 | 05 21 | 07 07 | 07 23 |
//              08 07 | 08 23 | 09 07 | 10 08 | 10 23 | 11 07 | 11 22 | 12 07 | 12 22'
// 每年 48 字符、21 年共约 1.2KB——换 504 条 'YYYY-MM-DD' 字符串要 10KB 以上。
const TERM_DAYS: { [year: string]: string } = {
  2020: '062004190520041905200521062207220722082307220721',
  2021: '052003180520042005210521072207230723082307220721',
  2022: '052004190520052005210621072307230723082307220722',
  2023: '052004190621052006210621072308230823082408220722',
  2024: '062004190520041905200521062207220722082307220621',
  2025: '052003180520042005210521072207230723082307220721',
  2026: '052004180520052005210521072307230723082307220722',
  2027: '052004190621052006210621072308230823082307220722',
  2028: '062004190520041905200521062207220722082307220621',
  2029: '052003180520042005210521072207230723082307220721',
  2030: '052004180520052005210521072307230723082307220722',
  2031: '052004190621052006210621072308230823082307220722',
  2032: '062004190520041905200521062207220722082307220621',
  2033: '052003180520042005210521072207230723082307220721',
  2034: '052004180520052005210521072307230723082307220722',
  2035: '052004190621052005210621072307230823082307220722',
  2036: '062004190520041905200521062207220722082307220621',
  2037: '052003180520042005210521072207230723082307220721',
  2038: '052004180520052005210521072307230723082307220722',
  2039: '052004190621052005210621072307230823082307220722',
  2040: '062004190520041905200521062207220722082307220621',
}

/**
 * 某公历日是否节气日：是则返回该节气，否则 null。
 *
 * 用本地时间的年月日（getFullYear / getMonth / getDate）：表里的日期是北京时间口径，
 * 而小程序绝大多数用户就在东八区，用本地日期才与「用户看到的日历」一致。
 * 少数海外时区可能差一天——错一天只是早晚一天换图，不影响功能可用性。
 *
 * @param year  公历年
 * @param month 1-12
 * @param day   1-31
 */
export function solarTermOf(year: number, month: number, day: number): SolarTerm | null {
  const days = TERM_DAYS[year]
  if (!days) return null
  // 每月两个节气，按 SOLAR_TERMS 顺序排：1 月=小寒+大寒，2 月=立春+雨水 …
  const i1 = (month - 1) * 2
  if (i1 < 0 || i1 + 1 >= SOLAR_TERMS.length) return null
  // 注意：节气下标 n 在年度串里的字符偏移是 n*2（每个节气占两位），
  // 不是 n。曾经把两者当成一个，2 月 4 日被判成雨水——串里第 3 对才是立春。
  const d1 = parseInt(days.slice(i1 * 2, i1 * 2 + 2), 10)
  const d2 = parseInt(days.slice(i1 * 2 + 2, i1 * 2 + 4), 10)
  if (Number.isNaN(d1) || Number.isNaN(d2)) return null
  if (day === d1) return SOLAR_TERMS[i1]
  if (day === d2) return SOLAR_TERMS[i1 + 1]
  return null
}

/** 同上，直接收 Date（页面里传 new Date() 用这个） */
export function solarTermOfDate(date: Date): SolarTerm | null {
  return solarTermOf(date.getFullYear(), date.getMonth() + 1, date.getDate())
}

/**
 * 把某张内置素材挪到列表第一位。
 *
 * 为什么是「挪到最前」而不是「置顶插入」：背景条是横滑的，立春那天用户第一眼
 * 就该看到立春；其余素材相对顺序保持不变，老用户找得到原来那张。
 * 目标不在清单里（素材调整 / 老后端）时原样返回，不插空位。
 *
 * festival.ts 的 festivalFirst 也走这里——两个模块共用同一套换位逻辑，
 * 免得「节气版」和「节日版」各写一遍、日后改一处漏一处。
 */
export function pinFirst<T extends { id: string }>(list: T[], posterId: string): T[] {
  const hit = list.findIndex((p) => p.id === posterId)
  if (hit <= 0) return list // 不在清单里，或本来就是第一位
  return [list[hit], ...list.slice(0, hit), ...list.slice(hit + 1)]
}

/** 把当前节气那张内置素材排到列表第一位（见 pinFirst）。 */
export function termFirst<T extends { id: string }>(list: T[], term: SolarTerm | null): T[] {
  if (!term) return list
  return pinFirst(list, term.posterId)
}
