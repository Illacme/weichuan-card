import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

// 服务端路径选择器：守两类回归
//  1. 后端数组字段为 null（Go nil slice 序列化成 null）时，模板里的 `.length` 会抛异常，
//     整个弹窗渲染失败 → 用户看到的是「点浏览按钮没反应」。这里把 null 当常态守住。
//  2. 弹窗只应在 visible 变 true 时拉一次数据（曾因 watch immediate + @open 双重触发，
//     页面一挂载就发 6 个请求，且把未打开时的状态写成脏数据）。
vi.mock('@/api/settings', () => ({
  browseServerPaths: vi.fn(),
}))

import { browseServerPaths } from '@/api/settings'
import PathPickerDialog from './PathPickerDialog.vue'

const ElDialog = defineComponent({
  name: 'ElDialog',
  props: ['modelValue', 'title', 'width'],
  emits: ['update:modelValue'],
  template: `<div class="el-dialog" v-if="modelValue"><div class="el-dialog__title">{{ title }}</div><slot /><div class="el-dialog__footer"><slot name="footer" /></div></div>`,
})
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'size'],
  emits: ['update:modelValue'],
  template: `<input class="el-input" :value="modelValue" :placeholder="placeholder" @input="$emit('update:modelValue', $event.target.value)" />`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'size', 'text', 'disabled'],
  template: `<button class="el-button" :disabled="disabled"><slot /></button>`,
})
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['size', 'type', 'effect'],
  template: `<span class="el-tag"><slot /></span>`,
})

function payload(over: Record<string, unknown> = {}) {
  return {
    code: 0,
    data: {
      kind: 'source_dir',
      path: '/opt',
      selected: '/opt',
      selectedIsDir: true,
      parent: '/',
      home: '/home/dev',
      entries: [],
      truncated: false,
      current: { ok: false, message: '未配置小程序源码目录（MP_SOURCE_DIR）' },
      suggestions: [],
      ...over,
    },
  }
}

function mountPicker(kind = 'source_dir', modelValue = '') {
  return mount(PathPickerDialog, {
    props: { kind, modelValue, visible: false },
    global: {
      stubs: {
        'el-dialog': ElDialog,
        'el-input': ElInput,
        'el-button': ElButton,
        'el-tag': ElTag,
      },
      directives: { loading: {} },
    },
  })
}

// 打开弹窗（触发 watch → 拉数据）
async function open(w: ReturnType<typeof mountPicker>) {
  await w.setProps({ visible: true })
  await flushPromises()
}

function buttonByText(w: ReturnType<typeof mountPicker>, text: string) {
  return w.findAll('.el-button').find((b) => b.text().includes(text))!
}

