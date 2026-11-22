import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, nextTick } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'

// 平台 CDN 凭据的真值源是「CDN分发 → 平台自身 CDN」（迁移 0060），
// 不再是系统设置的 cdn_* 一组：这里 mock 的是CDN分发接口。
vi.mock('@/api/storageProfiles', () => ({ listDeliveryProfiles: vi.fn() }))
vi.mock('@/api/storage', () => ({ cdnRefresh: vi.fn() }))
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() },
}))

import { ElMessage } from 'element-plus'
import { listDeliveryProfiles, type DeliveryProfile } from '@/api/storageProfiles'
import { cdnRefresh } from '@/api/storage'
import CdnRefresh from './CdnRefresh.vue'

const ElCard = defineComponent({ name: 'ElCard', template: `<div class="el-card"><slot /></div>` })
// alert 拍平：标题与默认插槽（按钮）都进 DOM，便于断言「提示 + 去设置」一体
const ElAlert = defineComponent({
  name: 'ElAlert',
  props: ['title', 'type', 'closable'],
  template: `<div class="el-alert">{{ title }}<slot /></div>`,
})
const ElTag = defineComponent({
  name: 'ElTag',
  props: ['size', 'type', 'effect'],
  template: `<span class="el-tag"><slot /></span>`,
})
const ElInput = defineComponent({
  name: 'ElInput',
  props: ['modelValue', 'placeholder', 'type', 'rows'],
  emits: ['update:modelValue'],
  template: `<textarea class="el-textarea" :placeholder="placeholder" :value="modelValue" @input="$emit('update:modelValue', $event.target.value)"></textarea>`,
})
const ElButton = defineComponent({
  name: 'ElButton',
  props: ['type', 'loading', 'size'],
  template: `<button class="el-button" @click="$emit('click')"><slot></slot></button>`,
})

function profile(over: Partial<DeliveryProfile> = {}): DeliveryProfile {
  return {
    id: 1, name: '平台自身 CDN', provider: 'aliyun', domain: '',
    endpoint: '', resourceId: '', status: 'unknown', lastError: '', version: 1,
    builtin: true, purpose: 'platform_primary',
    accessKeyConfigured: true, secretKeyConfigured: true, accessKeyMasked: 'AK****',
    ...over,
  }
}

// 三种厂商形态：aliyun（AK+SK）、cloudfront（AK+SK+资源标识）、cloudflare（仅 Token+资源标识）
const ON_ALIYUN = [profile()]

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/platform/storage-profiles', component: { template: '<div>profiles</div>' } },
    ],
  })
}

async function setup(profiles: DeliveryProfile[]) {
  const router = makeRouter()
  await router.push('/')
  await router.isReady()
  vi.mocked(listDeliveryProfiles).mockResolvedValue(profiles as never)
  const w = mount(CdnRefresh, {
    global: {
      plugins: [router],
      stubs: { 'el-card': ElCard, 'el-alert': ElAlert, 'el-tag': ElTag, 'el-input': ElInput, 'el-button': ElButton },
    },
  })
  await vi.waitFor(() => expect(listDeliveryProfiles).toHaveBeenCalled())
  await nextTick()
  return { w, router }
}

function submitButton(w: any) {
  return w.findAll('button.el-button').find((b: any) => b.text().includes('提交刷新'))!
}

