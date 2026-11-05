import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick, defineComponent } from 'vue'
import UploadField from './UploadField.vue'

// UploadField 内的素材库弹窗（AssetPicker）有自己的单测；
// 这里把它桩成只透传 props / 派发事件的壳，专注验证入口与 picked 回填逻辑。
// element-plus 的 ElMessage 同样 mock 掉（限位提示等分支只断言调用，不渲染真实消息）。
vi.mock('element-plus', () => ({
  ElMessage: { success: vi.fn(), error: vi.fn(), warning: vi.fn() },
}))

// 上传实现抽在 api/uploadFile.ts（有自己的单测）；这里只关心
// 「进度回调有没有变成用户看得见的进度条 / 阶段文案」。
const uploadMocks = vi.hoisted(() => ({ uploadFile: vi.fn() }))
vi.mock('@/api/uploadFile', () => ({ uploadFile: uploadMocks.uploadFile }))

import { ElMessage } from 'element-plus'

// 轻量 stub：仅捕获 el-upload 透传的 props，避免引入完整 Element Plus（jsdom 下过重易 OOM）。
const ElUpload = defineComponent({
  name: 'ElUpload',
  props: ['action', 'limit', 'fileList', 'multiple', 'accept', 'listType', 'beforeUpload', 'httpRequest', 'onSuccess'],
  template: `<div class="el-upload"><slot /><slot name="tip" /></div>`,
})
const AssetPickerStub = defineComponent({
  name: 'AssetPicker',
  props: ['modelValue', 'multiple', 'limit', 'defaultCategory'],
  emits: ['update:modelValue', 'picked'],
  template: `<div class="asset-picker-stub" />`,
})

const mountOpts = {
  global: { stubs: { 'el-upload': ElUpload, AssetPicker: AssetPickerStub } },
}

