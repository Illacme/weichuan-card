import { defineStore } from 'pinia'
import { listCompanies, type CompanyItem } from '@/api/tenants'
import { setCompanyScope } from './companyScope'

/**
 * 「当前正在管理的企业」——平台超管的跨租户视角。
 *
 * 背景：多租户后台有两级身份。
 *   - 企业管理员：数据天然只有本企业一份，无需选择；
 *   - 平台超管：可见全部租户，但「企业内容 / 数据统计 / 审核管理」这些页面一次只能对着
 *     一家企业看和改。缺了这个选择器，平台超管打开企业内容页会被后端兜到 1 号企业
 *     （零数据的商用版里 1 号企业可能根本不存在），改着 B 家资料写进别人家。
 *
 * 存下来的 currentId 只影响**请求参数**（见 store/companyScope.ts 与 api/request.ts 的
 * 请求拦截器），不改任何身份或权限——后端仍按令牌里的 company_id 鉴权，
 * 企业管理员永远只能碰自己那家。
 */
const LS_KEY = 'admin_scope_company_id'

/** 后端 maxPageSize = 100：一次尽量拉全，超出的靠关键字远程搜索。 */
const PAGE_SIZE = 100

export const useTenantStore = defineStore('tenant', {
  state: () => ({
    /** 候选企业（平台超管才有值；关键字搜索时被替换为搜索结果） */
    companies: [] as CompanyItem[],
    /** 满足当前关键字的企业总数（> companies.length 说明还有没拉到的） */
    total: 0,
    loading: false,
    loaded: false,
    keyword: '',
    /** 当前管理的企业 id；0 = 未选择 */
    currentId: Number(localStorage.getItem(LS_KEY) || 0) || 0,
    /**
     * 本次会话的身份：平台超管才需要选企业。
     * 初值取 localStorage，是为了让「刷新页面后第一帧」就能算对 needPick ——
     * 子页面在挂载时立刻发请求，等 /api/admin/me 回来再判断已经晚了。
     * AdminLayout 的 setup 会再同步一次（服务端为准）。
     */
    isPlatform: localStorage.getItem('admin_is_platform') === '1',
    /** 企业管理员自己的企业 id（平台超管为 0，仅用于展示） */
    ownCompanyId: 0,
  }),
  getters: {
    current: (s): CompanyItem | null => s.companies.find((c) => c.id === s.currentId) || null,
    /** 顶栏展示名：优先简称（与侧边栏品牌名同源），没设简称用全名 */
    currentName(): string {
      const c = this.current
      return c ? c.shortName || c.name : ''
    },
    /** 平台超管还没选企业：企业内容/统计/审核/员工页据此显示空态 */
    needPick: (s) => s.isPlatform && s.currentId <= 0,
    /** 企业太多时提示「只列出前 N 家，输入关键字搜索」 */
    truncated: (s) => s.total > s.companies.length,
  },
  actions: {
    /**
     * 身份确定后同步（登录成功 / 刷新后 hydrate / 登出）。
     * 企业管理员固定「本企业」且不注入 companyId——后端本来就只认他自己的企业，
     * 多传一个参数只会让「谁在收窄」这件事变得含糊。
     */
    syncIdentity(isPlatform: boolean, ownCompanyId: number) {
      this.isPlatform = isPlatform
      this.ownCompanyId = ownCompanyId
      if (!isPlatform) {
        this.currentId = ownCompanyId
        this.companies = []
        this.loaded = false
      }
      setCompanyScope(this.currentId, isPlatform)
    },

    /**
     * 拉候选企业（平台超管专用）。
     * force=false 时只在未加载过时拉一次——顶栏选择器每个页面都要用，不能每次挂载都打一次接口。
     */
    async ensureLoaded(force = false) {
      if (!this.isPlatform) return
      if (this.loaded && !force) return
      await this.fetch(this.keyword)
    },

    /** 关键字远程搜索（下拉里输入时调用）；空关键字回到全量列表 */
    async search(keyword: string) {
      if (!this.isPlatform) return
      this.keyword = keyword.trim()
      await this.fetch(this.keyword)
    },

    async fetch(keyword: string) {
      this.loading = true
      try {
        const paged = await listCompanies({ page: 1, size: PAGE_SIZE, keyword: keyword || undefined })
        this.companies = paged.list
        this.total = paged.total
        this.loaded = true
        // 选中的企业已不存在（被删 / 换了数据库 / 关键字搜索把结果过滤掉了）：
        // 关键字搜索时不做校正，否则一输入就被清空。
        if (!keyword && this.currentId > 0 && !this.companies.some((c) => c.id === this.currentId)) {
          this.setCurrent(0)
        }
      } catch {
        // 拉取失败不清空已有候选：顶栏选择器宁可显示旧列表，也不要突然变空让人以为企业没了
        this.loaded = true
      } finally {
        this.loading = false
      }
    },

    /** 切换当前企业（0 = 取消选择） */
    setCurrent(id: number) {
      this.currentId = id > 0 ? id : 0
      if (this.currentId > 0) localStorage.setItem(LS_KEY, String(this.currentId))
      else localStorage.removeItem(LS_KEY)
      setCompanyScope(this.currentId, this.isPlatform)
    },

    /** 登出：连同 localStorage 一起清，换人登录不该继承上一个人的视角 */
    clear() {
      this.companies = []
      this.total = 0
      this.loaded = false
      this.keyword = ''
      this.isPlatform = false
      this.ownCompanyId = 0
      this.currentId = 0
      localStorage.removeItem(LS_KEY)
      setCompanyScope(0, false)
    },
  },
})
