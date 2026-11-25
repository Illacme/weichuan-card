// 名片墙：企业全员名片列表，小程序**裸启动**时的落地页。
//
// 为什么需要这一页：扫「体验版二维码」或直接打开小程序属于裸启动——微信不会带任何
// 参数，页面拿不到 staffId。此前首页在这种情况下会弹一个「输入员工编号」的输入框
// （开发期兜底，注释里写着「联调取数 / 演示用」），真实访客看到的第一个界面就是
// 一个要求输入数字的弹窗。名片墙把落点换成「这家公司有谁」，点谁看谁的名片。
//
// 三条边界：
//  1. 只画公开字段（姓名/职位/头像/编号）——后端也做了白名单，两边都不能松；
//  2. 企业不可用（不存在/停用）与「公司还没人」是两种状态，文案必须分开：
//     前者要说「不可用」，后者要说「还没有人开通名片」，否则访客以为系统坏了；
//  3. 跳名片页要带上 companyId：平台模式下不传就会被后端兜到 1 号企业，
//     点进去看到的是别人家公司的人。
//
// 分页与搜索（2026-10-30）：
//   - 原来这一页一次拉全员（后端上限 300），500 人以上的企业只能看到前 300 位，
//     而且没有任何入口看到剩下的人——等于不能用。现在改成**首屏一页 60** +
//     「查看全部 N 位同事」进 pages/team/all 做滚动加载；
//   - 搜索框就地搜索（同一个接口带 keyword），只拉第一页；想看全部匹配结果
//     同样走「查看全部」并带上关键字。
//   - 首屏只拉 60 而不是 300：大企业首屏不必等 300 条 JSON 解析完才出画面。
import { request, assetUrl } from '../../utils/request'
import { thumbnailURL, onThumbError, onImgFail } from '../../utils/thumb'
import { FALLBACK_BRAND, applyBrandTitle, cachedBrandName } from '../../utils/brand'
import { mapTeamList, teamParams, hasMoreOf } from '../../utils/team'
import { readEntryStaff, writeEntryStaff, clearEntryStaff } from '../../utils/entry'
import { getCompanyId } from '../../utils/tenant'
import { bindTheme } from '../../utils/theme'
import { latestGuard, isSuperseded } from '../../utils/latest'

// 系统导航栏兜底清理（team.json 已改 custom 导航，正常情况用不到）。
// 保留这里的 setNavigationBarTitle 是为了老基础库 / 调试器里万一仍画出
// 系统栏时不闪「团队成员」；真正的首帧保证来自 team.json 不再写静态标题。
function setNavTitle(title: string) {
  try {
    wx.setNavigationBarTitle({ title })
  } catch {
    // 标题是装饰，失败不能影响主流程
  }
}

