import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { uploadFile } from './uploadFile'

// 上传有两条链路（预签名直传 / 普通 multipart），四类回退（预签名 404、code!=0、
// PUT 失败、PUT 网络错误）。这些分支全是「静默降级」——写错了只会表现为
// 「偶尔有人说图片传不上去」，所以每条都要钉住。
//
// XHR 用最小假实现（记录 open/send，手动 finish），不引入 mock 库：
// 我们的调用只用到 open/setRequestHeader/send/onload/onerror 五个面。
// upload.onprogress 是新增的第六个面——进度条全靠它，必须能手动触发。
// upload.onload 是第七个面：请求体发完时触发，早于 XHR 自己的 onload，
// 用来钉住「字节发完 → 等服务端确认」这段没有进度事件的静默期。
class FakeXHR {
  static instances: FakeXHR[] = []
  method = ''
  url = ''
  status = 0
  responseText = ''
  headers: Record<string, string> = {}
  sent: any = null
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  upload: { onprogress: ((e: any) => void) | null; onload: (() => void) | null } = {
    onprogress: null,
    onload: null,
  }

  progress(loaded: number, total: number, lengthComputable = true) {
    this.upload.onprogress?.({ lengthComputable, loaded, total })
  }

  uploaded() {
    this.upload.onload?.()
  }

  open(method: string, url: string) {
    this.method = method
    this.url = url
  }
  setRequestHeader(k: string, v: string) {
    this.headers[k.toLowerCase()] = v
  }
  send(body: any) {
    this.sent = body
    FakeXHR.instances.push(this)
  }
  finish(status: number, text = '') {
    this.status = status
    this.responseText = text
    this.onload?.()
  }
  fail() {
    this.onerror?.()
  }
}

const file = () => new File(['bin'], 'photo.png', { type: 'image/png' })

// 推进微任务：uploadFile 内部有多层 await，一次 Promise.resolve 不够
async function flush(n = 8) {
  for (let i = 0; i < n; i++) await Promise.resolve()
}

function mockPresign(res: any) {
  const fn = vi.fn().mockResolvedValue({ json: async () => res })
  vi.stubGlobal('fetch', fn)
  return fn
}

