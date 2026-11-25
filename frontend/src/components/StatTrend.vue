<template>
  <div class="stat-trend">
    <div class="trend-legend">
      <!-- 图例可点选：八条线挤在一张图上会互相盖住，故默认只画转化三线，
           需要看意向趋势时再勾。至少保留一项——全灭就没图可看了 -->
      <span
        v-for="s in seriesDef"
        :key="s.key"
        class="legend-item"
        :class="{ 'legend-off': !isOn(s.key) }"
        role="button"
        :aria-pressed="isOn(s.key)"
        @click="toggle(s.key)"
      >
        <i class="dot" :style="{ background: s.color }" />{{ s.label }}
      </span>
      <span class="trend-hint">点击图例切换指标</span>
      <span v-if="points.length" class="trend-range">{{ rangeText }}</span>
    </div>
    <svg
      v-if="points.length"
      class="trend-svg"
      viewBox="0 0 680 220"
      role="img"
      aria-label="按天趋势"
    >
      <!-- 横向网格线 + Y 轴刻度（最大值 / 中值 / 0） -->
      <g>
        <line
          v-for="(g, i) in gridYs"
          :key="'g' + i"
          class="grid-line"
          :x1="PAD.l"
          :x2="W - PAD.r"
          :y1="g.y"
          :y2="g.y"
        />
        <text
          v-for="(g, i) in gridYs"
          :key="'gt' + i"
          class="axis-label"
          :x="PAD.l - 8"
          :y="g.y + 4"
          text-anchor="end"
        >{{ g.label }}</text>
      </g>
      <!-- X 轴日期标签：首 / 中 / 尾（点密集时全打会糊成一团） -->
      <text
        v-for="(x, i) in xTicks"
        :key="'x' + i"
        class="axis-label"
        :x="x.x"
        :y="H - 8"
        text-anchor="middle"
      >{{ x.label }}</text>
      <!-- 面积底只给当前首条激活的系列（多条面积会互相盖住） -->
      <polygon v-if="points.length > 1" class="area" :points="areaPoints" :style="{ fill: areaColor }" />
      <!-- 三条折线：non-scaling-stroke 保证 viewBox 缩放后线宽不跟着变 -->
      <polyline
        v-for="s in series"
        :key="s.key"
        :points="s.points"
        fill="none"
        :stroke="s.color"
        stroke-width="2"
        stroke-linejoin="round"
        stroke-linecap="round"
        vector-effect="non-scaling-stroke"
      />
      <!-- 数据点：原生 <title> 作 tooltip，零 JS 依赖 -->
      <g v-for="s in series" :key="'d' + s.key">
        <circle
          v-for="(p, i) in s.dots"
          :key="i"
          :cx="p.x"
          :cy="p.y"
          r="3"
          :fill="s.color"
        >
          <title>{{ p.date }} {{ s.label }} {{ p.value }}</title>
        </circle>
      </g>
      <!-- 点击热区：数据点 r=3 太小，给每个日期一个透明大圆承接点击。
           点趋势图上某一天 = 下钻看当天的逐条明细（与点员工行「明细」
           是两个互补的下钻入口）。锚在访问系列点上——它是主指标 -->
      <g v-for="(p, i) in hitDots" :key="'h' + i">
        <circle
          class="hit-dot"
          :cx="p.x"
          :cy="p.y"
          r="12"
          fill="transparent"
          @click="emit('pick', p.date)"
        >
          <title>查看 {{ p.date }} 的明细</title>
        </circle>
      </g>
    </svg>
    <p v-else class="trend-empty">所选时段暂无数据</p>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

// 一天一行：前三项是转化，后五项（带 targetId 的内容事件）是意向。
// 意向项用可选字段：图表缺项时按 0 画，避免 undefined 变成 NaN 坐标。
export interface TrendPoint {
  date: string
  visit: number
  call: number
  save: number
  projectView?: number
  serviceView?: number
  videoPlay?: number
  share?: number
  posterSave?: number
}

const props = defineProps<{ points: TrendPoint[] }>()
const emit = defineEmits<{ (e: 'pick', date: string): void }>()

// 画布几何：viewBox 坐标系，外层 width:100% 等比缩放
const W = 680
const H = 220
const PAD = { l: 44, r: 16, t: 16, b: 28 }
const plotW = W - PAD.l - PAD.r
const plotH = H - PAD.t - PAD.b

type SeriesKey =
  | 'visit'
  | 'call'
  | 'save'
  | 'projectView'
  | 'serviceView'
  | 'videoPlay'
  | 'share'
  | 'posterSave'
// 转化三色与汇总卡 / 明细标签同源（访问蓝 / 拨号绿 / 保存橙）；
// 后五项统一走紫青粉系，一眼能分出「转化」与「意向」两组
const seriesDef: { key: SeriesKey; label: string; color: string }[] = [
  { key: 'visit', label: '访问', color: '#409eff' },
  { key: 'call', label: '拨号', color: '#67c23a' },
  { key: 'save', label: '保存', color: '#e6a23c' },
  { key: 'projectView', label: '案例浏览', color: '#9254de' },
  { key: 'serviceView', label: '服务浏览', color: '#36cfc9' },
  { key: 'videoPlay', label: '视频播放', color: '#f759ab' },
  { key: 'share', label: '转发', color: '#ff9c6e' },
  { key: 'posterSave', label: '海报保存', color: '#597ef7' },
]

