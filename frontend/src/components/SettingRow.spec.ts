import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SettingRow from './SettingRow.vue'

// 守门测试：SettingRow 的控件分支是一条 v-if / v-else-if 链
// （bool → int → 可写敏感 → 其余文本）。历史上把「可写敏感」从 v-else-if
// 误写成 v-if，导致开关/整数行额外多渲染一个文本框（界面出现「60 / 60」两个框）。
// 这里对每种类型断言：控制区只出现一个输入类控件，防止分支链再次断裂。

const base = {
  key: 'k',
  name: '配置项',
  key_type: '',
  type: 'string',
  description: '',
  value: '',
  configured: false,
  restart: false,
  readOnly: false,
  secret: false,
} as any

function mountRow(item: Record<string, unknown>, modelValue = '') {
  return mount(SettingRow, {
    props: { item: { ...base, ...item }, modelValue },
    global: {
      stubs: {
        // stub 要渲染 placeholder：三态下拉的空值态靠它显示「未设置（…）」而不是
// Element Plus 通用「请选择」（EP 2.14 hasModelValue 对空串为 false，见组件注释）
'el-select': { name: 'ElSelect', template: '<select :placeholder="placeholder"><slot /></select>', props: ['placeholder', 'fallbackPlacements'] },
        'el-option': { template: '<option />' },
        'el-input-number': { template: '<input type="number" />' },
        // stub 要渲染 placeholder：示例占位符是「填什么格式」的引导，断言它就得看得见
        'el-input': { template: '<input type="text" :placeholder="placeholder" />', props: ['placeholder'] },
        'el-button': { template: '<button><slot /></button>' },
        'el-tag': { template: '<span><slot /></span>' },
        // tooltip 拍平：触发徽标 + 说明文本一起渲染，便于断言说明仍在 DOM
        'el-tooltip': {
          template: '<span class="el-tooltip"><slot /><span class="el-tooltip__content">{{ content }}</span></span>',
          props: ['content', 'placement'],
        },
        // stub 掉服务端路径选择器弹窗：它内部含输入框（用于「直接输入路径」），
        // 会干扰「行内只有一个输入控件」的断言；弹窗本身在测试中不开（visible=false）
        'PathPickerDialog': { template: '<span class="path-picker-stub" />' },
      },
    },
  })
}

