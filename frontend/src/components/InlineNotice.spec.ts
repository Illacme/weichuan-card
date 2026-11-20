import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import InlineNotice from './InlineNotice.vue'

describe('components/InlineNotice', () => {
  it('用叹号标识，和帮助配置的问号区分开', () => {
    const w = mount(InlineNotice, { slots: { default: '已经填了 feedId，这一项不会生效' } })
    expect(w.find('.inline-notice-mark').text()).toBe('!')
    expect(w.text()).toContain('这一项不会生效')
  })

  it('按语气用不同颜色类，默认 warn', () => {
    const warn = mount(InlineNotice, { slots: { default: '留意' } })
    expect(warn.html()).toContain('is-warn')

    const danger = mount(InlineNotice, { props: { tone: 'danger' }, slots: { default: '会失败' } })
    expect(danger.html()).toContain('is-danger')
  })
})
