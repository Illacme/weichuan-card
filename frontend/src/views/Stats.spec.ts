import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick, defineComponent, h, provide, inject } from 'vue'
import Stats from './Stats.vue'

// 名片统计页依赖 api/stats（汇总 + 按员工 + 趋势 + 导出）与 element-plus 的消息/确认框，全部 mock 掉。
// 表格复用 Employees.spec.ts 那套「行上下文」轻量 stub，避免 jsdom 下挂完整 Element Plus。
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))
import { ElMessage } from 'element-plus'

const mocks = vi.hoisted(() => ({
  // SOURCE_LABELS 与组件共用同一份映射，mock 必须透出，否则组件 import 到 undefined 会崩
  SOURCE_LABELS: { scan: '扫码', share_card: '转发', search: '搜索', poster: '海报', unknown: '未知', other: '其他' },
  adminStats: vi.fn(async () => ({
    summary: {
      visit: 12,
      call: 3,
      save: 5,
      projectView: 7,
      serviceView: 4,
      videoPlay: 2,
      share: 6,
      posterSave: 1,
    },
    byStaff: [
      // 五类事件分项都带上：表格「内容浏览」列取前三类之和，
      // 「裂变」列取后两类之和（张三 7/4，李四 6/3）
      {
        staffId: 1, name: '张三', title: '工程师', visit: 7, call: 2, save: 3,
        projectView: 4, serviceView: 2, videoPlay: 1, share: 3, posterSave: 1,
      },
      {
        staffId: 2, name: '李四', title: '销售', visit: 5, call: 1, save: 2,
        projectView: 3, serviceView: 2, videoPlay: 1, share: 3, posterSave: 0,
      },
    ],
    // 后端趋势一次给八项（前端图例可点选），这里带上意向项验透传
    trend: [
      {
        date: '2026-10-21', visit: 4, call: 1, save: 2,
        projectView: 2, serviceView: 1, videoPlay: 0, share: 3, posterSave: 1,
      },
      {
        date: '2026-10-22', visit: 8, call: 2, save: 3,
        projectView: 5, serviceView: 3, videoPlay: 2, share: 3, posterSave: 0,
      },
    ],
    topProjects: [
      { targetId: 3, name: '车间改造', count: 5 },
      { targetId: 4, name: '配电升级', count: 2 },
    ],
    topServices: [{ targetId: 9, name: '机电维保', count: 4 }],
    // 流量来源分布 + 裂变榜（迁移 0022 起）：来源占比 = count / sum(count)
    sourceDist: [
      { source: 'scan', count: 4 },
      { source: 'share_card', count: 5 },
      { source: 'search', count: 2 },
      { source: 'poster', count: 1 },
    ],
    fissionTop: [
      { staffId: 1, name: '张三', count: 3 },
      { staffId: 2, name: '李四', count: 2 },
    ],
  })),
  adminStatsDetail: vi.fn(async () => ({ list: [], total: 0, page: 1, size: 20 })),
  exportStatsCsv: vi.fn(async () => {}),
}))
const { adminStats, adminStatsDetail, exportStatsCsv } = mocks
vi.mock('@/api/stats', () => mocks)

const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'clearable'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :placeholder="placeholder" :value="modelValue"
    @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'link'],
  template: `<button class="el-button"><slot /></button>`,
})
const ElPagination = defineComponent({
  name: 'ElPagination',
  props: ['currentPage', 'pageSize', 'total', 'pageSizes', 'layout', 'background'],
  template: `<div class="el-pagination" />`,
})
// 日期范围选择器 stub：点击即发出一个固定范围，模拟运营选了「近 7 天」
const ElDatePicker = defineComponent({
  name: 'ElDatePicker',
  props: ['modelValue', 'type', 'shortcuts', 'valueFormat', 'clearable', 'unlinkPanels', 'rangeSeparator', 'startPlaceholder', 'endPlaceholder'],
  emits: ['update:modelValue'],
  template: `<div class="el-date-picker" @click="$emit('update:modelValue', ['2026-10-01', '2026-10-07'])" />`,
})

