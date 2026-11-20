import { describe, it, expect } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import FieldHelp from './FieldHelp.vue'

// el-tooltip 的 popper 由 Element Plus 挂在 body 下；单测里只关心
// 「什么时候要求它显示」和「它拿到什么内容」，用桩把控制面暴露出来。
const ElTooltipStub = defineComponent({
  name: 'ElTooltip',
  props: ['content', 'visible', 'trigger', 'placement', 'popperClass'],
  emits: ['update:visible'],
  template: `<span class="tip"><slot /></span>`,
})

const props = {
  title: 'feed-token',
  usage: '跨主体场景代替 feedId 内嵌播放。',
  source: 'MP 平台开权限后移动端复制。',
  rules: '小程序须为非个人主体。',
  note: '24 小时是复制入口窗口，token 值长期有效。',
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
    const content = String(w.findComponent(ElTooltipStub).props('content'))
    expect(content).toBe(
      [
        '功能：跨主体场景代替 feedId 内嵌播放。',
        '值来源：MP 平台开权限后移动端复制。',
        '规则限制：小程序须为非个人主体。',
        '注意：24 小时是复制入口窗口，token 值长期有效。',
      ].join('\n')
    )

    const minimal = mount(FieldHelp, {
      props: { title: '封面', usage: '视频加载前显示。' },
      global: { stubs: { 'el-tooltip': ElTooltipStub } },
    })
    expect(String(minimal.findComponent(ElTooltipStub).props('content'))).toBe(
      '功能：视频加载前显示。'
    )
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
