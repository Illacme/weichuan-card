// festival.ts —— 判断「今天是不是节日」，并给出对应的海报背景。
//
// 与 solarTerm.ts 的关系：**节日优先于节气**。同一天既是节日又是节气时
// （2020-2040 里有 14 处，如 2026-03-20 龙抬头撞春分、2038-02-04 春节撞立春），
// 用节日那张。理由是产品要求「优先校验当前日期是否节日」，也因为节气的
// 情感指向弱于节日——正月初四那天用户想看到的是「春节」而不是「立春」。
//
// 为什么不引农历库：和节气一样，小程序端只需要一个布尔 + 一个素材 id。
// 这里只留「农历节日的公历日期」一张表，判等即可。
//
// 清明与冬至**故意不在这里登记**：它们既是节气也是节日，但 poster-qingming /
// poster-dongzhi 已经由节气链路自动选中，重复登记会出现两个真相源，
// 而且「节日优先」在这两天恰好无从体现（两边返回同一张图）。生日的海报
// （poster-shengri）也不自动选——后端没有生日字段，猜不出来。

import { pinFirst } from './solarTerm'

/** 一个节日：key 自用，name 是界面文案，posterId 是内置海报素材 id */
export interface Festival {
  key: string
  name: string
  posterId: string
}

// 农历节日。顺序即 FEST_DAYS 年度串里末位那位数字的含义（0-9），
// 改顺序必须同步重新生成表。
export const LUNAR_FESTIVALS: Festival[] = [
  { key: 'chuxi', name: '除夕', posterId: 'poster-chuxi' },
  { key: 'laba', name: '腊八', posterId: 'poster-laba' },
  { key: 'xiaonian', name: '小年', posterId: 'poster-xiaonian' },
  { key: 'chunjie', name: '春节', posterId: 'poster-chunjie' },
  { key: 'yuanxiao', name: '元宵', posterId: 'poster-yuanxiao' },
  { key: 'longtaitou', name: '龙抬头', posterId: 'poster-longtaitou' },
  { key: 'duanwu', name: '端午', posterId: 'poster-duanwu' },
  { key: 'qixi', name: '七夕', posterId: 'poster-qixi' },
  { key: 'zhongqiu', name: '中秋', posterId: 'poster-zhongqiu' },
  { key: 'chongyang', name: '重阳', posterId: 'poster-chongyang' },
]

// 农历节日的公历日期，离线由 solarlunar 包的 lunar2solar 生成。
//
// 行 = 节日**实际所在的公历年**（不是农历年！腊八/小年/除夕落在哪个公历年
// 取决于该农历年有没有闰月、春节早或晚，没有规律可循：2022 年有两个腊八
// ——1/10 和 12/30，2023 年一个腊八都没有）。所以每行条目数不固定（9/10/11），
// 行首两位是条数，随后每条 5 字符：MM DD 序号。
//
// 例 2026 = '10' + '0126' '1' | '0210' '2' | '0216' '0' | '0217' '3' | '0303' '4'
//               | '0320' '5' | '0619' '6' | '0819' '7' | '0925' '8' | '1018' '9'
//         = 腊八 1/26、小年 2/10、除夕 2/16、春节 2/17、元宵 3/3、
//           龙抬头 3/20、端午 6/19、七夕 8/19、中秋 9/25、重阳 10/18
//
// 闰月里的同名日**不是**节日：2023-03-23 是闰二月初二，不是龙抬头。
// 生成时用 lunar2solar(y, m, d)（isLeapMonth 固定为 false）即不会取到闰月。
//
// 可信度：210 个日期逐一 solar2lunar 反算，农历月日全部对得上；
// 2026 年春节 2/17、小年 2/10（北方，腊月廿三）与公开日历一致。
// 表外年份的**农历**节日不猜不插值，退化为「不自动选农历节日图」；
// 公历节日（元旦/情人节/…）不依赖表，表外年份照样生效。
const FEST_DAYS: { [year: string]: string } = {
  2020: '1001021011720124001253020840224506256082571001810259',
  2021: '1001201020420211002123022640314506146081470921810149',
  2022: '110110101252013100201302154030450603608047091081004912301',
  2023: '09011420121001223020540221506226082270929810239',
  2024: '1001181020220209002103022440311506106081070917810119',
  2025: '1001071012220128001293021240301505316082971006810299',
  2026: '1001261021020216002173030340320506196081970925810189',
  2027: '1001151013020205002063022040309506096080870915810089',
  2028: '1001041011920125001263020940226505286082671003810269',
  2029: '1001221020620212002133022740316506166081670922810169',
  2030: '1001111012620202002033021740305506056080570912810059',
  2031: '1001011011620122001233020640222506246082471001810249',
  2032: '1001201020420210002113022540313506126081270919810129',
  2033: '1001081012320130001313021440302506016080170908810019',
  2034: '1001271021120218002193030540321506206082070927810209',
  2035: '1001161013120207002083022240311506106081070916810099',
  2036: '1001051012020127001283021140228505306082871004810279',
  2037: '1001231020720214002153030140318506186081770924810179',
  2038: '1001121012720203002043021840307506076080770913810079',
  2039: '1001021011720123001243020740224505276082671002810269',
  2040: '1001211020520211002123022640314506146081470920810149',
}