// 默认只画转化三线：八条全开在窄屏上会糊成一团，而多数时候看的就是这三条
const active = ref<SeriesKey[]>(['visit', 'call', 'save'])
function isOn(k: SeriesKey): boolean {
  return active.value.includes(k)
}
function toggle(k: SeriesKey) {
  if (isOn(k)) {
    // 不允许全部关掉：最后一个激活项点了也不生效（再点一次就变空图）
    if (active.value.length <= 1) return
    active.value = active.value.filter((x) => x !== k)
  } else {
    active.value = [...active.value, k]
  }
}

// 纵轴上限至少为 1：全零时分母不能是 0，且「有数据但都是 0」与「无数据」
// 在图上要有区别（前者画一条贴底的线，后者走空态文案）。
// 只按**当前激活**的系列取最大值：勾掉访问后纵轴应重新贴合其余线，
// 否则剩下的线会被压成贴底的一条直线（等于数据「消失」了）。
const maxValue = computed(() => {
  const vals: number[] = []
  for (const p of props.points) for (const k of active.value) vals.push(p[k] || 0)
  return Math.max(1, ...vals)
})

// 横坐标按索引均分（不是按日期）：缺事件的日期不占位，
// 由后端只回有事件的日期、前端按序连点，避免两边各写一套日期推导。
const xOf = (i: number) =>
  props.points.length <= 1 ? PAD.l + plotW / 2 : PAD.l + (i * plotW) / (props.points.length - 1)
const yOf = (v: number) => PAD.t + plotH - (v / maxValue.value) * plotH

// 只画当前激活的系列（顺序固定，不随勾选顺序变——否则线色会跳）
const series = computed(() =>
  seriesDef
    .filter((s) => isOn(s.key))
    .map((s) => {
      const dots = props.points.map((p, i) => ({
        x: xOf(i),
        y: yOf(p[s.key] || 0),
        date: p.date,
        value: p[s.key] || 0,
      }))
      return { ...s, dots, points: dots.map((d) => `${d.x},${d.y}`).join(' ') }
    })
)

// 面积底 = 折线 + 回到底边两个角的多边形（颜色跟随首条激活系列）
const areaPoints = computed(() => {
  const s = series.value[0]
  if (!s || props.points.length < 2) return ''
  const base = PAD.t + plotH
  const back = props.points.map((_, i) => `${xOf(i)},${base}`).reverse().join(' ')
  return `${s.points} ${back}`
})
const areaColor = computed(() => {
  const s = series.value[0]
  return s ? hexToRgba(s.color, 0.08) : 'transparent'
})
// 面积色用同色 8% 透明：写死蓝色在「只勾案例浏览」时会变成蓝配紫
function hexToRgba(hex: string, alpha: number): string {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

// 点击热区（每日期一个，不随系列重复）：锚在当前首条激活系列上，
// 全部关掉访问后仍能点到（从前锚死在 visit 上，勾掉访问就点不动了）
const hitDots = computed(() => {
  const s = series.value[0]
  if (!s) return []
  return s.dots.map((d) => ({ x: d.x, y: d.y, date: d.date }))
})

const gridYs = computed(() => {
  const m = maxValue.value
  return [
    { y: PAD.t, label: String(m) },
    { y: PAD.t + plotH / 2, label: String(Math.round(m / 2)) },
    { y: PAD.t + plotH, label: '0' },
  ]
})

const xTicks = computed(() => {
  const n = props.points.length
  if (n === 0) return []
  const idx = n === 1 ? [0] : n === 2 ? [0, 1] : [0, Math.floor((n - 1) / 2), n - 1]
  // 只显示月-日：x 轴空间有限，且范围已由上方文字说明
  return idx.map((i) => ({ x: xOf(i), label: props.points[i].date.slice(5) }))
})

const rangeText = computed(() => {
  const n = props.points.length
  if (!n) return ''
  return n === 1 ? props.points[0].date : `${props.points[0].date} ~ ${props.points[n - 1].date}`
})
</script>

<style scoped>
.stat-trend {
  margin: 4px 0 16px;
}
.trend-legend {
  display: flex;
  align-items: center;
  gap: 12px 16px;
  flex-wrap: wrap; /* 八项图例 + 提示 + 区间，窄屏要能换行而不是挤出容器 */
  margin-bottom: 4px;
  font-size: 13px;
  color: #606266;
}
.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  user-select: none;
  transition: opacity 0.15s;
}
/* 未勾选的指标留着色号但压暗：用户能看出「还有哪些可勾」 */
.legend-off {
  opacity: 0.38;
}
.trend-hint {
  color: #c0c4cc;
  font-size: 12px;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
}
.trend-range {
  margin-left: auto;
  color: #909399;
  font-size: 12px;
}
.trend-svg {
  width: 100%;
  height: auto;
  display: block;
}
.grid-line {
  stroke: #ebeef5;
  stroke-width: 1;
}
.axis-label {
  fill: #909399;
  font-size: 11px;
}
/* fill 由内联样式按当前首条激活系列的色号给出（写死蓝在只勾意向时会配色打架） */
.area {
  stroke: none;
}
.hit-dot {
  cursor: pointer;
}
.trend-empty {
  margin: 8px 0;
  padding: 24px 0;
  text-align: center;
  color: #909399;
  font-size: 13px;
  background: #fafcff;
  border: 1px dashed #ebeef5;
  border-radius: 6px;
}
</style>
