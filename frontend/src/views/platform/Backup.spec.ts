import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, inject, provide, nextTick } from 'vue'

vi.mock('@/api/backup', async (orig) => {
  const actual = (await orig()) as Record<string, unknown>
  return {
    ...actual,
    getBackupStatus: vi.fn(),
    runBackup: vi.fn(),
    downloadBackup: vi.fn(),
  }
})
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))
vi.mock('@element-plus/icons-vue', () => ({
  Refresh: {},
  Download: {},
  FolderAdd: {},
  QuestionFilled: {},
}))

import { ElMessage } from 'element-plus'
import {
  describeAge,
  downloadBackup,
  formatBytes,
  getBackupStatus,
  runBackup,
  type BackupStatus,
} from '@/api/backup'
import Backup from './Backup.vue'

// ── 迷你表格桩 ────────────────────────────────────────────────
// 这页的核心断言是「缺件也要占一行并显示缺失」，而 el-table-column 的 scoped slot
// 需要 { row }。用一个按行 provide 的包装组件模拟，就能真渲染单元格文字；
// 只 stub 成空 div 的话，页面上最该被看见的信息恰好断言不到。
const RowKey = 'bkRow'
const RowWrap = defineComponent({
  props: { row: { type: Object, default: () => ({}) } },
  setup(props, { slots }) {
    provide(RowKey, props)
    return () => h('div', { class: 'row' }, slots.default?.())
  },
})
const ElTable = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  setup(props, { slots }) {
    return () =>
      h(
        'div',
        { class: 'el-table' },
        (props.data as any[]).map((r) =>
          h(RowWrap, { row: r }, { default: () => slots.default?.() }),
        ),
      )
  },
})
const ElTableColumn = defineComponent({
  setup(_, { slots }) {
    const row = inject<{ row: any } | null>(RowKey, null)
    return () =>
      h('div', { class: 'cell' }, slots.default ? slots.default({ row: row?.row }) : [])
  },
})
const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['title', 'description', 'type', 'closable'],
  template: `<div class="el-alert" :data-type="type">{{ title }} {{ description }}<slot /></div>`,
})
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['type', 'size', 'effect'],
  template: `<span class="el-tag" :data-type="type"><slot /></span>`,
})
const ElTooltip = defineComponent({
  name: 'ElTooltip',
  props: ['content'],
  template: `<span class="el-tooltip" :data-tip="content"><slot /></span>`,
})
const ElIcon = defineComponent({ name: 'ElIcon', template: `<i class="el-icon"><slot /></i>` })
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'disabled', 'link', 'icon'],
  emits: ['click'],
  template: `<button class="el-button" :disabled="disabled" @click="$emit('click')"><slot /></button>`,
})

const mountOpts = {
  global: {
    stubs: {
      'el-card': ElCard,
      'el-alert': ElAlert,
      'el-table': ElTable,
      'el-table-column': ElTableColumn,
      'el-tag': ElTag,
      'el-tooltip': ElTooltip,
      'el-icon': ElIcon,
      'el-button': ElButton,
    },
  },
}

function status(over: Partial<BackupStatus> = {}): BackupStatus {
  return {
    dir: '/app/backups',
    driver: 'sqlite',
    canRun: true,
    running: false,
    freshHours: 26,
    items: [],
    lastRun: null,
    notes: [],
    automaticTip: '自动备份需在服务器配置 crontab（见 docs/deploy-runbook.md §9.2）',
    ...over,
  }
}

function dbItem(over: Record<string, unknown> = {}) {
  return {
    kind: 'db',
    label: '数据库',
    file: 'weichuan-20261102-030000.db.gz',
    bytes: 266240,
    mtime: '2026-11-02T03:00:00+08:00',
    ageHours: 2,
    fresh: true,
    ...over,
  }
}

function uploadsItem(over: Record<string, unknown> = {}) {
  return {
    kind: 'uploads',
    label: '上传文件（图片）',
    file: 'uploads-20261102-030000.tar.gz',
    bytes: 1024 * 1024,
    mtime: '2026-11-02T03:00:00+08:00',
    ageHours: 2,
    fresh: true,
    ...over,
  }
}

function secretsItem(over: Record<string, unknown> = {}) {
  return {
    kind: 'secrets',
    label: '运行时密钥',
    file: 'secrets-20261102-030000.tar.gz.enc',
    bytes: 4096,
    mtime: '2026-11-02T03:00:00+08:00',
    ageHours: 2,
    fresh: true,
    ...over,
  }
}

async function setup(s: BackupStatus) {
  vi.mocked(getBackupStatus).mockResolvedValue(s)
  const w = mount(Backup, mountOpts)
  await flushPromises()
  await nextTick()
  return w
}

function btn(w: any, text: string) {
  return w.findAll('button.el-button').find((b: any) => b.text().includes(text))!
}