// 公历节日：日期固定，直接判等，不需要表。md 键是 '月-日'。
// key 同时也是 posterId 后缀（poster-yuandan）。
const SOLAR_FIXED: { [md: string]: Festival } = {
  '1-1': { key: 'yuandan', name: '元旦', posterId: 'poster-yuandan' },
  '2-14': { key: 'qingrenjie', name: '情人节', posterId: 'poster-qingrenjie' },
  '3-8': { key: 'nvshenjie', name: '女神节', posterId: 'poster-nvshenjie' },
  '5-1': { key: 'laodongjie', name: '劳动节', posterId: 'poster-laodongjie' },
  '6-1': { key: 'ertongjie', name: '儿童节', posterId: 'poster-ertongjie' },
  '9-10': { key: 'jiaoshijie', name: '教师节', posterId: 'poster-jiaoshijie' },
  '10-1': { key: 'guoqing', name: '国庆', posterId: 'poster-guoqing' },
  '12-24': { key: 'pinganye', name: '平安夜', posterId: 'poster-pinganye' },
  '12-25': { key: 'shengdan', name: '圣诞', posterId: 'poster-shengdan' },
}

// 「某月第 n 个星期几」的日期：母亲节（5 月第 2 个周日）、父亲节（6 月第 3 个周日）、
// 感恩节（11 月第 4 个周四）。用本地 Date 算，与节气那边同一口径（见 solarTerm.ts）。
function nthWeekdayOfMonth(year: number, month: number, weekday: number, n: number): number {
  const firstDow = new Date(year, month - 1, 1).getDay()
  return 1 + ((weekday - firstDow + 7) % 7) + (n - 1) * 7
}

const SOLAR_FLOAT: { match: (y: number, m: number, d: number) => boolean; fest: Festival }[] = [
  {
    fest: { key: 'muqinjie', name: '母亲节', posterId: 'poster-muqinjie' },
    match: (y, m, d) => m === 5 && d === nthWeekdayOfMonth(y, 5, 0, 2),
  },
  {
    fest: { key: 'fuqinjie', name: '父亲节', posterId: 'poster-fuqinjie' },
    match: (y, m, d) => m === 6 && d === nthWeekdayOfMonth(y, 6, 0, 3),
  },
  {
    fest: { key: 'ganenjie', name: '感恩节', posterId: 'poster-ganenjie' },
    match: (y, m, d) => m === 11 && d === nthWeekdayOfMonth(y, 11, 4, 4),
  },
]

const pad2 = (n: number) => (n < 10 ? '0' + n : '' + n)

/** 农历节日命中（查表）。表外年份 / 非节日日返回 null。 */
function lunarFestivalOf(year: number, month: number, day: number): Festival | null {
  const row = FEST_DAYS[year]
  if (!row) return null
  const count = parseInt(row.slice(0, 2), 10)
  if (Number.isNaN(count) || count <= 0) return null
  const mmdd = pad2(month) + pad2(day)
  for (let i = 0; i < count; i++) {
    const off = 2 + i * 5
    // 每条 5 字符：MM(2) DD(2) 序号(1)。偏移是 2 + i*5，不是 i*5——
    // 行首那两位条数也是字符，漏掉它整行都会串位。
    if (row.slice(off, off + 4) === mmdd) {
      const idx = parseInt(row[off + 4], 10)
      if (idx >= 0 && idx < LUNAR_FESTIVALS.length) return LUNAR_FESTIVALS[idx]
      return null
    }
  }
  return null
}

/**
 * 某公历日是否节日：是则返回该节日，否则 null。
 *
 * 同一天可能是多个节日（2020-2040 有 8 处，如 2033-02-14 元宵撞情人节、
 * 2037-02-14 除夕撞情人节）。规则：**农历节日优先**。传统节日的文化指向
 * 更明确，而公历节日（情人节/儿童节）氛围性更强；除夕那天显然不该出情人节海报。
 *
 * @param year  公历年
 * @param month 1-12
 * @param day   1-31
 */
export function festivalOf(year: number, month: number, day: number): Festival | null {
  if (month < 1 || month > 12 || day < 1 || day > 31) return null
  const lunar = lunarFestivalOf(year, month, day)
  if (lunar) return lunar
  const fixed = SOLAR_FIXED[month + '-' + day]
  if (fixed) return fixed
  for (const item of SOLAR_FLOAT) {
    if (item.match(year, month, day)) return item.fest
  }
  return null
}

/** 同上，直接收 Date（页面里传 new Date() 用这个） */
export function festivalOfDate(date: Date): Festival | null {
  return festivalOf(date.getFullYear(), date.getMonth() + 1, date.getDate())
}

/**
 * 把当天节日那张内置素材排到列表第一位（道理同 solarTerm.ts 的 termFirst）。
 * 节日图不在清单里（素材调整 / 老后端）时原样返回，不插空位。
 */
export function festivalFirst<T extends { id: string }>(list: T[], festival: Festival | null): T[] {
  if (!festival) return list
  return pinFirst(list, festival.posterId)
}
