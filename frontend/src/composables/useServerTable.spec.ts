import { describe, it, expect, vi, beforeEach } from 'vitest'
import { nextTick } from 'vue'
import { useServerTable } from './useServerTable'
import type { Paged } from '@/api/request'

// 可控的 Promise：用来制造「先发的请求后回来」这种真实网络乱序，
// 靠 sleep 时序断言在 CI 上会抖。
function deferred() {
  let resolve!: (v: Paged) => void
  let reject!: (e: any) => void
  const promise = new Promise<Paged>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

// useServerTable 把「页码 / 每页条数 / 关键字」都放进请求参数，由服务端切片。
// 这里用假的 loader 验证三件事：翻页即重载、改每页条数即重载、
// 关键字防抖后回到第 1 页（否则「第 3 页 + 新关键字」大概率是空结果）。
describe('useServerTable', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  function setup(overrides: Partial<Paged> = {}) {
    const calls: Array<{ page: number; size: number; keyword: string }> = []
    const loader = vi.fn(async (q: { page: number; size: number; keyword: string }) => {
      calls.push(q)
      return { list: [{ id: q.page }], total: 100, page: q.page, size: q.size, ...overrides } as Paged
    })
    const t = useServerTable(loader, { pageSize: 10, debounce: 300 })
    return { ...t, loader, calls }
  }

  it('初次 load 带上默认页码与每页条数', async () => {
    const { load, loader } = setup()
    await load()
    expect(loader).toHaveBeenCalledWith({ page: 1, size: 10, keyword: '' })
  })

  it('翻页 / 改每页条数会触发重载，并把参数传下去', async () => {
    const { load, loader, currentPage, size } = setup()
    await load()
    currentPage.value = 3
    await nextTick()
    expect(loader).toHaveBeenLastCalledWith({ page: 3, size: 10, keyword: '' })

    size.value = 20
    await nextTick()
    expect(loader).toHaveBeenLastCalledWith({ page: 3, size: 20, keyword: '' })
    expect(loader).toHaveBeenCalledTimes(3)
  })

  it('关键字防抖后回到第 1 页再查（避免停在第 3 页查到空页）', async () => {
    const { load, loader, keyword, currentPage } = setup()
    await load()
    currentPage.value = 3
    await nextTick()

    keyword.value = '张三'
    await nextTick() // 等 Vue 把 watch 回调跑起来（防抖计时器是在回调里起的）
    // 防抖窗口内不请求
    vi.advanceTimersByTime(200)
    expect(loader).toHaveBeenCalledTimes(2)

    vi.advanceTimersByTime(200)
    await nextTick()
    await nextTick()
    expect(currentPage.value).toBe(1)
    expect(loader).toHaveBeenLastCalledWith({ page: 1, size: 10, keyword: '张三' })
  })

  it('后端把越界页码夹回末页时，以回包页码为准（界面不停在空页）', async () => {
    const { rows, total, load, currentPage } = setup({ page: 5, total: 42 })
    currentPage.value = 99
    await load()
    expect(currentPage.value).toBe(5)
    expect(total.value).toBe(42)
    expect(rows.value).toHaveLength(1)
  })

  it('loader 抛错时不把界面卡在 loading（错误提示由请求拦截器负责）', async () => {
    const t = useServerTable(async () => {
      throw new Error('boom')
    })
    await t.load()
    expect(t.loading.value).toBe(false)
    expect(t.rows.value).toEqual([])
  })

  it('并发乱序：先发的请求后回来，不能盖掉新一页的数据与总数', async () => {
    const first = deferred()
    const second = deferred()
    let n = 0
    const loader = vi.fn(() => (n++ === 0 ? first.promise : second.promise))
    const t = useServerTable(loader)

    const p1 = t.load()
    const p2 = t.load()
    second.resolve({ list: [{ id: 2 }], total: 2, page: 1, size: 20 } as Paged)
    await p2
    // 旧响应最后才到：以前这里会把 rows 改成第 1 页的数据、total 改成 1
    first.resolve({ list: [{ id: 1 }], total: 1, page: 1, size: 20 } as Paged)
    await p1

    expect(t.rows.value).toEqual([{ id: 2 }])
    expect(t.total.value).toBe(2)
    expect(t.loading.value).toBe(false)
  })

  it('并发乱序：先发的请求失败，不能把后发请求的 loading 提前清掉', async () => {
    const first = deferred()
    const second = deferred()
    let n = 0
    const loader = vi.fn(() => (n++ === 0 ? first.promise : second.promise))
    const t = useServerTable(loader)

    const p1 = t.load()
    const p2 = t.load()
    first.reject(new Error('boom'))
    await p1
    // 新请求还在飞：旧请求的 finally 不该把 loading 提前清掉
    expect(t.loading.value).toBe(true)

    second.resolve({ list: [], total: 0, page: 1, size: 20 } as Paged)
    await p2
    expect(t.loading.value).toBe(false)
  })
})