// 行上下文：ElTable 为每行包一层 TableRow 并 provide 行数据，ElTableColumn 注入后转发给 #default 槽
const TableRow = defineComponent({
  name: 'TableRow',
  props: ['row'],
  setup(props, { slots }) {
    provide('tableRow', props.row)
    return () => h('div', { class: 'el-table-row' }, slots.default ? slots.default() : [])
  },
})
const ElTable = defineComponent({
  name: 'ElTable',
  props: ['data', 'border', 'stripe'],
  emits: ['row-click'],
  setup(props, { slots, emit }) {
    return () =>
      h(
        'div',
        {
          class: 'el-table',
          // 点表格即当作点了第一行（TOP 榜靠 row-click 下钻，桩只需能发出事件）
          onClick: () => {
            const row = (props.data || [])[0]
            if (row) emit('row-click', row)
          },
        },
        (props.data || []).map((row: any, i: number) =>
          h(TableRow, { key: i, row }, () => (slots.default ? slots.default() : null))
        )
      )
  },
})
const ElTableColumn = defineComponent({
  name: 'ElTableColumn',
  props: ['prop', 'label', 'width', 'align', 'sortable'],
  setup(props, { slots }) {
    const row = inject('tableRow', null) as any
    return () => {
      const content = slots.default
        ? slots.default({ row })
        : props.prop && row
          ? String(row[props.prop] ?? '')
          : ''
      return h('div', { class: 'el-table-column' }, content)
    }
  },
})

// 趋势图组件有自己的单测（StatTrend.spec.ts），这里桩成只透传 props 的壳
const StatTrendStub = defineComponent({
  name: 'StatTrend',
  props: ['points'],
  emits: ['pick'],
  template: `<div class="stat-trend-stub" />`,
})

// 明细抽屉同样有自己的单测（StatDetail.spec.ts）；这里把 props 暴露到
// data 属性上，方便断言「打开时带了哪个员工 / 哪个事件类型 / 哪个对象」
const StatDetailStub = defineComponent({
  name: 'StatDetail',
  props: ['open', 'range', 'staffs', 'staffId', 'eventType', 'targetId', 'targetName'],
  emits: ['update:open'],
  template: `<div class="stat-detail-stub" :data-open="String(open)" :data-staff-id="String(staffId)"
    :data-event-type="String(eventType)" :data-target-id="String(targetId)" :data-target-name="String(targetName)" />`,
})

// 悬浮提示桩：只渲染默认槽，content 走 props（便于断言「内容浏览」「裂变」两列的拆分文案）
const ElTooltip = defineComponent({
  name: 'ElTooltip',
  props: ['content', 'placement'],
  template: `<span class="el-tooltip"><slot /></span>`,
})

// 图标容器桩（折叠区箭头用 <el-icon>），透传默认槽
const ElIcon = defineComponent({
  name: 'ElIcon',
  template: `<span class="el-icon"><slot /></span>`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-input': ElInput,
      'el-button': ElButton,
      'el-table': ElTable,
      'el-table-column': ElTableColumn,
      'el-pagination': ElPagination,
      'el-date-picker': ElDatePicker,
      'el-tooltip': ElTooltip,
      'el-icon': ElIcon,
      StatTrend: StatTrendStub,
      StatDetail: StatDetailStub,
    },
    directives: { loading: {} },
  },
}

// 等挂载后 onMounted → load() 的微任务链走完
async function flushLoad(_w: ReturnType<typeof mount>) {
  await nextTick()
  await nextTick()
  await nextTick()
}

// 页面上有三张表（TOP 案例 / TOP 服务 / 员工），按数据行特征区分：
// TOP 榜行带 targetId，员工行带 staffId
function tablesOf(w: ReturnType<typeof mount>) {
  const all = w.findAllComponents(ElTable)
  const rows = (t: (typeof all)[number]): any[] => t.props('data') || []
  return {
    // top = 有 targetId 的榜（案例/服务）；staff = 有 staffId 且带 visit 列的按员工表
    // （裂变榜也有 staffId，但无 visit 列，靠 visit 区分，否则会误匹配裂变表）
    top: all.filter((t) => rows(t).some((r) => r.targetId !== undefined)), // 模板顺序：案例在前、服务在后
    staff: all.find((t) => rows(t).some((r) => r.staffId !== undefined && r.visit !== undefined))!,
  }
}