beforeEach(() => {
  FakeXHR.instances = []
  vi.stubGlobal('XMLHttpRequest', FakeXHR as any)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('uploadFile', () => {
  it('预签名可用：PUT 直传成功后返回签名 URL，不再走普通上传', async () => {
    mockPresign({ code: 0, data: { putUrl: 'https://oss.example.com/put', url: 'https://cdn/x.png' } })

    const p = uploadFile(file())
    await flush()

    const xhr = FakeXHR.instances[0]
    expect(xhr.method).toBe('PUT')
    expect(xhr.url).toBe('https://oss.example.com/put')
    expect(xhr.headers['content-type']).toBe('image/png')
    xhr.finish(200)

    await expect(p).resolves.toBe('https://cdn/x.png')
    expect(xhr.headers['content-type']).toBe('image/png')
  })

  it('预签名返回 code!=0（本地磁盘等不支持直传）：回退普通上传', async () => {
    mockPresign({ code: 1, msg: '当前存储不支持预签名' })

    const p = uploadFile(file())
    await flush()

    const xhr = FakeXHR.instances[0]
    expect(xhr.method).toBe('POST')
    expect(xhr.url).toBe('/api/admin/upload')
    expect(xhr.sent).toBeInstanceOf(FormData)
    xhr.finish(200, JSON.stringify({ code: 0, data: { urls: ['/uploads/a.png'] } }))

    await expect(p).resolves.toBe('/uploads/a.png')
  })

  it('预签名接口本身失败（404 / 网络）：回退普通上传', async () => {
    const fn = vi.fn().mockRejectedValue(new Error('404'))
    vi.stubGlobal('fetch', fn)

    const p = uploadFile(file())
    await flush()

    expect(FakeXHR.instances[0].method).toBe('POST')
    FakeXHR.instances[0].finish(200, JSON.stringify({ code: 0, data: { urls: ['/uploads/b.png'] } }))
    await expect(p).resolves.toBe('/uploads/b.png')
  })

  it('PUT 直传失败（5xx）：回退普通上传而不是整体失败', async () => {
    mockPresign({ code: 0, data: { putUrl: 'https://oss/put', url: 'https://cdn/x.png' } })

    const p = uploadFile(file())
    await flush()
    FakeXHR.instances[0].finish(500) // PUT 失败

    await flush()
    const xhr2 = FakeXHR.instances[1]
    expect(xhr2.method).toBe('POST')
    xhr2.finish(200, JSON.stringify({ code: 0, data: { urls: ['/uploads/c.png'] } }))
    await expect(p).resolves.toBe('/uploads/c.png')
  })

  it('自定义 action 会推导出对应的预签名地址', async () => {
    const fetchFn = mockPresign({ code: 1 })

    const p = uploadFile(file(), { action: '/api/admin/upload?companyId=2' })
    await flush()

    expect(fetchFn).toHaveBeenCalledTimes(1)
    expect(String((fetchFn.mock.calls[0] as any[])[0])).toContain('/api/admin/upload/presign?')
    FakeXHR.instances[0].finish(200, JSON.stringify({ code: 0, data: { urls: ['/u.png'] } }))
    await expect(p).resolves.toBe('/u.png')
  })

  it('action 自带查询串时用 & 续接参数，不拼出两个 ?', async () => {
    // a?b=1?ext=png 会把 ext 吞进 companyId 的值里，服务端拿不到扩展名 → 直传签名出错
    const fetchFn = mockPresign({ code: 1 })

    const p = uploadFile(file(), { action: '/api/admin/upload?companyId=2' })
    await flush()

    const url = String((fetchFn.mock.calls[0] as any[])[0])
    expect(url).toBe(
      '/api/admin/upload/presign?companyId=2&ext=png&contentType=image%2Fpng&size=3'
    )
    FakeXHR.instances[0].finish(200, JSON.stringify({ code: 0, data: { urls: ['/u.png'] } }))
    await p
  })

  it('带上 size：直传的字节服务端看不到，配额只能靠这个数计量', async () => {
    const fetchFn = mockPresign({ code: 1 })
    const big = new File([new Uint8Array(2048)], 'big.jpg', { type: 'image/jpeg' })

    const p = uploadFile(big)
    await flush()

    expect(String((fetchFn.mock.calls[0] as any[])[0])).toContain('&size=2048')
    FakeXHR.instances[0].finish(200, JSON.stringify({ code: 0, data: { urls: ['/u.jpg'] } }))
    await p
  })

  it('普通上传业务失败：抛错带后端 msg（供 UI 直接展示）', async () => {
    mockPresign({ code: 1 })

    const p = uploadFile(file())
    await flush()
    FakeXHR.instances[0].finish(200, JSON.stringify({ code: 1, msg: '文件超过大小限制' }))

    await expect(p).rejects.toThrow('文件超过大小限制')
  })

  it('普通上传返回非 JSON（网关错误页 / 登录失效重定向）：抛错带状态码', async () => {
    mockPresign({ code: 1 })

    const p = uploadFile(file())
    await flush()
    FakeXHR.instances[0].finish(502, '<html>Bad Gateway</html>')

    await expect(p).rejects.toThrow('HTTP 502')
  })

  it('普通上传成功但没回文件地址：抛错而不是静默返回空串', async () => {
    mockPresign({ code: 1 })

    const p = uploadFile(file())
    await flush()
    FakeXHR.instances[0].finish(200, JSON.stringify({ code: 0, data: { urls: [] } }))

    await expect(p).rejects.toThrow('未返回文件地址')
  })

  // ---- 上传进度 ----
  //
  // 大视频点完「选择文件」没有任何反馈，用户只会以为页面卡死。
  // 更麻烦的是**直传失败会静默回退**：那一刻进度必然从头开始，
  // 不把阶段一起报出来，用户看到的就是「进度条倒退」这个说不通的现象。
  describe('上传进度', () => {
    const okMultipart = () =>
      JSON.stringify({ code: 0, data: { urls: ['/uploads/done.png'] } })

    it('直传：先报申请链接，再报 direct 进度，成功补到 100', async () => {
      mockPresign({ code: 0, data: { putUrl: 'https://oss/put', url: 'https://cdn/x.png' } })
      const seen: any[] = []
      const p = uploadFile(file(), { onProgress: (x) => seen.push(x) })

      expect(seen[0]).toMatchObject({ phase: 'presign', percent: 0 })
      await flush()
      const xhr = FakeXHR.instances[0]
      expect(xhr.method).toBe('PUT')
      xhr.progress(25, 100)
      xhr.progress(60, 100)
      expect(seen.at(-1)).toMatchObject({ phase: 'direct', percent: 60, loaded: 60, total: 100 })

      xhr.finish(200)
      await expect(p).resolves.toBe('https://cdn/x.png')
      expect(seen.at(-1)).toMatchObject({ phase: 'direct', percent: 100 })
    })

    it('预签名不可用回退：阶段切成 server 且进度从 0 重新计', async () => {
      mockPresign({ code: 1 })
      const seen: any[] = []
      const p = uploadFile(file(), { onProgress: (x) => seen.push(x) })
      await flush()

      const xhr = FakeXHR.instances[0]
      expect(xhr.method).toBe('POST')
      expect(seen.at(-1)).toMatchObject({ phase: 'server', percent: 0 })
      xhr.progress(50, 100)
      expect(seen.at(-1)).toMatchObject({ phase: 'server', percent: 50 })
      xhr.finish(200, okMultipart())

      await p
      expect(seen.at(-1)).toMatchObject({ phase: 'server', percent: 100 })
    })

    it('PUT 失败后回退：direct 阶段不能谎报 100%，随后切 server', async () => {
      mockPresign({ code: 0, data: { putUrl: 'https://oss/put', url: 'https://cdn/x.png' } })
      const seen: any[] = []
      const p = uploadFile(file(), { onProgress: (x) => seen.push(x) })
      await flush()

      const put = FakeXHR.instances[0]
      put.progress(80, 100)
      expect(seen.some((x) => x.phase === 'direct' && x.percent === 100)).toBe(false)
      put.finish(500)
      await flush()

      const phases = seen.map((x) => x.phase)
      expect(phases).toEqual(expect.arrayContaining(['presign', 'direct', 'server']))
      const firstServer = seen.find((x) => x.phase === 'server')
      expect(firstServer.percent).toBe(0)
      FakeXHR.instances[1].finish(200, okMultipart())
      await p
    })

    it('未传 onProgress 时不挂 upload.onprogress / upload.onload（老调用方零影响）', async () => {
      mockPresign({ code: 1 })
      const p = uploadFile(file())
      await flush()

      expect(FakeXHR.instances[0].upload.onprogress).toBe(null)
      expect(FakeXHR.instances[0].upload.onload).toBe(null)
      FakeXHR.instances[0].finish(200, okMultipart())
      await p
    })

    // 「99% 停很久」的根因：进度条只量了请求体发送，字节发完后还有一段
    // 完全没有 progress 事件的等待（中转时服务端要再写一次对象存储）。
    // 这段必须由 upload.onload 单独报出来，否则 UI 只能僵在 99%。
    describe('字节发完后的等待态', () => {
      it('直传：upload.onload 后报 finishing(via=direct)，响应回来才补 100', async () => {
        mockPresign({ code: 0, data: { putUrl: 'https://oss/put', url: 'https://cdn/x.png' } })
        const seen: any[] = []
        const p = uploadFile(file(), { onProgress: (x) => seen.push(x) })
        await flush()

        const xhr = FakeXHR.instances[0]
        // 故意让最后一个 progress 停在 96%：不能靠 loaded===total 判断「发完了」
        xhr.progress(96, 100)
        xhr.uploaded()
        expect(seen.at(-1)).toMatchObject({ phase: 'finishing', via: 'direct', percent: 99 })
        expect(seen.at(-1).loaded).toBe(3) // file.size，不是 100

        xhr.finish(200)
        await expect(p).resolves.toBe('https://cdn/x.png')
        expect(seen.at(-1)).toMatchObject({ phase: 'direct', percent: 100 })
      })

      it('回退中转：finishing 带 via=server，UI 才能说明为什么特别慢', async () => {
        mockPresign({ code: 1 })
        const seen: any[] = []
        const p = uploadFile(file(), { onProgress: (x) => seen.push(x) })
        await flush()

        const xhr = FakeXHR.instances[0]
        xhr.progress(99, 100)
        xhr.uploaded()
        expect(seen.at(-1)).toMatchObject({ phase: 'finishing', via: 'server', percent: 99 })

        xhr.finish(200, okMultipart())
        await p
        expect(seen.at(-1)).toMatchObject({ phase: 'server', percent: 100 })
      })

      it('没有 progress 事件也要能进入 finishing（小文件/事件被合并）', async () => {
        mockPresign({ code: 1 })
        const seen: any[] = []
        const p = uploadFile(file(), { onProgress: (x) => seen.push(x) })
        await flush()

        FakeXHR.instances[0].uploaded()
        expect(seen.at(-1).phase).toBe('finishing')

        FakeXHR.instances[0].finish(200, okMultipart())
        await p
      })

      it('PUT 失败回退：失败的直传不该留下 finishing 100% 的假成功', async () => {
        mockPresign({ code: 0, data: { putUrl: 'https://oss/put', url: 'https://cdn/x.png' } })
        const seen: any[] = []
        const p = uploadFile(file(), { onProgress: (x) => seen.push(x) })
        await flush()

        FakeXHR.instances[0].uploaded()
        FakeXHR.instances[0].finish(500)
        await flush()
        expect(seen.some((x) => x.phase === 'direct' && x.percent === 100)).toBe(false)
        expect(seen.at(-1)).toMatchObject({ phase: 'server', percent: 0 })

        FakeXHR.instances[1].finish(200, okMultipart())
        await p
      })
    })

    it('lengthComputable=false 时不产生 NaN 百分比', async () => {
      mockPresign({ code: 1 })
      const seen: any[] = []
      const p = uploadFile(file(), { onProgress: (x) => seen.push(x) })
      await flush()

      FakeXHR.instances[0].progress(0, 0, false)
      expect(seen.every((x) => Number.isFinite(x.percent))).toBe(true)
      FakeXHR.instances[0].finish(200, okMultipart())
      await p
    })
  })
})