describe('PathPickerDialog 服务端路径选择器', () => {
  beforeEach(() => {
    vi.mocked(browseServerPaths).mockReset()
  })

  it('打开时只请求一次（不在挂载时就请求）', async () => {
    vi.mocked(browseServerPaths).mockResolvedValue(payload() as any)
    const w = mountPicker('source_dir')
    await flushPromises()
    // visible=false 期间不该发请求：设置页有 6 个 pathKind 项，一挂载就发等于 6 个白跑
    expect(browseServerPaths).not.toHaveBeenCalled()

    await open(w)
    expect(browseServerPaths).toHaveBeenCalledTimes(1)
    expect(vi.mocked(browseServerPaths).mock.calls[0][0]).toBe('source_dir')
  })

  it('以 visible=true 直接挂载时也会加载（SettingRow 是「首次点开才挂载」）', async () => {
    // pickerReady 让弹窗在点开那一刻才创建，此时 visible 已经是 true：
    // 没有 immediate 就永远不触发 watch → 空白弹窗
    vi.mocked(browseServerPaths).mockResolvedValue(payload() as any)
    const w = mount(PathPickerDialog, {
      props: { kind: 'module_dir', modelValue: '', visible: true },
      global: {
        stubs: {
          'el-dialog': ElDialog,
          'el-input': ElInput,
          'el-button': ElButton,
          'el-tag': ElTag,
        },
        directives: { loading: {} },
      },
    })
    await flushPromises()
    expect(browseServerPaths).toHaveBeenCalledTimes(1)
    expect(w.find('.pp').exists()).toBe(true)
  })

  it('entries / suggestions 为 null 时正常渲染（后端空切片曾序列化成 null）', async () => {
    // Go 的 nil slice → JSON null；模板里 `suggestions.length` 会抛 TypeError，
    // 弹窗整块渲染失败，表现就是「点浏览按钮弹不出窗口」。
    vi.mocked(browseServerPaths).mockResolvedValue(
      payload({ entries: null, suggestions: null }) as any,
    )
    const w = mountPicker('module_dir')
    await open(w)
    expect(w.find('.pp').exists()).toBe(true)
    expect(w.find('.pp-list').text()).toContain('该目录下没有可选项')
    expect(w.find('.pp-suggest').exists()).toBe(false)
  })

  it('面包屑不出现双斜杠', async () => {
    // 旧实现先画一个根 "/"，再给每段补一个 "/"，于是 /Users/eason 显示成 //Users/eason
    vi.mocked(browseServerPaths).mockResolvedValue(payload({ path: '/Users/eason' }) as any)
    const w = mountPicker()
    await open(w)
    expect(w.find('.pp-crumbs').text().replace(/\s/g, '')).toBe('/Users/eason')
  })

  it('node_bin 停在目录上：给中性引导而不是红字报错，且不允许确认', async () => {
    vi.mocked(browseServerPaths).mockResolvedValue(
      payload({
        kind: 'node_bin',
        path: '/Users/eason',
        selected: '/Users/eason',
        selectedIsDir: true,
        current: { ok: false, message: '找不到 node 可执行文件（/Users/eason）：上传代码需要 Node 环境' },
      }) as any,
    )
    const w = mountPicker('node_bin', '/Users/eason')
    await open(w)

    const status = w.find('.pp-status')
    expect(status.classes()).toContain('info')
    expect(status.text()).toContain('请在下方列表点选 node 可执行文件')
    // 不能把「找不到 node 可执行文件」这种失败文案甩给正在浏览目录的用户
    expect(status.text()).not.toContain('找不到 node 可执行文件')
    expect(buttonByText(w, '选择此路径').attributes('disabled')).toBeDefined()
  })

  it('点选 node 文件后可以确认，并回填该文件路径', async () => {
    vi.mocked(browseServerPaths)
      // 第一次：列目录，目录本身不是可用的 node
      .mockResolvedValueOnce(
        payload({
          kind: 'node_bin',
          path: '/opt/bin',
          selected: '/opt/bin',
          selectedIsDir: true,
          entries: [{ name: 'node', path: '/opt/bin/node', type: 'file', marker: 'node 可执行文件' }],
          current: { ok: false, message: '找不到 node 可执行文件（/opt/bin）' },
        }) as any,
      )
      // 第二次：点选文件 → 列表仍停在父目录，但 selected 变成文件本身
      .mockResolvedValueOnce(
        payload({
          kind: 'node_bin',
          path: '/opt/bin',
          selected: '/opt/bin/node',
          selectedIsDir: false,
          entries: [{ name: 'node', path: '/opt/bin/node', type: 'file', marker: 'node 可执行文件' }],
          current: { ok: true, message: 'node 可执行文件可用（/opt/bin/node）' },
        }) as any,
      )
    const w = mountPicker('node_bin')
    await open(w)

    await w.find('.pp-entry').trigger('click')
    await flushPromises()
    expect(vi.mocked(browseServerPaths).mock.calls[1][1]).toBe('/opt/bin/node')
    expect(w.find('.pp-entry').classes()).toContain('active')
    expect(w.find('.pp-status').classes()).toContain('ok')

    await buttonByText(w, '选择此路径').trigger('click')
    expect(w.emitted('pick')![0]).toEqual(['/opt/bin/node'])
    expect(w.emitted('update:visible')!.at(-1)).toEqual([false])
  })

  it('请求失败时展示后端原因，且不给确认', async () => {
    vi.mocked(browseServerPaths).mockResolvedValue({ code: 1, msg: '该路径不在允许浏览的范围内' } as any)
    const w = mountPicker('source_dir', '/etc')
    await open(w)
    expect(w.find('.pp-status').classes()).toContain('error')
    expect(w.find('.pp-status').text()).toContain('该路径不在允许浏览的范围内')
    expect(buttonByText(w, '选择此路径').attributes('disabled')).toBeDefined()
  })

  it('候选行带完整路径，同名的 node 才能区分开', async () => {
    vi.mocked(browseServerPaths).mockResolvedValue(
      payload({
        kind: 'node_bin',
        suggestions: [
          { path: '/opt/homebrew/bin/node', label: 'node', reason: '在 PATH 中' },
          { path: '/usr/local/bin/node', label: 'node', reason: '常见安装位置' },
        ],
      }) as any,
    )
    const w = mountPicker('node_bin')
    await open(w)
    const items = w.findAll('.pp-suggest-item')
    expect(items).toHaveLength(2)
    expect(items[0].text()).toContain('/opt/homebrew/bin/node')
    expect(items[1].text()).toContain('/usr/local/bin/node')
  })
})
