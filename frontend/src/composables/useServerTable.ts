import { getCurrentInstance, onUnmounted, ref, watch, type Ref } from 'vue'
import type { Paged } from '@/api/request'

/**
 * 服务端分页表格（关键字 + 页码 + 每页条数都在请求参数里，由后端切片）。
 *
 * 与 useTable（纯前端切片）的分工：
 *   - 数据量会随运营增长、且后端已支持分页参数的列表（员工 / 企业 / 申请 / 账号）用本函数；
 *   - 数据量天然有界、需要拖拽排序的列表（分类 / 项目 / 服务）继续用 useTable。
 *
 * @param loader  接收查询条件、返回 Paged 的接口调用
 * @param opts    pageSize 默认每页条数；debounce 关键字输入防抖（毫秒）
 */
export function useServerTable<T = any>(
  loader: (q: { page: number; size: number; keyword: string }) => Promise<Paged<T>>,
  opts: { pageSize?: number; debounce?: number } = {}
) {
  const rows = ref<T[]>([]) as Ref<T[]>
  const keyword = ref('')
  const currentPage = ref(1)
  const size = ref(opts.pageSize ?? 20)
  const total = ref(0)
  const loading = ref(false)

  // 请求序号：快速翻页 / 连打关键字时，先发的请求可能后回来。不设序号的话，
  // 旧响应会把新一页的数据盖掉，还会把 currentPage 拨回旧响应的 page——
  // 界面显示「第 1 页」却是第 3 页的数据，且没有任何报错。
  // 这里只认最后一次发出的请求；AbortSignal 那种「真的取消掉旧请求」是请求层
  // 的事，此处用序号保证即使旧请求没被取消也绝不影响界面。
  let requestSeq = 0
  let disposed = false

  async function load() {
    const seq = ++requestSeq
    loading.value = true
    try {
      const p = await loader({
        page: currentPage.value,
        size: size.value,
        keyword: keyword.value.trim(),
      })
      if (disposed || seq !== requestSeq) return // 已有更新的请求，丢弃这次结果
      rows.value = p?.list ?? []
      total.value = p?.total ?? 0
      // 后端会把越界页码夹到末页：以回包的页码为准，避免界面停在已被夹走的页号
      if (p?.page && p.page !== currentPage.value) currentPage.value = p.page
    } catch {
      // 拦截器已提示（401/403/业务失败），这里只保证不把界面卡在 loading。
      // 旧请求失败也不能清掉新请求的 loading，交给下面的 finally 按序号判断。
    } finally {
      if (!disposed && seq === requestSeq) loading.value = false
    }
  }

  // 翻页 / 改每页条数：立即重载
  watch([currentPage, size], () => load())

  // 关键字：防抖后回到第 1 页再查（否则「第 3 页 + 新关键字」大概率是空结果）
  let timer: ReturnType<typeof setTimeout> | undefined
  watch(keyword, () => {
    if (timer) clearTimeout(timer)
    timer = setTimeout(() => {
      if (currentPage.value !== 1) {
        currentPage.value = 1 // 由上面的 watcher 触发一次加载，避免重复请求
        return
      }
      load()
    }, opts.debounce ?? 300)
  })
  // 组件卸载后不再残留待触发的关键字查询（组件外使用——如单测——则跳过）
  if (getCurrentInstance()) {
    onUnmounted(() => {
      disposed = true
      requestSeq++ // 让在飞的请求回来后直接被丢弃，不再写已卸载组件的 ref
      if (timer) clearTimeout(timer)
    })
  }

  return { rows, keyword, currentPage, size, total, loading, load }
}
