import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import StorageOverview from './StorageOverview.vue'
import { elStubs, flush } from '@/test/elStubs'

const localProfile = { driver: 'local' } as any
const cloudProfile = { driver: 'oss' } as any
const cloudDelivery = { provider: 'aliyun' } as any
const noCdn = { provider: 'none' } as any

function mountPage(props = {}) {
  return mount(StorageOverview, { props, global: { stubs: elStubs } })
}

/** 展开按需帮助区；默认收起是设计的一部分，所以每次都要显式点开 */
async function openHelp(w: ReturnType<typeof mountPage>) {
  await w.find('.ov-toggle').trigger('click')
  await flush()
}

describe('存储总览页', () => {
  it('默认只给一句结论：文件存在本地磁盘、CDN 未启用', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn, globalBinding: null })
    await flush()
    const text = w.text()
    expect(text).toContain('文件存在服务器本地磁盘，CDN 未启用')
    // 默认配置要让人放心：不接云也能用
    expect(text).toContain('不接任何云账号也能正常使用')
    expect(text).toContain('不接云也能正常使用')
    // 接手的人不该先看到 driver / provider 这类字段名
    expect(text).not.toContain('platform_primary')
    expect(text).not.toContain('tenant_id')
  })

  it('默认视图不铺外链、不铺流程：第一屏只有一个动作', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    // 这是「简化」的核心守门：39 个官方链接曾经全铺在首页，等于把手册摊在门口
    expect(w.findAll('a[href]')).toHaveLength(0)
    expect(w.findAll('.ov-step')).toHaveLength(0)
    // 帮助区整块不渲染（不是渲染后隐藏）：连章节标题都不该出现在 DOM 里
    expect(w.find('.ov-help').exists()).toBe(false)
    expect(w.findAll('.ov-help-section')).toHaveLength(0)
    // 动作按钮只有一个：默认本地时是「我要接入云平台」
    const actions = w.findAll('button.el-button')
    expect(actions).toHaveLength(1)
    expect(actions[0].text()).toBe('我要接入云平台')
  })

  it('画出三层架构，CDN 关闭时标明它是可选的', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    const text = w.text()
    expect(w.findAll('.ov-node')).toHaveLength(3)
    expect(text).toContain('访客')
    expect(text).toContain('CDN · 可选')
    expect(text).toContain('存储 · 必有')
    expect(text).toContain('服务器本地磁盘')
  })

  it('接入云平台后结论如实改写，动作换成「管理存储档案」', async () => {
    const w = mountPage({
      platformProfile: cloudProfile,
      platformDelivery: cloudDelivery,
      globalBinding: { storageProfileId: 3 } as any,
    })
    await flush()
    const text = w.text()
    expect(text).toContain('文件存在阿里云 OSS，CDN 阿里云 CDN')
    expect(text).not.toContain('不接任何云账号也能正常使用')
    expect(w.find('button.el-button').text()).toBe('管理存储档案')
  })

  it('帮助区默认收起，点开才出现五步流程且每步能跳页签', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    expect(w.find('.ov-toggle').attributes('aria-expanded')).toBe('false')
    expect(w.find('.ov-toggle').text()).toContain('展开帮助')

    await openHelp(w)
    expect(w.find('.ov-toggle').attributes('aria-expanded')).toBe('true')
    expect(w.findAll('.ov-step')).toHaveLength(5)
    expect(w.text()).toContain('先看清楚现在存哪')
    expect(w.text()).toContain('需要更大容量时，新建存储档案')
    expect(w.text()).toContain('把老文件搬到新桶')
    expect(w.text()).toContain('切换生效')

    const links = w.findAll('.ov-step-link')
    await links[1].trigger('click')
    expect(w.emitted('goto')?.[0]).toEqual(['storage'])
    await links[3].trigger('click')
    expect(w.emitted('goto')?.[1]).toEqual(['migration'])
  })

  it('帮助区里给全第三方平台官方入口，新窗口打开且带安全属性', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    await openHelp(w)
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

  it('帮助区里带名词解释与 S3 端点示例，术语和填法不用猜', async () => {
    const w = mountPage({ platformProfile: localProfile, platformDelivery: noCdn })
    await flush()
    await openHelp(w)
    const text = w.text()
    for (const term of ['文件对象', '存储档案', '分发档案', '绑定', '迁移任务', '本地镜像', '自检']) {
      expect(text).toContain(term)
    }
    expect(text).toContain('s3.ap-northeast-1.amazonaws.com')
    expect(text).toContain('不带')
  })
})
