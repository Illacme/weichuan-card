import { describe, it, expect } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import StorageOverview from './StorageOverview.vue'
import { elStubs, flush } from '@/test/elStubs'

const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['title', 'description', 'type'],
  template: '<div class="el-alert">{{ title }} {{ description }}<slot /></div>',
})
const stubs = { ...elStubs, 'el-alert': ElAlert }

const localProfile = { driver: 'local' } as any
const cloudProfile = { driver: 'oss' } as any
const cloudDelivery = { provider: 'aliyun' } as any
const noCdn = { provider: 'none' } as any

function mountPage(props = {}) {
  return mount(StorageOverview, { props, global: { stubs } })
}

describe('存储总览页', () => {
  it('默认状态一眼看懂：本地磁盘 + 不启用 CDN + 默认配置标签', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn, globalBinding: null })
    await flush()
    const text = w.text()
    expect(text).toContain('当前是默认配置')
    expect(text).toContain('本地磁盘')
    expect(text).toContain('未启用 CDN')
    expect(text).toContain('默认就是本地配置')
    expect(text).toContain('使用平台自带配置')
    // 接手的人不该先看到 driver / provider 这类字段名
    expect(text).not.toContain('platform_primary')
    expect(text).not.toContain('tenant_id')
  })

  it('接入云平台后状态卡如实改写，并标出已接云', async () => {
    const w = mountPage({
      platformProfile: cloudProfile,
      platformDelivery: cloudDelivery,
      globalBinding: { storageProfileId: 3 } as any,
    })
    await flush()
    const text = w.text()
    expect(text).toContain('已接入云平台')
    expect(text).toContain('阿里云 OSS')
    expect(text).toContain('已设置全局默认')
    expect(text).not.toContain('当前是默认配置')
  })

  it('画出三层架构并讲清各层职责，不只是一段导语', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    const text = w.text()
    expect(text).toContain('整体架构：三层，各管各的')
    expect(text).toContain('访客')
    expect(text).toContain('分发（CDN）')
    expect(text).toContain('存储 · 本地磁盘')
    expect(text).toContain('缓存没命中时回源')
    expect(w.findAll('.ov-node')).toHaveLength(3)
  })

  it('给出完整五步配置流程，每一步都能跳到对应页签', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    expect(w.findAll('.ov-step')).toHaveLength(5)
    expect(w.text()).toContain('先看清楚现在存哪')
    expect(w.text()).toContain('需要更大容量时，新建存储档案')
    expect(w.text()).toContain('把老文件搬到新桶')
    expect(w.text()).toContain('切换生效')
    const buttons = w.findAll('.ov-step button')
    await buttons[1].trigger('click')
    expect(w.emitted('goto')?.[0]).toEqual(['storage'])
    await buttons[3].trigger('click')
    expect(w.emitted('goto')?.[1]).toEqual(['migration'])
  })

  it('所有第三方平台都给出可点开的官方入口，新窗口打开且带上安全属性', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    const links = w.findAll('a[href]')
    expect(links.length).toBeGreaterThanOrEqual(20)
    const urls = links.map((a) => a.attributes('href') || '')
    expect(urls.some((u) => u.includes('oss.console.aliyun.com'))).toBe(true)
    expect(urls.some((u) => u.includes('ram.console.aliyun.com'))).toBe(true)
    expect(urls.some((u) => u.includes('console.cloud.tencent.com/cos'))).toBe(true)
    expect(urls.some((u) => u.includes('dash.cloudflare.com/profile/api-tokens'))).toBe(true)
    expect(urls.some((u) => u.includes('console.aws.amazon.com/cloudfront'))).toBe(true)
    expect(urls.some((u) => u.includes('portal.qiniu.com/user/key'))).toBe(true)
    expect(urls.some((u) => u.includes('console.huaweicloud.com/obs'))).toBe(true)
    for (const a of links) {
      expect(a.attributes('target')).toBe('_blank')
      expect(a.attributes('rel')).toContain('noopener')
    }
  })

  it('列出名词解释与 S3 端点示例，用户不用去猜术语和填法', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    const text = w.text()
    for (const term of ['文件对象', '存储档案', '分发档案', '绑定', '迁移任务', '本地镜像', '自检']) {
      expect(text).toContain(term)
    }
    expect(text).toContain('s3.ap-northeast-1.amazonaws.com')
    expect(text).toContain('不要带')
  })
})