describe('SettingRow 控件唯一性（防 v-if 链断裂回归）', () => {
  it('bool 行只渲染一个控件（三态下拉）', () => {
    const w = mountRow({ type: 'bool' })
    expect(w.find('.row-control').findAll('select, input')).toHaveLength(1)
  })

  it('三态下拉空值态显示「未设置（…）」而不是通用「请选择」', () => {
    // EP 2.14：hasModelValue 对空串为 false，不显式传 placeholder 会回落「请选择」，
    // 看着像必选项，与「留空=回退环境变量/缺省值」相反。bool 与枚举两条分支都要守。
    const withEnv = mountRow({ type: 'bool', envVar: 'CDN_ENABLED' })
    expect(withEnv.find('.row-control select').attributes('placeholder')).toBe('未设置（用部署环境的值）')
    const noEnv = mountRow({ type: 'bool' })
    expect(noEnv.find('.row-control select').attributes('placeholder')).toBe('未设置（用缺省值）')
    const enumRow = mountRow({ type: 'string', options: ['local', 'minio', 'oss', 's3'], envVar: 'STORAGE_DRIVER' })
    expect(enumRow.find('.row-control select').attributes('placeholder')).toBe('未设置（用部署环境的值）')
  })

  it('int 行只渲染一个控件（数字框）', () => {
    const w = mountRow({ type: 'int' }, '60')
    expect(w.find('.row-control').findAll('select, input')).toHaveLength(1)
  })

  it('string 行只渲染一个控件（文本框）', () => {
    const w = mountRow({ type: 'string' }, 'x')
    expect(w.find('.row-control').findAll('select, input')).toHaveLength(1)
  })

  it('可写敏感行：一个输入框 + 清除按钮，但仍只有一个输入控件', () => {
    const w = mountRow({ type: 'string', secret: true, configured: true }, 'x')
    expect(w.find('.row-control').findAll('input')).toHaveLength(1)
  })

  it('只读敏感行：走普通只读框显示预览，也只有一个输入控件', () => {
    const w = mountRow({ type: 'string', secret: true, readOnly: true }, 'preview')
    expect(w.find('.row-control').findAll('input')).toHaveLength(1)
  })

  it('格式易错项带悬浮说明徽标，普通项不带', () => {
    // 地址类项填错后果当场发现不了（图片打不开 / 小程序连不上），必须有说明徽标
    const w = mountRow({ key: 'upload_base_url', type: 'string' })
    expect(w.find('.row-hint').exists()).toBe(true)
    expect(w.text()).toContain('图片全部打不开')
    // 无提示的项不挂徽标：徽标一多就等于没有
    const plain = mountRow({ key: 'brand_name', type: 'string' })
    expect(plain.find('.row-hint').exists()).toBe(false)
  })

  it('格式关键项给示例占位，其余保持未设置', () => {
    const w = mountRow({ key: 'miniprogram_base_url', type: 'string' })
    expect(w.find('.row-control input').attributes('placeholder')).toContain('https://')
    const plain = mountRow({ key: 'brand_name', type: 'string' })
    expect(plain.find('.row-control input').attributes('placeholder')).toBe('未设置')
  })

  it('枚举项渲染为三态下拉：未设置 + 后端下发的候选值', () => {
    // storage_driver 后端有 oneOf 校验 + Options 候选：界面必须渲染下拉而不是
    // 文本框——候选值就摆在后端却让人手打，填错只能等保存时报错
    // （2026-10-22 用户实测：「存储方式还是填写字符串的方式，怎么没有下拉」）
    const w = mountRow({ key: 'storage_driver', type: 'string', options: ['local', 'minio', 'oss', 's3'] })
    expect(w.find('.row-control select').exists()).toBe(true)
    expect(w.find('.row-control input').exists()).toBe(false)
    // 未设置（回退环境变量）+ 4 个候选
    expect(w.findAll('.row-control option')).toHaveLength(5)
    // 下拉朝向必须限定 bottom/top：设置页的选择框贴内容区右缘，面板比右侧剩余
    // 空间宽时 popper.js 默认翻到 left，下拉整个跑到选择框左侧盖住标签列
    // （2026-10-23 用户实测「为什么点击下拉框后是在左侧弹出的」）。砍掉 left/right
    // 后最坏只是被平移到贴视口右缘，形态仍是「选择框正下方展开」。
    expect(w.findComponent({ name: 'ElSelect' }).props('fallbackPlacements')).toEqual(['bottom-start', 'top-start'])
    // 未登记 options 的普通字符串项仍是文本框
    const plain = mountRow({ key: 'brand_name', type: 'string' })
    expect(plain.find('.row-control select').exists()).toBe(false)
    expect(plain.find('.row-control input').exists()).toBe(true)
  })

  it('行内校验错误：传了才显示，不传不占版面', () => {
    // 配置向导在「下一步」前校验地址类项，错误显示在行内（主列表不传，靠后端拦）
    const w = mountRow({ key: 'upload_base_url', type: 'string' }, 'cdn.example.com')
    expect(w.find('.row-error').exists()).toBe(false)
    const w2 = mount(SettingRow, {
      props: {
        item: { ...base, key: 'upload_base_url', type: 'string' },
        modelValue: 'cdn.example.com',
        error: '不是完整地址，应类似 https://cdn.example.com',
      },
      global: {
        stubs: {
          // stub 要渲染 placeholder：三态下拉的空值态靠它显示「未设置（…）」而不是
// Element Plus 通用「请选择」（EP 2.14 hasModelValue 对空串为 false，见组件注释）
'el-select': { name: 'ElSelect', template: '<select :placeholder="placeholder"><slot /></select>', props: ['placeholder', 'fallbackPlacements'] },
          'el-option': { template: '<option />' },
          'el-input-number': { template: '<input type="number" />' },
          'el-input': { template: '<input type="text" :placeholder="placeholder" />', props: ['placeholder'] },
          'el-button': { template: '<button><slot /></button>' },
          'el-tag': { template: '<span><slot /></span>' },
          'el-tooltip': {
            template: '<span class="el-tooltip"><slot /><span class="el-tooltip__content">{{ content }}</span></span>',
            props: ['content', 'placement'],
          },
        },
      },
    })
    expect(w2.find('.row-error').text()).toContain('不是完整地址')
  })
})

describe('SettingRow 路径类配置（浏览服务器目录按钮）', () => {
  it('pathKind 项渲染文本框 + 浏览按钮，且仅一个输入控件', () => {
    // 后台上传小程序代码那三项（mp_node_bin / mp_ci_module_dir / mp_source_dir）
    // 是「服务端路径」类：文本框旁要出现「浏览…」按钮，但仍只一个输入框
    const w = mountRow({ key: 'mp_source_dir', type: 'string', pathKind: 'source_dir' }, '/opt/src')
    expect(w.find('.row-control input').exists()).toBe(true)
    expect(w.find('.path-browse').exists()).toBe(true)
    expect(w.find('.row-control').findAll('input')).toHaveLength(1)
  })

  it('非 pathKind 项不渲染浏览按钮', () => {
    const w = mountRow({ key: 'brand_name', type: 'string' })
    expect(w.find('.path-browse').exists()).toBe(false)
  })

  it('只读项即便带 pathKind 也不渲染浏览按钮', () => {
    const w = mountRow({ key: 'mp_source_dir', type: 'string', pathKind: 'source_dir', readOnly: true })
    expect(w.find('.path-browse').exists()).toBe(false)
  })
})