describe('UploadField', () => {
  beforeEach(() => {
    vi.mocked(ElMessage.warning).mockClear()
    vi.mocked(ElMessage.error).mockClear()
    uploadMocks.uploadFile.mockReset()
  })

  it('渲染选择文件按钮与提示文案', () => {
    const w = mount(UploadField, { props: { modelValue: '' }, ...mountOpts })
    expect(w.text()).toContain('选择文件')
    expect(w.text()).toContain('支持图片')
  })

  it('渲染「从素材库选择」入口', () => {
    const w = mount(UploadField, { props: { modelValue: '' }, ...mountOpts })
    expect(w.text()).toContain('从素材库选择')
  })

  it('单选 modelValue 回显为文件列表（文件名取 URL 末段）', async () => {
    const w = mount(UploadField, {
      props: { modelValue: 'http://cdn.example.com/path/abc.png' },
      ...mountOpts,
    })
    await nextTick()
    const fl = w.findComponent(ElUpload).props('fileList') as Array<{ name: string }>
    expect(fl).toHaveLength(1)
    expect(fl[0].name).toBe('abc.png')
  })

  it('多选 modelValue 数组回显多个文件', async () => {
    const w = mount(UploadField, {
      props: { modelValue: ['http://x/a.png', 'http://x/b.jpg'], multiple: true },
      ...mountOpts,
    })
    await nextTick()
    const fl = w.findComponent(ElUpload).props('fileList') as Array<{ name: string }>
    expect(fl.map((f) => f.name)).toEqual(['a.png', 'b.jpg'])
  })

  it('普通上传 action 与 limit 透传给 el-upload', async () => {
    const w = mount(UploadField, {
      props: { modelValue: '', action: '/api/admin/upload' },
      ...mountOpts,
    })
    await nextTick()
    const up = w.findComponent(ElUpload)
    expect(up.props('action')).toBe('/api/admin/upload')
    expect(up.props('limit')).toBe(9)
  })

  it('library-type 透传给素材选择器（定位默认分类）', () => {
    const w = mount(UploadField, { props: { modelValue: '', libraryType: 'icon' }, ...mountOpts })
    expect(w.findComponent(AssetPickerStub).props('defaultCategory')).toBe('icon')
    const w2 = mount(UploadField, { props: { modelValue: '', multiple: true, limit: 3 }, ...mountOpts })
    const picker = w2.findComponent(AssetPickerStub)
    expect(picker.props('multiple')).toBe(true)
    expect(picker.props('limit')).toBe(3)
  })

  it('素材库单选：picked 直接替换 modelValue', async () => {
    const w = mount(UploadField, { props: { modelValue: 'http://x/old.png' }, ...mountOpts })
    w.findComponent(AssetPickerStub).vm.$emit('picked', 'http://x/lib.png')
    await nextTick()
    expect(w.emitted('update:modelValue')![0]).toEqual(['http://x/lib.png'])
  })

  it('素材库多选：picked 数组追加到已有值', async () => {
    const w = mount(UploadField, {
      props: { modelValue: ['http://x/a.png'], multiple: true, limit: 9 },
      ...mountOpts,
    })
    w.findComponent(AssetPickerStub).vm.$emit('picked', ['http://x/b.png', 'http://x/c.png'])
    await nextTick()
    expect(w.emitted('update:modelValue')![0]).toEqual([['http://x/a.png', 'http://x/b.png', 'http://x/c.png']])
  })

  it('素材库多选超限：截断到 limit 并提示', async () => {
    const w = mount(UploadField, {
      props: { modelValue: ['http://x/a.png'], multiple: true, limit: 2 },
      ...mountOpts,
    })
    w.findComponent(AssetPickerStub).vm.$emit('picked', ['http://x/b.png', 'http://x/c.png', 'http://x/d.png'])
    await nextTick()
    expect(w.emitted('update:modelValue')![0]).toEqual([['http://x/a.png', 'http://x/b.png']])
    expect(ElMessage.warning).toHaveBeenCalledWith('最多上传 2 个文件')
  })

  // ---- 选文件时的大小预校验 ----
  //
  // 这里的数字必须和**服务端** checkFile 的判断对齐（那边是 size > max 才拒）：
  // 正好等于上限的文件，两边都该放行。之前前端写的是 size < max，
  // 于是「正好 100MB 的视频」界面说不能超过、后端其实收——同一个文件两套结论，
  // 用户只会觉得「改了上限也没用」。
  describe('大小预校验', () => {
    const pick = (maxMB: number) => {
      const w = mount(UploadField, { props: { modelValue: '', maxMB }, ...mountOpts })
      return w.findComponent(ElUpload).props('beforeUpload') as (f: { size: number }) => boolean
    }
    const MB = 1024 * 1024

    beforeEach(() => vi.mocked(ElMessage.error).mockClear())

    it('正好等于上限：放行（与服务端 size > max 的判定一致）', () => {
      expect(pick(100)({ size: 100 * MB })).toBe(true)
      expect(ElMessage.error).not.toHaveBeenCalled()
    })

    it('超出上限 1 字节：拦下并提示上限值', () => {
      expect(pick(100)({ size: 100 * MB + 1 })).toBe(false)
      expect(ElMessage.error).toHaveBeenCalledWith('文件不能超过 100MB')
    })

    it('上限被写成 0 时按「不限制」处理，不把文件全拒掉', () => {
      expect(pick(0)({ size: 500 * MB })).toBe(true)
      expect(ElMessage.error).not.toHaveBeenCalled()
    })
  })

  // ---- 上传进度条 ----
  //
  // 用户反馈：点完「选择文件」上传几十 MB 的视频，界面长时间没有任何反应。
  // 这里钉的是「api 层报出来的进度真的变成了用户看得见的东西」，
  // 以及最容易被漏掉的一条：直传失败会**静默回退**成服务器中转，
  // 那一刻进度从头开始，不写清阶段就是一条莫名其妙倒退的进度条。
  describe('上传进度条', () => {
    function startUpload(w: any) {
      let resolveFn: (u: string) => void = () => {}
      let rejectFn: (e: Error) => void = () => {}
      let emitProgress: (p: any) => void = () => {}
      uploadMocks.uploadFile.mockImplementation((_file: any, opts: any) => {
        emitProgress = opts.onProgress
        return new Promise<string>((res, rej) => {
          resolveFn = res
          rejectFn = rej
        })
      })
      const option = {
        file: new File(['x'], 'a.mp4', { type: 'video/mp4' }),
        action: '/api/admin/upload',
        // 模拟 el-upload：自定义 http-request 拿到 onSuccess 后，最终会转交给
        // 组件注册的 :on-success。只把 option.onSuccess 桩成 vi.fn 的话，
        // 「上传成功 → 回填 v-model」这半条链路根本没跑到。
        onSuccess: vi.fn((res: any) => {
          const cb: any = w.findComponent(ElUpload).props('onSuccess')
          cb?.(res)
        }),
        onError: vi.fn(),
        onProgress: vi.fn(),
      }
      const ret = w.findComponent(ElUpload).props('httpRequest')(option as any)
      return {
        option,
        ret,
        progress: (p: any) => emitProgress(p),
        done: (u: string) => resolveFn(u),
        fail: (e: Error) => rejectFn(e),
      }
    }
    const mountField = () => mount(UploadField, { props: { modelValue: '' }, ...mountOpts })

    it('直传中：显示阶段文案与百分比，进度条宽度跟着走', async () => {
      const w = mountField()
      const u = startUpload(w)

      u.progress({ phase: 'presign', percent: 0, loaded: 0, total: 100 })
      await nextTick()
      expect(w.find('.uf-progress').text()).toContain('正在申请上传链接')
      expect(w.find('.uf-progress-pct').exists()).toBe(false) // 这步没字节进度，别显示 0%

      u.progress({ phase: 'direct', percent: 42, loaded: 42, total: 100 })
      await nextTick()
      expect(w.find('.uf-progress').text()).toContain('正在直传到云存储')
      expect(w.find('.uf-progress-pct').text()).toContain('42%')
      expect(w.find('.uf-progress-bar').attributes('style')).toContain('42%')
      expect(w.find('.uf-progress.is-server').exists()).toBe(false)
    })

    it('回退到服务器中转：换成警示配色并说明原因，进度从 0 重新计', async () => {
      const w = mountField()
      const u = startUpload(w)

      u.progress({ phase: 'direct', percent: 80, loaded: 80, total: 100 })
      u.progress({ phase: 'server', percent: 0, loaded: 0, total: 100 })
      await nextTick()

      expect(w.find('.uf-progress.is-server').exists()).toBe(true)
      expect(w.find('.uf-progress').text()).toContain('已改用服务器中转')
      expect(w.find('.uf-progress').text()).toContain('大文件会占服务器带宽')
      expect(w.find('.uf-progress-pct').text()).toContain('0%')
    })

    // 用户反馈「进度条总在 99% 停很久」：那段时间字节已经发完，
    // 只是还在等服务端（中转时服务端要再把文件写一次对象存储）。
    // 必须显示「还在等」+ 计时 + 动画，不能把 99% 僵在那里。
    it('字节发完进入 finishing：不显示 99%，改显示等待秒数与滑动动画', async () => {
      const w = mountField()
      const u = startUpload(w)

      u.progress({ phase: 'direct', percent: 99, loaded: 99, total: 100 })
      u.progress({ phase: 'finishing', percent: 99, loaded: 100, total: 100, via: 'direct' })
      await nextTick()

      expect(w.find('.uf-progress.is-finishing').exists()).toBe(true)
      expect(w.find('.uf-progress').text()).toContain('等待云存储确认')
      expect(w.find('.uf-progress-pct').text()).toContain('已等待 0s')
      expect(w.find('.uf-progress-pct').text()).not.toContain('99%')
      expect(w.find('.uf-progress-bar.is-waiting').exists()).toBe(true)
      expect(w.find('.uf-progress-bar').attributes('style')).toContain('30%')

      u.done('https://cdn/x.mp4')
      await flushPromises()
      expect(w.find('.uf-progress').exists()).toBe(false)
      w.unmount()
    })

    it('中转路径的 finishing：保留警示配色并说明「正在写入云存储」', async () => {
      const w = mountField()
      const u = startUpload(w)

      u.progress({ phase: 'server', percent: 90, loaded: 90, total: 100 })
      u.progress({ phase: 'finishing', percent: 99, loaded: 100, total: 100, via: 'server' })
      await nextTick()

      expect(w.find('.uf-progress.is-server').exists()).toBe(true)
      expect(w.find('.uf-progress').text()).toContain('正在写入云存储')
      expect(w.find('.uf-progress').text()).toContain('大文件可能要几十秒')

      u.done('/uploads/x.mp4')
      await flushPromises()
      w.unmount()
    })

    it('等待计时每秒走动，上传完成后不残留计时器', async () => {
      vi.useFakeTimers()
      try {
        const w = mountField()
        const u = startUpload(w)
        u.progress({ phase: 'server', percent: 90, loaded: 90, total: 100 })
        u.progress({ phase: 'finishing', percent: 99, loaded: 100, total: 100, via: 'server' })
        await nextTick()

        expect(w.find('.uf-progress-pct').text()).toContain('已等待 0s')
        vi.advanceTimersByTime(3000)
        await nextTick()
        expect(w.find('.uf-progress-pct').text()).toContain('已等待 3s')

        u.done('/uploads/x.mp4')
        await flushPromises()
        expect(w.find('.uf-progress').exists()).toBe(false)
        expect(vi.getTimerCount()).toBe(0)
        w.unmount()
      } finally {
        vi.useRealTimers()
      }
    })

    it('上传成功：进度条消失，onSuccess 拿到 {code:0,data:{urls}} 信封', async () => {
      const w = mountField()
      const u = startUpload(w)
      u.progress({ phase: 'direct', percent: 60, loaded: 60, total: 100 })
      await nextTick()
      expect(w.find('.uf-progress').exists()).toBe(true)

      u.done('/uploads/new.mp4')
      await nextTick()
      await Promise.resolve()

      expect(w.find('.uf-progress').exists()).toBe(false)
      expect(u.option.onSuccess).toHaveBeenCalledWith({
        code: 0,
        data: { urls: ['/uploads/new.mp4'] },
      })
      expect(w.emitted('update:modelValue')![0]).toEqual(['/uploads/new.mp4'])
    })

    it('上传失败：进度条消失并提示错误，不留下一条卡住的进度条', async () => {
      const w = mountField()
      const u = startUpload(w)
      u.progress({ phase: 'direct', percent: 30, loaded: 30, total: 100 })
      await nextTick()

      u.fail(new Error('文件过大，上限 100 MB'))
      await nextTick()
      await Promise.resolve()

      expect(w.find('.uf-progress').exists()).toBe(false)
      expect(ElMessage.error).toHaveBeenCalledWith('文件过大，上限 100 MB')
      expect(u.option.onError).toHaveBeenCalled()
    })

    // el-upload 对「返回 Promise 的 http-request」会**再接一次** onSuccess，
    // 第二遍拿到 undefined，我们的 onSuccess 读 res.code 就抛 TypeError。
    // 这条守的是 customUpload 返回 undefined，别哪天图省事又写成 async。
    it('http-request 不返回 Promise，避免 el-upload 二次触发 onSuccess', () => {
      const w = mountField()
      const u = startUpload(w)
      expect(u.ret).toBeUndefined()
    })
  })
})