Page({
  data: {
    themeClass: '',
    list: [],
    total: 0,
    // 还有更多人（后端 hasMore）：底部据此显示「查看全部 N 位同事」。
    // 不能用 total > list.length 推——搜索时 total 是匹配总数，list 只有第一页，
    // 两者相减得到的是「没加载的匹配数」，不是「还有更多人」这个布尔语义。
    hasMore: false,
    // 搜索关键字（input 的 value 双向绑定）
    keyword: '',
    // 已提交给接口的关键字：与 keyword 分开，避免用户边打字边发请求
    // （输入框里正在输入的字符还没确认，不该触发网络请求）
    activeKeyword: '',
    // 企业抬头：**初始为空，等接口回来再填**。
    //
    // 这里原来写的是 FALLBACK_BRAND（产品名「微传名片」）。问题在于：企业配了
    // 「默认进某位员工名片」时，这一页只是个中转站，弱网下会停 1~3 秒，
    // 访客看到的就是「微传名片 + 加载中」——自家公司的小程序顶着平台名，
    // 既莫名其妙，也像是没配置好（2026-11-04 反馈）。
    // 空串时 WXML 整块不渲染（见 team.wxml 的 wx:if），加载期只剩一个干净的加载提示。
    // 真拿不到企业名时的兜底仍然保留，在 load() 里——那时至少是「加载完了但没有名字」，
    // 和「还没加载完」是两回事。
    title: '',
    logo: '',
    // LOGO 加载失败的地址：WXML 用 `logo && logoFail !== logo` 判断是否还画它
    // （见 utils/thumb.ts onImgFail——记 URL 而不是布尔标记，换 LOGO 自动重试）
    logoFail: '',
    companyId: 0,
    loading: true,
    // 启动遮罩：首帧标题在模块求值阶段同步读缓存，不等 onLoad 再 setData。
    // 没配 brand_name 就留白，不回落产品名——中转页不该闪平台品牌。
    bootTitle: cachedBrandName(),
    bootVisible: true,
    bootFade: false,
    // 自定义导航（team.json navigationStyle=custom）：状态栏高度与整体高度。
    // 系统导航栏在页面脚本执行前就会按 json 画出来，onLoad 里 setNavigationBarTitle
    // 已经来不及——真机仍会闪一帧静态「团队成员」。改成自绘后，标题完全由页面控制。
    statusBarHeight: 20,
    navHeight: 64,
    // 自绘导航标题：首屏取后台已配置的 brand_name（没配就空串），
    // 确认留在名片墙后换成企业简称，错误态换成中性标题。
    // 不在这里写死「团队成员」或产品名——首帧必须由配置决定。
    navTitle: '',
    // 首屏决策态：还不知道该停在名片墙还是改道去指定员工名片。
    // 初始必须为 true——裸启动第一帧不能露出任何企业内容（导航栏、搜索框、
    // LOGO、宫格都不行），否则弱网下会一段段冒出来，最后再整页跳走路。
    // 只有确认「留在名片墙」或「出错」时才落 false；改道态保持 true 直到页面卸载。
    booting: true,
    // 正在按「上次记住的默认入口」改道去名片页：整页不渲染任何内容，
    // 否则会先闪一下名片墙（甚至闪一句「还没有可展示的名片」）再跳走。
    redirecting: false,
    error: false,
    // 企业不存在 / 已停用（后端 code=1），与「公司暂时没有员工」区分开
    unavailable: false,
  },

  // 搜索 / 清空 / 下拉刷新共用一个守门：连续换关键字时，旧关键字的响应晚回
  // 会把列表填回旧结果（用户点中的人反而不在列表里），且不报错、不打日志。
  guard: null as any,

  onUnload() {
    this.guard?.cancel()
  },

  // 遮罩淡出结束后才撤节点。没有定时器：transitionend 没触发时它只是
  // opacity:0 + pointer-events:none 的空壳，不会再挡住任何点击。
  onBootFadeEnd() {
    if (!this.data.booting && this.data.bootFade) {
      this.setData({ bootVisible: false, bootFade: false })
    }
  },
  // catchtouchmove 需要在页面里有对应处理函数，否则控制台会报 noop is not a function。
  noop() {},

  onLoad(query: any) {
    // 先算租户与搜索态：缓存命中时不需要量状态栏 / 绑主题 / 建请求守门。
    // 这几步加起来在弱机上正好是一帧，而这一页注定要被 reLaunch 掉。
    const cid = parseInt((query && query.companyId) || '0', 10)
    const kw = ((query && query.keyword) || '').trim()
    const skipEntryCache = String((query && query.skipEntryCache) || '') === '1'
    const entryKey = cid > 0 ? cid : getCompanyId()
    // 本次生效的租户，贯穿整页生命周期（见 entryKey()）。
    // query 显式带的 companyId 最权威（扫码 / 分享链），否则用本机粘滞值。
    ;(this as any)._entryKey = entryKey

    // 首帧标题已在 data 里同步读好；只有本次会话里缓存被后台刷新过才补一次。
    const bootTitle = cachedBrandName()
    if (bootTitle !== this.data.bootTitle) this.setData({ bootTitle })

    // 兼容旧基础库 / 调试器：自定义导航被忽略时先抹掉静态标题。
    // 真正的首帧保证来自 team.json 不再写死标题。
    setNavTitle('')

    // 上次已经知道「这家企业默认进某位员工的名片」：直接改道，一次请求都不发。
    // 弱网下省掉的正是整个中转页停留（原来要等完一次往返才 reLaunch）。
    // 搜索态不跳：用户是来找人的，被弹进某人的名片会觉得「我搜的人呢」。
    // skipEntryCache 是自愈回流，再跳就会形成死循环。
    const cached = kw || skipEntryCache ? 0 : readEntryStaff(entryKey)
    if (cached > 0) {
      const target = cid > 0 ? cid : getCompanyId()
      this.setData({ redirecting: true, loading: false })
      wx.reLaunch({
        url:
          '/pages/index/index?staffId=' +
          cached +
          (target > 0 ? '&companyId=' + target : '') +
          // 告诉名片页这次是「凭缓存跳过来的」：万一那位员工已被停用 / 删除，
          // 它负责清缓存并把访客送回名片墙（见 pages/index/index.ts）。
          '&fromEntryCache=1',
      })
      return
    }

    // 到这里才确认本页可能真的要留下：主题、状态栏、请求守门都属于墙态成本。
    bindTheme(this)
    // 自定义导航先量状态栏：避免在刘海屏上把标题顶到状态栏里。
    // 失败时用 20/64 的保守值，与 wxml 初值一致。
    let sb = 20
    try {
      const info = wx.getSystemInfoSync()
      if (info && info.statusBarHeight) sb = info.statusBarHeight
    } catch {}
    this.setData({
      statusBarHeight: sb,
      navHeight: sb + 44,
      // 没配 brand_name 就留白；墙态拿到企业名后会被 load() 覆盖。
      navTitle: bootTitle,
    })
    if (!this.guard) this.guard = latestGuard()

    // 平台模式下企业可能由扫码 scene 带进来；query 里的 companyId 只用于
    // 兜底展示，实际请求的 companyId 由 utils/request 按租户语义统一注入。
    const patch: Record<string, unknown> = {}
    if (cid > 0) patch.companyId = cid
    // 从「查看全部」返回时把关键字带回来，否则用户会以为自己搜的东西丢了
    if (kw) patch.keyword = patch.activeKeyword = kw
    if (Object.keys(patch).length) this.setData(patch)

    // 名片页发现「缓存里的默认员工已经不可用」时会带这个标记把人送回来：
    // 清掉缓存，并且这一次老老实实走网络、直接渲染名片墙，**不再改道**。
    if (skipEntryCache) {
      clearEntryStaff(entryKey)
      this.load({ noRedirect: true })
      return
    }

    this.load()
  },

  // 「默认入口」缓存的租户键。
  //
  // 为什么不能每处自己算一次：读的时候用 query 的 companyId、写的时候用
  // getCompanyId()，两者不同就会「写得进、读不到」——缓存看着有，实际永不命中，
  // 而且不报错。所以 onLoad 里算一次存下，全程只认它。
  entryKey(): number {
    const k = (this as any)._entryKey
    return typeof k === 'number' ? k : getCompanyId()
  },

  // 下拉刷新走 silent：不把整页切成「加载中」，否则内容会闪一下再回来。
  //
  // noRedirect（2026-11-04）：本次即使后端说「该进某位员工的名片」也不改道。
  // 只有一种调用方——名片页发现缓存里的默认员工已失效、把人送回来时（skipEntryCache）。
  // 少了这个开关就会死循环：这里清掉缓存 → 又按后端结果写回去并 reLaunch →
  // 名片页又说「员工不存在」→ 再送回来…… 来回跳。
  async load(opts?: { silent?: boolean; noRedirect?: boolean }) {
    const noRedirect = !!(opts && opts.noRedirect)
    if (!this.guard) this.guard = latestGuard()
    const guard = this.guard
    const sig = guard.next()
    const patch: Record<string, unknown> = { error: false, unavailable: false }
    if (!opts || !opts.silent) patch.loading = true
    this.setData(patch)
    try {
      const r: any = await request('GET', '/api/team', teamParams(1, this.data.activeKeyword), sig)
      // 旧轮响应（或已被取消后仍回来的）一律不写回：包括下面的默认入口改道——
      // 否则用旧关键字发起的请求会把正在搜人的访客弹进某个人的名片。
      if (!guard.isCurrent(sig)) return
      if (r.code !== 0) {
        // 企业不存在 / 停用：这不是网络问题，重试也没用，给明确说法
        // 决策已结束（落在错误态），booting 必须收掉，否则错误页也被启动遮罩挡住。
        setNavTitle('团队成员')
        this.setData({
          booting: false,
          bootFade: true,
          loading: false,
          unavailable: true,
          navTitle: '团队成员',
        })
        return
      }
      const comp = r.company || {}
      // 企业在后台配了「默认进入指定员工的名片」（一张名片打天下的用法）→ 直接改道，
      // 不渲染名片墙。后端已经把「指定员工是否还有效」校验过并回落成 wall，
      // 所以这里放心按 mode 走。
      //
      // 用 reLaunch 而不是 redirectTo：名片墙是 entryPagePath，栈里只有它自己；
      // reLaunch 清栈重建，访客按返回键直接退出小程序，而不是退回一张他从没看过的墙。
      //
      // 只在**没有搜索**时改道：用户在墙上搜人时突然被弹进某个人的名片，
      // 会觉得「我搜的人呢」。
      const entry = r.entry || {}
      const entryStaff = Number(entry.staffId) || 0
      if (entry.mode === 'staff' && entryStaff > 0 && !this.data.activeKeyword && !noRedirect) {
        // 记住这次结果：下次裸启动直接进名片页，不必再等这一趟往返（见 utils/entry.ts）。
        // 键用 entryKey()——必须与 onLoad 里读缓存时**同一个键**，
        // 否则「写得进、读不到」，缓存等于没有。
        writeEntryStaff(this.entryKey(), entryStaff)
        const cid = Number(comp.id) || this.data.companyId
        // 保持启动遮罩不淡出：旧页在 reLaunch 完成前不能露出任何墙态内容。
        this.setData({ redirecting: true, loading: false })
        wx.reLaunch({
          url:
            '/pages/index/index?staffId=' +
            entryStaff +
            (cid > 0 ? '&companyId=' + cid : ''),
        })
        return
      }
      // 没配默认入口（或后端已回落成名片墙）：把可能残留的旧缓存清掉。
      // 不清的话，管理员取消这个配置之后，访客还会一直被默默跳到那位员工的名片。
      // 搜索态不碰缓存：搜索结果与「企业默认入口」是两回事。
      if (!this.data.activeKeyword) clearEntryStaff(this.entryKey())
      const list = mapTeamList(r.data)
      const total = Number(r.total) || 0
      const title = comp.shortName || comp.fullName || FALLBACK_BRAND
      this.setData({
        // 到这里才确认「本页就是最终落点」：一次性放出企业抬头、搜索框与宫格。
        // 图片仍会异步到达，但至少不再出现「先标题、再搜索、再 LOGO、再列表」的
        // 分帧拼装感——结构同一帧出现，只有图片本身渐进解码。
        booting: false,
        bootFade: true,
        navTitle: title,
        list,
        total,
        hasMore: hasMoreOf(r),
        title,
        logo: assetUrl(comp.logo),
        // 抬头里的 id 比 query 更权威（后端按实际生效租户回传）
        companyId: Number(comp.id) || this.data.companyId,
        loading: false,
      })
      // 导航栏标题与页面抬头一致；企业信息为空时回落品牌名
      applyBrandTitle({ shortName: comp.shortName, fullName: comp.fullName })
    } catch (e) {
      // 被取消 / 已被新轮取代：不是故障，别把整页打成错误态，也别清掉新轮的 loading
      if (isSuperseded(e) || !guard.isCurrent(sig)) return
      setNavTitle('团队成员')
      this.setData({
        booting: false,
        bootFade: true,
        loading: false,
        error: true,
        navTitle: '团队成员',
      })
    }
  },

  // 下拉刷新：静默重拉，无论成败都收掉顶部指示器（不收会一直挂着转）。
  async onPullDownRefresh() {
    try {
      await this.load({ silent: true })
    } catch {
      // 错误态由 load 自己落（error 标记）；这里只兜网络异常，
      // 避免 onPullDownRefresh 抛出未捕获的 Promise rejection
    } finally {
      wx.stopPullDownRefresh()
    }
  },

  // ---- 搜索 ----
  //
  // 输入时不发请求（每敲一个字就打一次接口，既费流量又会让宫格闪烁）；
  // 只在键盘「搜索」键 / 点搜索按钮时提交。
  onKeywordInput(e: any) {
    this.setData({ keyword: (e && e.detail && e.detail.value) || '' })
  },

  onSearch() {
    const kw = (this.data.keyword || '').trim()
    if (kw === this.data.activeKeyword) return
    this.setData({ activeKeyword: kw })
    this.load()
  },

  onClearSearch() {
    if (!this.data.activeKeyword && !this.data.keyword) return
    this.setData({ keyword: '', activeKeyword: '' })
    this.load()
  },

  // 进「查看全部」：带上当前企业与已提交的关键字，让全部页接着搜同一个词
  goAll() {
    const cid = this.data.companyId
    const kw = this.data.activeKeyword
    const qs =
      (cid > 0 ? 'companyId=' + cid : '') +
      (kw ? (cid > 0 ? '&' : '') + 'keyword=' + encodeURIComponent(kw) : '')
    wx.navigateTo({ url: '/pages/team/all' + (qs ? '?' + qs : '') })
  },

  onTap(e: any) {
    const staffId = parseInt(e.currentTarget.dataset.id, 10) || 0
    if (staffId <= 0) return
    // 带上 companyId：不传的话平台模式下后端会兜到 1 号企业，
    // 点进去看到的是别家公司的人（见 utils/request.ts 的租户注入约定）
    const cid = this.data.companyId
    wx.navigateTo({
      url:
        '/pages/index/index?staffId=' +
        staffId +
        (cid > 0 ? '&companyId=' + cid : ''),
    })
  },

  onShareAppMessage() {
    // 名片墙本身适合整包转发（「看我们团队」），落点就回到名片墙
    return { title: this.data.title, path: '/pages/team/team' }
  },

  onImgErr: onThumbError,
  onImgFail,
})