describe('Backup 备份与恢复面板', () => {
  beforeEach(() => {
    vi.mocked(getBackupStatus).mockReset()
    vi.mocked(runBackup).mockReset()
    vi.mocked(downloadBackup).mockReset()
    vi.mocked(runBackup).mockResolvedValue({ started: true })
    vi.mocked(downloadBackup).mockResolvedValue(undefined)
    vi.mocked(ElMessage.success).mockClear()
    vi.mocked(ElMessage.error).mockClear()
    vi.mocked(ElMessage.info).mockClear()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('三件套固定三行：没有的那件显示「缺失」而不是消失', async () => {
    const w = await setup(status({ items: [dbItem()] }))
    const rows = w.findAll('.el-table .row')
    expect(rows).toHaveLength(3)
    expect(rows[0].text()).toContain('数据库')
    expect(rows[0].text()).toContain('weichuan-20261102-030000.db.gz')
    // 图片与密钥都没有 → 各自占一行且明确写「缺失」
    expect(rows[1].text()).toContain('上传文件')
    expect(rows[1].text()).toContain('缺失')
    expect(rows[2].text()).toContain('运行时密钥')
    expect(rows[2].text()).toContain('缺失')
  })

  it('后端 notes 原样呈现在页面上（缺件原因不能被折叠）', async () => {
    const w = await setup(
      status({ items: [dbItem()], notes: ['缺少「运行时密钥」备份：它刻意不在后台生成'] }),
    )
    expect(w.text()).toContain('缺少「运行时密钥」备份')
    // 三件套的说明必须点明「缺一件救不回来」的后果，否则用户不知道密钥包为何重要
    expect(w.text()).toContain('缺一件就救不回来')
    expect(w.text()).toContain('读不出内容')
  })

  it('密钥包行没有下载按钮，并说明该从服务器取', async () => {
    const w = await setup(status({ items: [dbItem(), uploadsItem(), secretsItem()] }))
    const rows = w.findAll('.el-table .row')
    expect(rows[2].text()).toContain('不提供下载')
    // 判据是「没有按钮」，不是「文字里没有下载」——「不提供下载」本身就含这两个字
    expect(rows[2].find('button.el-button').exists()).toBe(false)
    // 库与图片可以下载
    expect(rows[0].find('button.el-button').exists()).toBe(true)
    expect(rows[1].find('button.el-button').exists()).toBe(true)
    // 该行有两个 tooltip（类别说明 + 下载说明），取后者
    const tips = rows[2].findAll('.el-tooltip').map((t) => t.attributes('data-tip') || '')
    expect(tips.some((t) => t.includes('scp'))).toBe(true)
  })

  it('点「下载」按文件名取产物（不是拿整页状态）', async () => {
    const w = await setup(status({ items: [dbItem()] }))
    await btn(w, '下载').trigger('click')
    await flushPromises()
    expect(vi.mocked(downloadBackup)).toHaveBeenCalledWith('weichuan-20261102-030000.db.gz')
    expect(vi.mocked(ElMessage.success)).toHaveBeenCalled()
  })

  it('MySQL 形态：立即备份不可点，并说明该去服务器跑脚本', async () => {
    const w = await setup(status({ driver: 'mysql', canRun: false }))
    const b = btn(w, '立即备份')
    expect(b.attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('mysqldump')
    // 不可点还要点：不该发请求
    await b.trigger('click')
    await flushPromises()
    expect(vi.mocked(runBackup)).not.toHaveBeenCalled()
  })

  it('立即备份 → 轮询到 running=false 才提示完成', async () => {
    vi.useFakeTimers()
    vi.mocked(getBackupStatus).mockReset()
    vi.mocked(getBackupStatus)
      .mockResolvedValueOnce(status({ items: [dbItem()] }))
      // 触发后第一次轮询：仍在跑
      .mockResolvedValueOnce(status({ running: true }))
      // 第二次轮询：跑完了，带结果
      .mockResolvedValueOnce(
        status({
          items: [dbItem(), uploadsItem()],
          lastRun: { at: '2026-11-02T03:05:00+08:00', ok: true, message: '已生成 2 个文件' },
        }),
      )

    const w = mount(Backup, mountOpts)
    await flushPromises()
    await btn(w, '立即备份').trigger('click')
    await flushPromises()
    expect(vi.mocked(runBackup)).toHaveBeenCalledTimes(1)
    // 第一轮还在跑：不能提前报「完成」
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(vi.mocked(ElMessage.success)).not.toHaveBeenCalledWith('备份完成')

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(vi.mocked(ElMessage.success)).toHaveBeenCalledWith('备份完成')
    expect(w.text()).toContain('已生成 2 个文件')
    w.unmount()
  })

  it('手动备份失败：红条留在页面上，不只弹一次 toast', async () => {
    const w = await setup(
      status({
        items: [dbItem()],
        lastRun: { at: '2026-11-02T03:05:00+08:00', ok: false, message: '打包上传目录失败：no space left' },
      }),
    )
    const alert = w.findAll('.el-alert').find((a) => a.text().includes('最近一次手动备份'))!
    expect(alert.attributes('data-type')).toBe('error')
    expect(alert.text()).toContain('no space left')
  })

  it('打开页面时若任务已在跑（别人触发的），接着盯而不是显示空闲', async () => {
    vi.useFakeTimers()
    vi.mocked(getBackupStatus).mockReset()
    vi.mocked(getBackupStatus)
      .mockResolvedValueOnce(status({ running: true }))
      .mockResolvedValueOnce(
        status({ lastRun: { at: '2026-11-02T03:05:00+08:00', ok: true, message: 'ok' } }),
      )

    const w = mount(Backup, mountOpts)
    await flushPromises()
    expect(btn(w, '备份中').exists()).toBe(true)
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(vi.mocked(ElMessage.success)).toHaveBeenCalledWith('备份完成')
    w.unmount()
  })
})

describe('备份面板的格式化工具', () => {
  it('formatBytes 按量级收敛，不摊开字节数', () => {
    expect(formatBytes(0)).toBe('—')
    expect(formatBytes(-1)).toBe('—')
    expect(formatBytes(900)).toBe('900 B')
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(266240)).toBe('260 KB')
    expect(formatBytes(5 * 1024 * 1024 * 1024)).toBe('5.0 GB')
  })

  it('describeAge 回答「还新鲜吗」，而不是丢一个时间戳', () => {
    expect(describeAge(0)).toBe('1 小时内')
    expect(describeAge(5)).toBe('5 小时前')
    expect(describeAge(50)).toBe('2 天前')
    expect(describeAge(-1)).toBe('—')
  })
})
