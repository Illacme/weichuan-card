import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import WechatBindingCell from './WechatBindingCell.vue'

// 微信绑定可见性（P0）：
//  - 组件层：状态标签、掩码、窄表格 compact 三种表现；
//  - 页面层：三个账号页都必须接上同一组件，不能有的看得见有的看不见；
//  - 安全底线：管理员的 WXOpenID 仍标 json:"-"——「可见」不等于「把凭据原文发出去」。
//
// 页面层用读源码守门而不是给每个页面造全套 mount：Accounts.vue 没有现成的测试脚手架，
// 为一行状态列搭一整套 mock 不划算；但纯字符串断言会被注释骗过，所以先剥注释，
// 再配合 Companies.spec.ts / Employees.spec.ts 里的真实 DOM 断言（data-bound）兜底。
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['type', 'effect', 'size'],
  template: `<span class="el-tag" :data-type="type"><slot /></span>`,
})

type CellProps = { bound: boolean; masked?: string; compact?: boolean }

function cell(props: CellProps) {
  return mount(WechatBindingCell, { props, global: { stubs: { 'el-tag': ElTag } } })
}

function code(path: string): string {
  return readFileSync(resolve(process.cwd(), path), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

describe('WechatBindingCell · 展示', () => {
  it('未绑定显示灰色状态，不带掩码', () => {
    const w = cell({ bound: false, masked: '' })
    expect(w.text()).toContain('未绑定')
    expect(w.find('.el-tag').attributes('data-type')).toBe('info')
    expect(w.find('.wx-bind-mask').exists()).toBe(false)
    expect(w.attributes('data-bound')).toBe('false')
  })

  it('已绑定显示绿色状态与脱敏标识', () => {
    const w = cell({ bound: true, masked: 'o****d-a' })
    expect(w.text()).toContain('已绑定')
    expect(w.find('.el-tag').attributes('data-type')).toBe('success')
    expect(w.find('.wx-bind-mask').text()).toBe('o****d-a')
    expect(w.attributes('data-masked')).toBe('o****d-a')
    expect(w.attributes('data-bound')).toBe('true')
  })

  it('掩码为空时不渲染空 span，仍保留已绑定状态', () => {
    const w = cell({ bound: true, masked: '' })
    expect(w.text()).toContain('已绑定')
    expect(w.find('.wx-bind-mask').exists()).toBe(false)
  })

  it('compact 窄表格只露状态标签，不把掩码塞进窄列', () => {
    const w = cell({ bound: true, masked: 'o****d-a', compact: true })
    expect(w.text()).toContain('已绑定')
    expect(w.find('.wx-bind-mask').exists()).toBe(false)
    expect(w.find('.wx-bind-cell').classes()).toContain('is-compact')
    expect(w.attributes('data-masked')).toBe('')
  })
})

describe('WechatBindingCell · 三个账号页必须都接上', () => {
  const views = [
    { path: 'src/views/platform/Accounts.vue', masked: 'row.wxOpenidMasked', label: '平台超管账号管理' },
    { path: 'src/views/Companies.vue', masked: 'row.wxOpenidMasked', label: '企业管理员弹窗' },
    { path: 'src/views/Employees.vue', masked: 'row.openid', label: '员工管理' },
  ]

  for (const v of views) {
    it(`${v.label} 有微信绑定列且传的是掩码字段`, () => {
      const src = code(v.path)
      expect(src, `${v.path} 未接入 WechatBindingCell`).toContain('<WechatBindingCell')
      expect(src, `${v.path} 缺少 prop="wechatBound" 列（elStubs 断言会假绿）`).toContain('prop="wechatBound"')
      expect(src, `${v.path} 传错掩码字段`).toContain(v.masked)
      expect(src).toContain('label="微信绑定"')
    })
  }

  it('员工列表不再直接铺开 openid 明文列', () => {
    const src = code('src/views/Employees.vue')
    expect(src).not.toContain('<span class="openid-cell"')
    expect(src).not.toContain('label="openid"')
  })

  it('管理员的 WXOpenID 仍是 json:"-"，可见性不等于下发凭据原文', () => {
    const models = code('../backend/internal/model/models.go')
    expect(models).toMatch(/WXOpenID\s+string\s+`[^`]*json:"-"/)
    const binding = code('../backend/internal/handler/wechat_binding.go')
    expect(binding).toContain('maskOpenIDForDisplay')
    expect(binding).toContain('employeeBindingRows')
  })
})