describe('Stats', () => {
  beforeEach(() => {
    vi.mocked(adminStats).mockClear()
    vi.mocked(adminStatsDetail).mockClear()
    vi.mocked(exportStatsCsv).mockClear()
    vi.mocked(ElMessage.error).mockClear()
  })

  it('挂载即加载：汇总三卡 + 按员工表格都渲染', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    expect(vi.mocked(adminStats)).toHaveBeenCalledTimes(1)
    // 首次加载不带日期范围（全部时间）
    expect(vi.mocked(adminStats)).toHaveBeenCalledWith({})
    // 汇总
    expect(w.text()).toContain('12') // 总访问
    expect(w.text()).toContain('3') // 拨号点击
    expect(w.text()).toContain('5') // 保存点击
    // 按员工明细（编号 / 姓名 / 三个计数）
    expect(w.text()).toContain('张三')
    expect(w.text()).toContain('李四')
    expect(w.text()).toContain('7')
    // 默认范围文案
    expect(w.text()).toContain('全部时间')
  })

  it('关键字按姓名 / 职位过滤表格数据', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    await w.find('input.el-input').setValue('销售')
    await nextTick()
    // 取员工表（页面现在有三张表，第一张是 TOP 案例榜）
    expect(tablesOf(w).staff.props('data')).toHaveLength(1)
    expect(tablesOf(w).staff.props('data')[0].name).toBe('李四')
    // 汇总卡片不受筛选影响（汇总来自接口，不是前端累加当前页）
    expect(w.text()).toContain('12')
  })

  it('员工列定义含编号/姓名/职位/访问/拨号/保存/内容浏览/裂变/操作九个列', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    // 注意：stub 的 ElTable 会为每一行各渲染一次列，故先去重再比对顺序；
    // 且页面现在有三张表，这里只取员工表（TOP 榜列是另一套）
    const labels = [
      ...new Set(tablesOf(w).staff.findAllComponents(ElTableColumn).map((c) => c.props('label'))),
    ]
    expect(labels).toEqual([
      '编号',
      '姓名',
      '职位',
      '访问',
      '拨号',
      '保存',
      '内容浏览',
      '裂变',
      '操作',
    ])
  })

  it('选日期范围后按 from/to 重新查询，范围文案同步更新', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)
    expect(vi.mocked(adminStats)).toHaveBeenCalledTimes(1)

    await w.find('.el-date-picker').trigger('click')
    await flushLoad(w)

    expect(vi.mocked(adminStats)).toHaveBeenCalledTimes(2)
    expect(vi.mocked(adminStats)).toHaveBeenLastCalledWith({
      from: '2026-10-01',
      to: '2026-10-07',
    })
    expect(w.text()).toContain('2026-10-01 至 2026-10-07')
  })

  it('清空日期范围回到全部时间口径', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    await w.findComponent(ElDatePicker).vm.$emit('update:modelValue', ['2026-10-01', '2026-10-07'])
    await flushLoad(w)
    expect(vi.mocked(adminStats)).toHaveBeenLastCalledWith({ from: '2026-10-01', to: '2026-10-07' })

    await w.findComponent(ElDatePicker).vm.$emit('update:modelValue', null)
    await flushLoad(w)
    expect(vi.mocked(adminStats)).toHaveBeenLastCalledWith({})
    expect(w.text()).toContain('全部时间')
  })

  it('点导出把当前日期范围传给导出接口', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    await w.findComponent(ElDatePicker).vm.$emit('update:modelValue', ['2026-10-01', '2026-10-07'])
    await flushLoad(w)

    const exportBtn = w.findAll('button').find((b) => b.text() === '导出')
    expect(exportBtn).toBeTruthy()
    await exportBtn!.trigger('click')
    await nextTick()
    expect(vi.mocked(exportStatsCsv)).toHaveBeenCalledWith({
      from: '2026-10-01',
      to: '2026-10-07',
    })
  })

  it('趋势数据透传给趋势图组件', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    const trend = w.findComponent(StatTrendStub)
    expect(trend.exists()).toBe(true)
    expect(trend.props('points')).toHaveLength(2)
    expect(trend.props('points')[0].date).toBe('2026-10-21')
  })

  it('导出失败时弹出接口返回的错误信息（不静默）', async () => {
    vi.mocked(exportStatsCsv).mockRejectedValueOnce(new Error('登录已失效，请重新登录'))
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    const exportBtn = w.findAll('button').find((b) => b.text() === '导出')
    await exportBtn!.trigger('click')
    await nextTick()
    await nextTick()
    expect(vi.mocked(ElMessage.error)).toHaveBeenCalledWith('登录已失效，请重新登录')
  })

  it('点员工行「明细」打开明细抽屉并带上该员工', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    const rowBtn = w.findAll('button').find((b) => b.text() === '明细')
    expect(rowBtn).toBeTruthy()
    await rowBtn!.trigger('click')
    await nextTick()

    const drawer = w.findComponent(StatDetailStub)
    expect(drawer.props('open')).toBe(true)
    expect(drawer.props('staffId')).toBe(1) // 第一行是张三
    // 日期范围随页面当前口径传给抽屉（默认全部时间 = 空对象）
    expect(drawer.props('range')).toEqual({})
    // 员工下拉选项取全量 rows（不受关键字筛选影响）
    expect(drawer.props('staffs')).toHaveLength(2)
  })

  it('点「数据明细」按钮打开抽屉（不带员工筛选）', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    const btn = w.findAll('button').find((b) => b.text() === '数据明细')
    expect(btn).toBeTruthy()
    await btn!.trigger('click')
    await nextTick()

    const drawer = w.findComponent(StatDetailStub)
    expect(drawer.props('open')).toBe(true)
    expect(drawer.props('staffId')).toBe(0)
  })

  it('趋势图点某天：范围收窄到当天 + 打开明细抽屉', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)
    expect(vi.mocked(adminStats)).toHaveBeenCalledTimes(1)

    await w.findComponent(StatTrendStub).vm.$emit('pick', '2026-10-22')
    await flushLoad(w)

    // 汇总按当天重查
    expect(vi.mocked(adminStats)).toHaveBeenLastCalledWith({
      from: '2026-10-22',
      to: '2026-10-22',
    })
    // 范围文案同步
    expect(w.text()).toContain('2026-10-22 至 2026-10-22')
    // 抽屉打开，且日期范围收窄到当天——明细与汇总是同一个口径
    const drawer = w.findComponent(StatDetailStub)
    expect(drawer.props('open')).toBe(true)
    expect(drawer.props('range')).toEqual({ from: '2026-10-22', to: '2026-10-22' })
  })

  it('改页面日期范围时，抽屉拿到的范围同步更新（保持同口径）', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    await w.findComponent(ElDatePicker).vm.$emit('update:modelValue', ['2026-10-01', '2026-10-07'])
    await flushLoad(w)
    expect(w.findComponent(StatDetailStub).props('range')).toEqual({
      from: '2026-10-01',
      to: '2026-10-07',
    })
  })

  // 「内容浏览」只含三类指向上内容对象的事件；转发/海报保存进「裂变动作」。
  // 这个分组是口径修正的核心：混在一起时「访客对什么内容感兴趣」答不出来，
  // 且会把内容浏览总量抬高（海报保存的主体很可能是员工本人）。
  it('内容浏览三卡与裂变两卡分成两组渲染，互不串台', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    const blocks = w.findAll('.intent-block')
    expect(blocks).toHaveLength(2)

    const browse = blocks[0].text()
    expect(browse).toContain('内容浏览')
    expect(browse).toContain('案例浏览')
    expect(browse).toContain('服务浏览')
    expect(browse).toContain('视频播放')
    // 数值取自汇总（7/4/2）
    expect(browse).toContain('7')
    expect(browse).toContain('4')
    expect(browse).toContain('2')
    // 转发/海报保存绝不能出现在内容浏览组里——否则口径又混回去了
    expect(browse).not.toContain('转发')
    expect(browse).not.toContain('海报保存')

    const fission = blocks[1].text()
    expect(fission).toContain('裂变动作')
    expect(fission).toContain('转发')
    expect(fission).toContain('海报保存')
    expect(fission).not.toContain('案例浏览')
    expect(fission).not.toContain('服务浏览')

    expect(browse + fission).not.toContain('undefined')
  })

  it('TOP 榜渲染案例与服务两张榜（名称 + 次数）', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    const { top } = tablesOf(w)
    expect(top).toHaveLength(2) // 案例榜 + 服务榜
    expect(top[0].props('data')).toEqual([
      { targetId: 3, name: '车间改造', count: 5 },
      { targetId: 4, name: '配电升级', count: 2 },
    ])
    expect(top[1].props('data')).toEqual([{ targetId: 9, name: '机电维保', count: 4 }])
    // 榜上文字可见（案例名 / 服务名）
    expect(w.text()).toContain('车间改造')
    expect(w.text()).toContain('机电维保')
    expect(w.text()).toContain('TOP 案例浏览')
    expect(w.text()).toContain('TOP 服务浏览')
  })

  it('点 TOP 案例某行下钻：抽屉预选 eventType=project_view + 该案例', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    await tablesOf(w).top[0].trigger('click')
    await nextTick()

    const drawer = w.findComponent(StatDetailStub)
    expect(drawer.props('open')).toBe(true)
    expect(drawer.props('eventType')).toBe('project_view')
    expect(drawer.props('targetId')).toBe(3)
    expect(drawer.props('targetName')).toBe('车间改造')
    // 榜单是租户级聚合，下钻不筛员工——否则看不到「这个案例吸引了多少人」
    expect(drawer.props('staffId')).toBe(0)
  })

  it('点 TOP 服务某行下钻：抽屉预选 service_view + 该服务', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    await tablesOf(w).top[1].trigger('click')
    await nextTick()

    const drawer = w.findComponent(StatDetailStub)
    expect(drawer.props('eventType')).toBe('service_view')
    expect(drawer.props('targetId')).toBe(9)
    expect(drawer.props('targetName')).toBe('机电维保')
  })

  it('员工行「明细」不带事件/对象筛选（不残留上次下钻的预选）', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    // 先下钻一次，再回到员工行——筛选必须被清干净
    await tablesOf(w).top[0].trigger('click')
    await nextTick()
    const rowBtn = w.findAll('button').find((b) => b.text() === '明细')!
    await rowBtn.trigger('click')
    await nextTick()

    const drawer = w.findComponent(StatDetailStub)
    expect(drawer.props('staffId')).toBe(1)
    expect(drawer.props('eventType')).toBe('')
    expect(drawer.props('targetId')).toBe(0)
  })

  it('员工表「内容浏览」列是三浏览之和、「裂变」列是转发+海报，各自悬浮拆分', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    // 张三：浏览 4+2+1 = 7，裂变 3+1 = 4
    const rows = tablesOf(w).staff.props('data') as any[]
    expect(rows[0].browse).toBe(7)
    expect(rows[0].fission).toBe(4)
    // 李四：浏览 3+2+1 = 6，裂变 3+0 = 3
    expect(rows[1].browse).toBe(6)
    expect(rows[1].fission).toBe(3)

    // 单元格渲染的是合计数
    const cells = w.findAll('.intent-cell').map((c) => c.text())
    expect(cells).toContain('7')
    expect(cells).toContain('4')

    // 两列的悬浮文案各自只给本组拆分（省掉五列表格，但不许互相串）
    const tips = w.findAllComponents(ElTooltip).map((t) => t.props('content'))
    expect(tips).toContain('案例浏览 4 · 服务浏览 2 · 视频播放 1')
    expect(tips).toContain('转发 3 · 海报保存 1')
    // 「内容浏览」的提示里不能混进裂变项
    expect(tips.some((s) => typeof s === 'string' && s.includes('案例浏览') && s.includes('海报保存'))).toBe(false)
  })

  it('TOP 榜无数据时显示空态文案（不像加载失败）', async () => {
    vi.mocked(adminStats).mockResolvedValueOnce({
      summary: {
        visit: 0, call: 0, save: 0,
        projectView: 0, serviceView: 0, videoPlay: 0, share: 0, posterSave: 0,
      },
      byStaff: [],
      trend: [],
      topProjects: [],
      topServices: [],
      sourceDist: [],
      fissionTop: [],
    })
    const w = mount(Stats, mountOpts)
    await flushLoad(w)

    expect(w.text()).toContain('范围内暂无案例浏览')
    expect(w.text()).toContain('范围内暂无服务浏览')
    // 来源分布 / 裂变榜同样空态文案，不抛
    expect(w.text()).toContain('范围内暂无访问数据')
    expect(w.text()).toContain('范围内暂无转发带来的访问')
  })

  it('内容浏览与裂变折叠区默认收起，点标题展开（方案A 同页分区）', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)
    // 默认收起：v-show 把 fold-body 设为 display:none
    expect((w.find('.fold-intent .fold-body').attributes('style') || '')).toContain('display: none')
    // 点分区标题展开
    await w.find('.fold-intent .fold-head').trigger('click')
    await nextTick()
    expect((w.find('.fold-intent .fold-body').attributes('style') || '')).not.toContain('display: none')
    // 展开后两组卡片与 TOP 榜仍在 DOM（数据渲染不依赖折叠态）
    expect(w.findAll('.intent-block')).toHaveLength(2)
    expect(tablesOf(w).top).toHaveLength(2)
    expect(w.find('.fold-intent').classes()).toContain('open')
  })

  it('按员工折叠区默认收起，展开后含员工表', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)
    expect((w.find('.fold-staff .fold-body').attributes('style') || '')).toContain('display: none')
    await w.find('.fold-staff .fold-head').trigger('click')
    await nextTick()
    expect((w.find('.fold-staff .fold-body').attributes('style') || '')).not.toContain('display: none')
    expect(tablesOf(w).staff.exists()).toBe(true)
    expect(w.find('.fold-staff').classes()).toContain('open')
  })

  it('流量来源与裂变折叠区：默认收起，展开后渲染来源分布条 + 裂变榜', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)
    // 默认收起
    expect((w.find('.fold-source .fold-body').attributes('style') || '')).toContain('display: none')
    await w.find('.fold-source .fold-head').trigger('click')
    await nextTick()
    expect((w.find('.fold-source .fold-body').attributes('style') || '')).not.toContain('display: none')
    expect(w.find('.fold-source').classes()).toContain('open')
    // 来源分布：四条来源各一行（scan/share_card/search/poster），标签用中文映射
    const rows = w.findAll('.src-row')
    expect(rows).toHaveLength(4)
    // 第一条来源标签应是「扫码」（SOURCE_LABELS.scan），计数为 4
    expect(w.find('.src-label').text()).toBe('扫码')
    expect(w.find('.src-count').text()).toBe('4')
    // 裂变榜：两名员工各一行，含「张三 / 3」「李四 / 2」
    const fis = w.findAll('.fission-block .el-table-row')
    expect(fis).toHaveLength(2)
    const fisText = w.find('.fission-block').text()
    expect(fisText).toContain('张三')
    expect(fisText).toContain('李四')
    expect(fisText).toContain('3')
  })

  it('来源分布占比按访问总量计算（条码宽度 = count / 总访问）', async () => {
    const w = mount(Stats, mountOpts)
    await flushLoad(w)
    await w.find('.fold-source .fold-head').trigger('click')
    await nextTick()
    // 总量 = 4+5+2+1 = 12；scan 行占比 = 4/12 ≈ 33%
    const bar = w.find('.src-row .src-bar')
    expect(bar.attributes('style')).toContain('width: 33%')
  })
})
