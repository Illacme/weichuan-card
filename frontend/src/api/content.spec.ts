// content.ts 会 import ./request（HTTP 客户端 + 401 跳转），牵出 router / store /
// element-plus 一整条依赖链。这里只验 probeVideoUrl 的入参与信封解包，把请求层打桩。
vi.mock('./request', () => ({
  default: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
}))

import { describe, expect, it, vi, beforeEach } from 'vitest'
import http from './request'
import { probeVideoUrl } from './content'

describe('probeVideoUrl（后台测试地址）', () => {
  beforeEach(() => vi.clearAllMocks())

  it('POST /admin/url-probe，带 __silent，只回信封里的 data', async () => {
    const payload = {
      reachable: false,
      status: 404,
      contentType: '',
      contentLength: 0,
      message: 'HTTP 404 找不到对象：检查文件访问基址是否带上了对象存储里的目录前缀',
    }
    ;(http.post as any).mockResolvedValue({ code: 0, data: payload })

    const r = await probeVideoUrl('https://cdn.example.com/a.mp4')

    expect(http.post).toHaveBeenCalledTimes(1)
    expect(http.post).toHaveBeenCalledWith(
      '/admin/url-probe',
      { url: 'https://cdn.example.com/a.mp4' },
      expect.objectContaining({ __silent: true })
    )
    expect(r).toEqual(payload)
  })

  // __silent 不能丢：丢了会同时弹一条全局红条 + 按钮旁边再显示一遍同样的原因，
  // 而「测试地址」不通本来就是预期结果之一，不是需要打断操作的错误。
  it('不带 __silent 时拦截器会多弹一条红条（守门断言 config 真的传了）', async () => {
    ;(http.post as any).mockResolvedValue({ code: 0, data: { reachable: true } })
    await probeVideoUrl('https://cdn.example.com/a.mp4')
    const cfg = (http.post as any).mock.calls[0][2]
    expect(cfg.__silent).toBe(true)
  })
})
