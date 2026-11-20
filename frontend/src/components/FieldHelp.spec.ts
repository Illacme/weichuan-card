import { describe, it, expect } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import FieldHelp from './FieldHelp.vue'

// el-tooltip 的 popper 由 Element Plus 挂在 body 下；单测里只关心
// 「什么时候要求它显示」和「它拿到什么内容」，用桩把控制面暴露出来。
const ElTooltipStub = defineComponent({
  name: 'ElTooltip',
  props: ['effect', 'visible', 'trigger', 'placement', 'popperClass'],
  emits: ['update:visible'],
  template: `<span class="tip"><slot name="content" /><slot /></span>`,
})

const props = {
  title: 'feed-token',
  usage: '视频号和小程序不是同一个主体时，用它来放行内嵌播放。',
  source: '在 MP 平台打开权限后，同一操作者在手机上复制。',
  rules: '小程序必须是非个人主体。',
  note: '24 小时说的是复制窗口，token 值本身不会过期。',
}

async function mountHelp() {
  const w = mount(FieldHelp, {
    props,
    global: { stubs: { 'el-tooltip': ElTooltipStub } },
  })
  await nextTick()
  return w
}

describe('components/FieldHelp', () => {
  it('默认收起，指针停留展开、离开收起', async () => {
    const w = await mountHelp()
    const tip = w.findComponent(ElTooltipStub)
    expect(tip.props('visible')).toBe(false)
    expect(tip.props('trigger')).toEqual([])

    await w.find('.field-help').trigger('mouseenter')
    expect(tip.props('visible')).toBe(true)

    await w.find('.field-help').trigger('mouseleave')
    expect(tip.props('visible')).toBe(false)
  })

  it('点击钉住：指针移开也不收起，再点一次收起', async () => {
    const w = await mountHelp()
    const badge = w.find('.field-help')
    const tip = w.findComponent(ElTooltipStub)

    await badge.trigger('mouseenter')
    await badge.trigger('click')
    await badge.trigger('mouseleave')
    expect(tip.props('visible')).toBe(true)

    await badge.trigger('click')
    expect(tip.props('visible')).toBe(false)
  })

  it('点击徽标以外的地方收起钉住的说明', async () => {
    const w = await mountHelp()
    const tip = w.findComponent(ElTooltipStub)

    await w.find('.field-help').trigger('click')
    expect(tip.props('visible')).toBe(true)

    document.body.click()
    await nextTick()
    expect(tip.props('visible')).toBe(false)
  })

  it('内容按「功能 / 值来源 / 规则限制 / 注意」四段组织，缺段不占位', async () => {
    const w = await mountHelp()
    expect(w.find('.fh-title').text()).toBe('feed-token')
    expect(w.findAll('.fh-key').map((k) => k.text())).toEqual([
      '功能',
      '值来源',
      '规则限制',
      '注意',
    ])
    expect(w.find('.fh-text').text()).toContain('不是同一个主体')

    const minimal = mount(FieldHelp, {
      props: { title: '封面', usage: '视频加载前显示。' },
      global: { stubs: { 'el-tooltip': ElTooltipStub } },
    })
    expect(minimal.findAll('.fh-key').map((k) => k.text())).toEqual(['功能'])
  })

  it('用浅色弹层：深色底配深色字会让长说明看不清', async () => {
    const w = await mountHelp()
    expect(w.findComponent(ElTooltipStub).props('effect')).toBe('light')

    const sfc = readFileSync(
      resolve(dirname(fileURLToPath(import.meta.url)), 'FieldHelp.vue'),
      'utf8'
    )
    expect(sfc).toContain('.field-help-popper.el-popper')
    expect(sfc).toContain('background: #fff')
  })

  it('键盘聚焦也能展开，并带可读的无障碍标签', async () => {
    const w = await mountHelp()
    const badge = w.find('.field-help')
    expect(badge.attributes('aria-label')).toBe('查看feed-token配置说明')
    expect(badge.attributes('tabindex')).toBe('0')

    await badge.trigger('focus')
    expect(w.findComponent(ElTooltipStub).props('visible')).toBe(true)
  })
})