describe('CdnRefresh 平台管理 · CDN 刷新', () => {
  beforeEach(() => {
    vi.mocked(listDeliveryProfiles).mockReset()
    vi.mocked(cdnRefresh).mockReset()
    vi.mocked(cdnRefresh).mockResolvedValue({ code: 0 } as never)
    vi.mocked(ElMessage.success).mockClear()
    vi.mocked(ElMessage.warning).mockClear()
  })

  it('配置齐全：只给通用说明，不挡提交，并显示当前厂商', async () => {
    const { w } = await setup(ON_ALIYUN)
    const text = w.text()
    expect(text).toContain('aliyun（阿里云）')
    expect(text).not.toContain('未启用')
    expect(text).not.toContain('还缺')
    // 说明里点明支持五家 + 自动刷新触发点 + 凭据在哪改
    expect(text).toContain('CloudFront')
    expect(text).toContain('自动刷新')
    expect(text).toContain('CDN分发')
  })

  it('未启用：先点明缺口并给去CDN分发的入口，省一次无效提交', async () => {
    const { w, router } = await setup([profile({ provider: 'none' })])
    expect(w.text()).toContain('CDN 刷新当前未启用')
    await w.findAll('button.el-button').find((b: any) => b.text().includes('去设置'))!.trigger('click')
    await nextTick()
    // 直达「CDN分发」标签：CDN 凭据只在那里配，不再指去系统设置
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/platform/storage-profiles'))
    expect(router.currentRoute.value.query.tab).toBe('delivery')
  })

  it('CloudFront 缺资源标识：点名缺的那一项', async () => {
    const { w } = await setup([profile({ provider: 'cloudfront', resourceId: '' })])
    expect(w.text()).toContain('cloudfront')
    expect(w.text()).toContain('还缺 资源标识')
    // 只守住「缺口列表」不误报；页面下方厂商说明里提到 AccessKey 是正常的
    expect(w.text()).not.toMatch(/还缺[^。]*AccessKey/)
  })

  it('Cloudflare：用不到 AccessKey，只提示缺 API Token', async () => {
    const { w } = await setup([
      profile({ provider: 'cloudflare', accessKeyConfigured: false, secretKeyConfigured: false, resourceId: 'zone-1' }),
    ])
    expect(w.text()).toContain('还缺 API Token')
    expect(w.text()).not.toMatch(/还缺[^。]*AccessKey/)
  })

  it('档案接口拿不到时不下判断：宁可不提示未启用，也不挡提交', async () => {
    const router = makeRouter()
    await router.push('/')
    await router.isReady()
    vi.mocked(listDeliveryProfiles).mockRejectedValue(new Error('网络错误') as never)
    const w = mount(CdnRefresh, {
      global: {
        plugins: [router],
        stubs: { 'el-card': ElCard, 'el-alert': ElAlert, 'el-tag': ElTag, 'el-input': ElInput, 'el-button': ElButton },
      },
    })
    await nextTick()
    await vi.waitFor(() => expect(w.text()).toContain('还没读到'))
    expect(w.text()).not.toContain('未启用')
    expect(submitButton(w).exists()).toBe(true)
  })

  it('提交：按行拆成 URL 数组，空行与首尾空格忽略', async () => {
    const { w } = await setup(ON_ALIYUN)
    await w.find('textarea').setValue('https://cdn.example.com/a.png\n\n  https://cdn.example.com/b.jpg  \n')
    expect(w.text()).toContain('共 2 条')
    await submitButton(w).trigger('click')
    await nextTick()
    expect(vi.mocked(cdnRefresh)).toHaveBeenCalledWith([
      'https://cdn.example.com/a.png',
      'https://cdn.example.com/b.jpg',
    ])
    expect(vi.mocked(ElMessage.success)).toHaveBeenCalled()
  })

  it('空输入不发请求：先提示，避免一次必然失败的往返', async () => {
    const { w } = await setup(ON_ALIYUN)
    await submitButton(w).trigger('click')
    await nextTick()
    expect(vi.mocked(cdnRefresh)).not.toHaveBeenCalled()
    expect(vi.mocked(ElMessage.warning)).toHaveBeenCalledWith('请至少填写一个 URL')
  })

  it('给出当前厂商的官方入口与凭据取法，刷新失败时不用自己搜控制台', async () => {
    const { w } = await setup([profile({ provider: 'cloudflare', resourceId: 'zone-1' })])
    const hrefs = w.findAll('a[href]').map((a) => a.attributes('href') || '')
    expect(hrefs).toContain('https://dash.cloudflare.com/profile/api-tokens')
    expect(hrefs).toContain('https://developers.cloudflare.com/cache/how-to/purge-cache/')
    expect(w.text()).toContain('API Token')
    expect(w.text()).toContain('Zone ID')
    for (const a of w.findAll('a[href]')) {
      expect(a.attributes('target')).toBe('_blank')
    }
  })
})
