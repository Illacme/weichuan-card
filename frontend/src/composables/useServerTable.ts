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

  async function load() {
    loading.value = true
    try {
      const p = await loader({
        page: currentPage.value,
        size: size.value,
        keyword: keyword.value.trim(),
      })
      rows.value = p?.list ?? []
      total.value = p?.total ?? 0
      // 后端会把越界页码夹到末页：以回包的页码为准，避免界面停在已被夹走的页号
      if (p?.page && p.page !== currentPage.value) currentPage.value = p.page
    } catch {
      // 拦截器已提示（401/403/业务失败），这里只保证不把界面卡在 loading
    } finally {
      loading.value = false
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
      if (timer) clearTimeout(timer)
    })
  }

  return { rows, keyword, currentPage, size, total, loading, load }
}
