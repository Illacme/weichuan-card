import { request, assetUrl, apiBaseUrl } from '../../utils/request'
import { track } from '../../utils/track'
import { solarTermOfDate, termFirst, type SolarTerm } from '../../utils/solarTerm'
import { festivalOfDate, festivalFirst, type Festival } from '../../utils/festival'
import { ensurePrivacy } from '../../utils/privacy'
import { applyBrandTitle } from '../../utils/brand'
import { onImgFail } from '../../utils/thumb'

// 默认海报背景：节气会过时（立春的图在十月发很怪），商务款才能一直用。
// 取不到清单或清单里没有这张时退回白底（BG_NONE）。
// 只有「当天正好是节日 / 节气」时才让位给当天那张，见 buildState。
const DEFAULT_BG = 'poster-brand'
// 「不换背景」的哨兵值：空串 = 白底卡片（海报素材拉不到时的兜底形态）
const BG_NONE = ''
// 本地存储键（按员工分键：同一台手机可能看过不同同事的名片）
const BG_KEY = 'posterBg'
const LAYOUT_KEY = 'posterLayout'
// 「原样」的本地存储哨兵：空串在 wx.getStorageSync 里与「键不存在」同义，
// 不区分开就无法判断「用户明确选了白底」还是「这人从没选过」。
const BG_NONE_STORE = 'none'

// 画布逻辑尺寸（CSS px）。素材是它的精确 2 倍（640×920），零裁切。
const CANVAS_W = 320
const CANVAS_H = 460
// 内置海报素材顶部烧了标题（节气名 / 场景名），员工信息必须从这之下开始，
// 否则会压在标题上。自定义背景和白底没有这段标题，内容可以更靠上。
const TITLE_ZONE = 150
const PLAIN_TOP = 24
// 「长按识别名片」基线：贴着画布底，留 14px 呼吸
const HINT_Y = CANVAS_H - 14
// 二维码底边：给下面的提示文字让出位置
const QR_BOTTOM = HINT_Y - 18
const QR_MAX = 104
const QR_MIN = 56
// 用户可选的二维码尺寸档位。0 = 自动（按内容高度自适应，内容越高码越小）。
//
// 中档 104 就是改造前自动算出的上限——「没动过设置的人看到的海报一模一样」。
// 大档 140 是给「扫得到最重要」的场景（贴群里、打印出来）：320 宽画布上占 44%，
// 还能和文字共存；再大就必须压掉联系方式了。
// 文案与尺寸绑在一起，wxml 直接渲染，不用在模板里套三元。
const QR_OPTIONS = [
  { size: 0, label: '自动' },
  { size: 76, label: '小' },
  { size: 104, label: '中' },
  { size: 140, label: '大' },
]
const QR_SIZES = QR_OPTIONS.map((o) => o.size)
const QR_MAX_EXPLICIT = 140
// 二维码与上方内容的最小间距
const QR_GAP = 14
// 图片加载失败的锁定时长（毫秒）：见 loadCached 注释
const IMG_FAIL_TTL = 3000

// 版式模板。key 必须与后端 handler/poster.go 的 PosterTemplate* 常量一致，
// 否则保存回去会被白名单归一成 classic（用户选了「居中」却变回「经典」）。
const TEMPLATES = [
  { key: 'classic', name: '经典' },
  { key: 'center', name: '居中' },
  { key: 'minimal', name: '极简' },
  { key: 'card', name: '卡片' },
  { key: 'split', name: '分栏' },
  { key: 'left', name: '大字' },
  { key: 'stack', name: '清单' },
  { key: 'poster', name: '画报' },
]
// 底部工具条：一个工具一行选项。
//
// 为什么不是「背景 / 版式」两个 tab + 五行选项：海报画布本身就占 920rpx，
// 五行选项（模板 / 元素 / 配色 / 头像 / 位置）全铺开必然把「位置」和
// 「保存/分享」顶到屏幕外，用户得先滚动才够得着——而「位置」恰恰是最需要
// 边看海报边调的一项。改成工具条后每次只渲染当前工具的那一行，
// 面板高度从 5 行降到 1 行，全部选项都在一屏内，且「位置」一点就到。
//
// 分组按「调哪个维度」而不是「调哪个对象」：「大小」里同时放头像和码——
// 想「把码放大点」的人不会先去点「头像」找；「位置」里同时放内容区和码，
// 各自一组按钮 + 读数。按对象分（头像 / 位置）会逼用户先想「码算哪个对象的」，
// 而它两个都沾。
const TOOLS = [
  { key: 'bg', name: '背景' },
  { key: 'tpl', name: '版式' },
  { key: 'show', name: '元素' },
  { key: 'text', name: '文字' },
  { key: 'size', name: '大小' },
  { key: 'pos', name: '位置' },
]
// 头像三档（画布坐标）。不做连续滑条：小程序里拖动调尺寸手感很差，
// 且三档已够区分「不抢戏 / 标准 / 突出」。
// 和二维码档位一样带文案，wxml 直接渲染 label——不用在模板里套三元，
// 以后调档位只改这里一处。
const AVATAR_OPTIONS = [
  { size: 56, label: '小' },
  { size: 72, label: '中' },
  { size: 88, label: '大' },
]
const AVATAR_SIZES = AVATAR_OPTIONS.map((o) => o.size)

// 位移：内容块和二维码各有自己的一对偏移，能拖到画布内任意位置。
//
// 「能拖多远」不写死，而是按内容实际包围盒算（见 computeGeometry / shiftRange）：
// 两字姓名和二十字公司全称的可用横向余量差很远，固定区间要么让人拖不动，
// 要么把字拖出画布裁掉。这里只保留按钮微调的步长，和一个挡脏数据的存储上限。
const OFFSET_STEP = 10
// OFFSET_LIMIT 只是「脏数据闸口」（手工改存储 / 老版本写进来的离谱值）。
// 真正的可拖区间由几何算出来，见 Geo.range。与后端 posterOffsetLimit 对齐。
const OFFSET_LIMIT = 480
// EDGE_MARGIN 内容/二维码贴边时四周至少留出的空隙：
// 0 会让文字被画布边缘切掉半个字，太多又浪费本就只有 320px 的宽度。
const EDGE_MARGIN = 8
// HINT_GAP 码底边到提示基线的间距。由默认位置反推：
// 码在默认位置时提示正好落在 HINT_Y，与改造前逐像素一致（升级不惊吓）。
const HINT_GAP = HINT_Y - QR_BOTTOM

const DEFAULT_LAYOUT = {
  template: 'classic',
  showAvatar: true,
  // 姓名可以关掉：有些场景（活动海报 / 只做品牌曝光）不想露个人名字，
  // 关掉后职位自动升为标题行，不会在头像旁边留一条孤零零的小字。
  showName: true,
  showTitle: true,
  showCompany: true,
  showAddress: true,
  showPhone: true,
  // 座机 / 邮箱默认**关**：这两行以前从不出现在海报上，升级后不该让老用户
  // 突然多出两行（他的版式是调好的）。需要的人在「元素」里打开即可。
  showLandline: false,
  showEmail: false,
  // 小程序码开关：关掉就是一张纯形象海报，没有扫码入口。
  // 默认开——它是整张海报唯一的落地入口，关掉要有明确意图。
  showQr: true,
  textColor: 'auto', // auto / light / dark
  // 自定义文字（一句话宣传语 / 服务承诺 / 活动信息）。
  // 存在版式里而不是每次现填：它更像「我的签名」，调好一次以后每张海报都带着；
  // 想临时改也只是改一次，改完就记住了。
  customText: '',
  offsetX: 0,
  offsetY: 0,
  // 二维码单独的位置：与内容互不影响（想码居中就居中，想码靠角就靠角）
  qrOffsetX: 0,
  qrOffsetY: 0,
  // 二维码尺寸档位：0 = 自动（跟着内容高度自适应）。见 QR_SIZES。
  qrSize: 0,
  avatarSize: 72,
}
// 自定义文字长度上限（按字符算）。30 字在 320px 画布上已经要折两行，
// 再长只能靠省略号收尾，不如从输入处就拦住。
const CUSTOM_TEXT_MAX = 30

type Layout = typeof DEFAULT_LAYOUT
// 一个文字块：computeGeometry 只算位置，drawContent 负责画。
// 拆开是为了让版式计算成为纯函数（单测可断言坐标，不必跑 canvas）。
type TextItem = {
  text: string
  x: number
  y: number
  size: number
  weight?: string
  align?: 'left' | 'center' | 'right'
  maxWidth: number
  dim?: boolean // 次要信息（职位/地址）用半透明，弱化层级
  accent?: boolean // 自定义文字用主色强调，和名片信息区分开
}
type Geo = {
  items: TextItem[]
  avatar: { x: number; y: number; size: number } | null
  qr: { x: number; y: number; size: number }
  card: { x: number; y: number; w: number; h: number } | null
  // 主色分隔线（大字 / 画报模板用）：姓名与信息之间的一道细线，
  // 比再加一行文字更省空间，也更容易「看起来像设计过」
  rule: { x: number; y: number; w: number } | null
  light: boolean
  // 提示文字跟着二维码走（码被拖到画布上方时，提示不能还钉在底部）
  // show=false：码被拖到贴近底边，提示放不下，宁可不画也别压在码上
  hint: { x: number; y: number; show: boolean } | null
  // 夹紧后的**实际**位移：拖动时想给的值可能越界，回写存储要用这个值，
  // 否则「拖到边界后松手」会把一个画不出来的偏移存进去
  offsetX: number
  offsetY: number
  qrOffsetX: number
  qrOffsetY: number
  // 可拖区间（相对默认位置的位移，px）。由内容包围盒算出，
  // 拖动时直接按它夹紧——不必每帧重新量文字，也不会把内容拖出画布。
  range: {
    contentXMin: number
    contentXMax: number
    contentYMin: number
    contentYMax: number
    qrXMin: number
    qrXMax: number
    qrYMin: number
    qrYMax: number
  }
}

Page({
  data: {
    staffId: 0,
    card: null as any,
    qrUrl: '',
    tempFilePath: '',
    // 微信隐私授权弹窗是否显示（由 ensurePrivacy 控制）：
    // 选图上传背景、存海报到相册前都要先取得用户对隐私政策的同意。
    privacyVisible: false,
    // 加载 / 失败态：海报是「先取数据 → 再画 canvas → 再导出图片」三步串起来的，
    // 任一步挂了都必须落到失败态并给重试入口——否则用户只看到一块白板，
    // 既不知道在加载也不知道失败了，只能退出重进（此前 Promise.all 没有 catch，
    // 断网时 reject 直接冒泡出 async 函数，页面永远停在空白）。
    loading: true,
    error: false,
    errorText: '海报生成失败',
    // 背景：posters 为内置素材（24 节气 + 20 场景），customs 为员工自己上传的，
    // bgId 为当前选中（BG_NONE = 白底）。清单拉不到时两者为空、bgId 落 BG_NONE，
    // 页面退化成白底海报，不报错。
    posters: [] as any[],
    customs: [] as any[],
    // 缩略图加载失败的地址（按下标记）：WXML 用 `url && xxxFail[index] !== url` 判断。
    // 记 URL 而不是布尔标记——素材列表会重排（节气排最前）、会增删（上传 / 删除），
    // 下标会错位；比较 URL 时错位只会让标记失效（多画一次图），不会误藏别的图。
    customFail: [] as string[],
    posterFail: [] as string[],
    bgId: BG_NONE,
    // 当天命中的素材 id（见 buildState）：空串表示今天既不是节日也不是节气，
    // 或年份超出内置表。wxml 用它给那张缩略图打角标。
    todayId: '',
    // 角标文案（节日名 / 节气名）
    todayName: '',
    // 角标类型：'festival' 节日 / 'term' 节气 / '' 无。两者配色不同，
    // 用户能看出「这张是因为过节」而不是「上次选的」。
    todayKind: '',
    // bgAuto：当前这张背景是「当天是节日/节气」自动挑的，不是用户自己选的。
    // 存偏好时要写回默认款而不是这张当天图——否则用户在节日日顺手调了下版式，
    // 偏好就被固化成「春节」，一个月后进页面还是春节（正是 DEFAULT_BG 注释里
    // 说的「会过时」）。用户一点背景就清零。
    bgAuto: false,
    // 底部面板：工具条（背景 / 版式 / 元素 / 配色 / 头像 / 位置）+ 只显示
    // 当前工具的那一行选项。默认落在背景上——换背景是最高频的操作。
    tool: 'bg',
    tools: TOOLS,
    layout: { ...DEFAULT_LAYOUT } as Layout,
    templates: TEMPLATES,
    avatarOptions: AVATAR_OPTIONS,
    qrOptions: QR_OPTIONS,
    // 输入框 maxlength 由常量提供：两处各写一个 30，改了一边就会静默不一致
    customMax: CUSTOM_TEXT_MAX,
    // 小程序码取不到时（后端未配 AppID / 微信接口抖动）不能静默留白：
    // 海报上画一个占位框 + 页面顶部给一条可重试的提示，
    // 否则用户会拿到一张「长按识别名片」却什么都没有的废图。
    qrError: false,
    qrMsg: '',
    // canUpload：有员工登录态才允许上传自定义背景（/api/employee/upload 要鉴权）。
    // 访客扫海报进来也能调版式（本地生效），但传不了自己的图。
    canUpload: false,
    // canEdit：这张海报归不归我编辑（== 登录员工 && 页面 staffId 就是我自己）。
    // 与 canUpload 分家是必要的：海报页会被转发给任何人，登录员工也完全可能
    // 打开**别人**的海报页——那时他不该拿到编辑入口。
    // 只读态下隐藏拖动层 / 工具条 / 上传，只留「保存」「转发」两个传播动作：
    // 海报是发给外人看的营销物料，被收件人改写再转发出去，品牌就不可控了。
    canEdit: false,
    // 只读态的一行说明：告诉访客这是谁的名片、他能做什么（见 wxml）
    visitorTip: '',
    uploading: false,
    // 二维码的透明热区（canvas 坐标系 = CSS px，画布固定 320×460）。
    // 独立成一个热区而不是靠算触摸点：算坐标要拿 canvas 的 boundingClientRect，
    // 而拖动是同步的、拿不到异步查询结果，第一下必然错位。热区由几何直接给出，
    // 天然准，且手指按下时就已经锁定在码上，拖的过程中热区不用跟着动。
    qrRect: { left: 0, top: 0, size: 0 },
  },
  onLoad(query: any) {
    let staffId = parseInt(query.staffId || '0', 10)
    if (!staffId && query.scene) {
      const params = decodeURIComponent(query.scene)
      const m = /staffId=(\d+)/.exec(params)
      if (m) staffId = parseInt(m[1], 10)
    }
    this.setData({ staffId, canUpload: !!wx.getStorageSync('empToken') })
    this.prepare()
  },
  async prepare() {
    const sid = this.data.staffId
    // 重试时必须清掉上一张的 tempFilePath：否则导出失败后按钮还拿着旧图，
    // 用户点「保存/分享」会拿到上一版海报（数据可能已变），且不报错。
    this.setData({ loading: true, error: false, tempFilePath: '' })
    try {
      const [card, code, posters, prefs] = await Promise.all([
        request('GET', '/api/card?staffId=' + sid),
        // from=poster：后端把该标记埋进小程序码 scene（staffId=12&from=poster），
        // 访客扫海报上的码打开名片时，scene 含 from=poster → 来源归为 poster，
        // 与「扫普通码」区分开（两者 scene 都是 scan 区间，只能靠这个标记区分）。
        request('GET', '/api/wechat/wxacode?staffId=' + sid + '&from=poster'),
        // 海报背景清单拉不到（老后端 / 断流）不该拖垮出图：退化成白底即可，
        // 所以单独立 catch，不能让它把 Promise.all 带崩——海报本身才是主功能。
        request('GET', '/api/assets/posters').catch(() => null),
        // 个性化设置同理：拉不到就用本地缓存，绝不影响出图。
        //
        // 本人走员工接口（读写 + 完整背景清单）；访客走公开快照
        // （GET /api/poster-prefs?staffId=，只回渲染这一张海报所需的最小集）。
        // 没有这一支，访客看到的就是「员工名片数据 + 客户端默认版式」重新生成的
        // 一张——员工调的排版、挑的背景、写的 slogan 全都传不出去。
        this.data.canUpload
          ? request('GET', '/api/employee/poster-prefs').catch(() => null)
          : request('GET', '/api/poster-prefs?staffId=' + sid).catch(() => null),
      ])
      // /api/wechat/wxacode 成功返回 {code:0,data:{url}}（data.url 正确）；
      // /api/card 的 employee/company/profile 在响应根级（非 data 包裹），故此处直接存整个响应。
      //
      // 小程序码取不到是**必须显式处理**的一种失败：它是整张海报唯一的入口
      // （扫码看名片），悄悄画一张没有码的图发出去，等于发了一张废纸。
      // 常见原因是后端没配 AppID/AppSecret（接口直接回 code:1 + msg）。
      const codeResp = code as any
      const qrUrl = assetUrl(codeResp && codeResp.code === 0 && codeResp.data ? codeResp.data.url : '')
      const qrMsg = qrUrl ? '' : (codeResp && codeResp.msg) || '小程序码生成失败'
      // 头像为后端相对路径，canvas 的 img.src 需绝对 URL 才能加载
      const cardAny = card as any
      if (cardAny.employee) cardAny.employee.avatar = assetUrl(cardAny.employee.avatar)
      this.setData({ card, qrUrl, qrError: !qrUrl, qrMsg, ...this.buildState(posters, prefs, cardAny) })
      // 导航栏标题按**员工所属企业**设置（海报在这里是替企业做的营销物料）。
      // 全局标题只有产品名，不覆盖就永远是「微传名片」——多租户下等于没品牌。
      // 放在 setData 之后：企业信息随 card 一起回来，此时才拿得到简称。
      applyBrandTitle(cardAny.company)
      this.draw()
    } catch (e) {
      this.setData({ loading: false, error: true, errorText: '名片信息加载失败，请检查网络' })
    }
  },
  // 单独重取小程序码：码是唯一会因为「后端配置」而失败的部分，
  // 不该逼用户为了它把整页（背景 + 版式）重新加载一遍。
  retryQr() {
    const that = this
    request('GET', '/api/wechat/wxacode?staffId=' + this.data.staffId + '&from=poster')
      .then((res: any) => {
        const url = assetUrl(res && res.code === 0 && res.data ? res.data.url : '')
        if (!url) {
          that.setData({ qrError: true, qrMsg: (res && res.msg) || '小程序码生成失败' })
          wx.showToast({ title: (res && res.msg) || '小程序码生成失败', icon: 'none' })
          return
        }
        // 换 URL 必须让缓存失效：同一 staffId 的码对象名固定，重生成后 URL 可能不变
        // 但内容已更新（TTL 覆盖写），缓存里那张旧图会被继续用。
        that._imgs = {}
        that._imgFails = {}
        that.setData({ qrUrl: url, qrError: false, qrMsg: '', tempFilePath: '' })
        that.draw()
      })
      .catch(() => that.setData({ qrError: true, qrMsg: '网络异常，小程序码获取失败' }))
  },
  // 把「内置素材 + 后端设置 + 本地缓存」合成页面状态。
  // 优先级：后端有记录就用后端的（跨设备一致）；后端说没记录（exists=false）
  // 或拉取失败，则用本机上次的选择——否则换新账号第一进页面会把手挑的背景冲掉。
  //
  // 节气自动选（`solarTermOfDate`）：当天正好是节气日时，把该节气的内置素材
  // 排到背景条第一位，并在用户没有自己的选择时直接选中它。非节气日与改造前
  // 完全一致（默认商务款），不会出现「十月还在发立春图」。
  //
  // 节日优先（`festivalOfDate`）：**先判节日，再判节气**。同一天既是节日又是
  // 节气时（2020-2040 有 14 处，如 2026-03-20 龙抬头撞春分、2038-02-04 春节
  // 撞立春）用节日那张——正月初四用户想看到「春节」而不是「立春」。
  // 判节日是纯查表 + 几次判等，比节气还便宜，放前面不心疼。
  buildState(postersResp: any, prefsResp: any, cardResp?: any) {
    const raw: any[] = postersResp && postersResp.code === 0 && postersResp.data && postersResp.data.items
      ? postersResp.data.items
      : []
    const festival: Festival | null = festivalOfDate(new Date())
    const term: SolarTerm | null = solarTermOfDate(new Date())
    const posters = festivalFirst(
      termFirst(
        raw
          .filter((p) => p && p.id)
          .map((p) => ({
            id: p.id,
            name: p.name || '',
            thumbUrl: assetUrl(p.thumb || p.file),
            fileUrl: assetUrl(p.file),
            custom: false,
          })),
        term
      ),
      festival
    )
    // 当天该自动选的那张：节日优先，见 pickToday
    const today = pickToday(festival, term, posters)

    const pd = prefsResp && prefsResp.code === 0 && prefsResp.data ? prefsResp.data : null
    const customs = (pd && pd.backgrounds ? pd.backgrounds : [])
      .filter((b: any) => b && b.id && b.file)
      .map((b: any) => ({
        id: b.id,
        name: b.name || '我的背景',
        thumbUrl: assetUrl(b.file),
        fileUrl: assetUrl(b.file),
        // raw 保留后端原始值（相对路径或绝对 CDN URL）：回存时原样提交，
        // 不能拿 fileUrl 反推——CDN 域名和 API 域名可能不同，反推会拼错。
        raw: b.file,
        custom: true,
      }))

    // canEdit：这张海报归不归我编辑。判据是「我的 staffId == 页面 staffId」，
    // 而不是「有没有登录」——登录员工完全可能打开**别人**转发来的海报页，
    // 那时把我的版式套在他的名片上既怪异又危险（改动会存进我自己的设置里）。
    //
    // 我的 staffId 有两个来源，缺一不可少：
    //  1. 员工接口回的 staffId（2026-10-28 起后端带）；
    //  2. 登录时写进 globalData.myStaffId 的那份（「我的」页会写，海报页的主入口
    //     正是「我的 → 生成海报」，进去时必然已就绪）。
    // 只认 1 会让**老后端 + 新小程序**的员工自己也变成只读（他自己排的版式
    // 突然改不动了）；只认 2 则覆盖不到「换设备后没开过『我的』页」的路径。
    // 两个都没有时才降级只读——宁可少给编辑权，也不能把别人的名片开放给他改。
    const myStaff = Number(pd && pd.staffId) || myStaffId()
    const owner = !!this.data.canUpload && myStaff > 0 && myStaff === this.data.staffId

    // 版式来源：服务端有记录就用它；没有时**只有本人**才回落到本机缓存——
    // 本机那份是员工自己手机上的偏好，跟访客无关。访客一律从默认重新算，
    // 这样「别人看到的」永远等于「员工排的」（公开快照 exists=false 时
    // 说明员工根本没调过，此时默认版式本来就是他要的效果）。
    const layout = normalizeLayout(pd && pd.exists ? pd.layout : owner ? this.readLayout() : null)
    const savedBg = pd && pd.exists ? pd.bgId || BG_NONE : owner ? this.readBg() : null
    // 「存的正好是默认商务款」也算没自己选过：见 resolveBgId 里同一处的说明。
    // 不这么判的话，第一次在节日日调了下版式就会把偏好固化成默认款，
    // 之后每个节日日都不再自动换了——而当天图的意义就在于「当天」。
    const noChoice = savedBg === null || savedBg === DEFAULT_BG
    // 当天图取节日那张，节日没有（或素材不在清单里）时才落到节气
    const preferId = today ? today.id : undefined
    const bgId = noChoice
      ? resolveBgId(null, posters, customs, preferId)
      : resolveBgId(savedBg, posters, customs)

    return {
      posters,
      customs,
      layout,
      bgId,
      canEdit: owner,
      // 只读态的一行说明：访客进来看见的是一整块海报加两个按钮，
      // 不写清楚「这是谁的、我能做什么」，他会以为这是个打不开的页面。
      visitorTip: owner || !cardResp ? '' : visitorTipOf(cardResp),
      // 当天命中的素材 id（非节日/非节气日、表外年份为空串）。wxml 用它给那张
      // 缩略图打一个名字角标：自动选中只靠蓝色边框看不出来是因为当天，
      // 用户会以为是上次的选择。
      todayId: today ? today.id : '',
      // 角标文案（节日名优先，与 todayId 同源）
      todayName: today ? today.name : '',
      todayKind: today ? today.kind : '',
      // 只有「没选过 + 当天命中 + 当天图真在清单里」才算自动挑的
      bgAuto: !!(noChoice && today && bgId === today.id),
    }
  },
  readBg(): string | null {
    try {
      // null = 从没选过（与「明确选了原样」区分开）：
      // wx.getStorageSync 对「键不存在」和「存的是空串」都返回 ''，
      // 所以「原样」落库时写哨兵值 'none'，空串只代表没存过。
      const raw = wx.getStorageSync(BG_KEY + ':' + this.data.staffId)
      if (!raw) return null
      return raw === BG_NONE_STORE ? BG_NONE : String(raw)
    } catch (e) {
      return null
    }
  },
  saveBg(id: string) {
    try {
      wx.setStorageSync(BG_KEY + ':' + this.data.staffId, id || BG_NONE_STORE)
    } catch (e) {
      // 存不下（存储满 / 隐私模式）不影响本次出图，下次进来退回默认即可
    }
  },
  readLayout(): Layout {
    try {
      const raw = wx.getStorageSync(LAYOUT_KEY + ':' + this.data.staffId)
      return normalizeLayout(raw ? JSON.parse(raw) : null)
    } catch (e) {
      // 存储里是脏值（老版本 / 手工改过）：回落默认，不让页面白屏
      return { ...DEFAULT_LAYOUT }
    }
  },
  saveLayout(layout: Layout) {
    try {
      wx.setStorageSync(LAYOUT_KEY + ':' + this.data.staffId, JSON.stringify(layout))
    } catch (e) {
      // 同上：存不下不影响本次出图
    }
  },
  // 当前选中的背景对象（内置或自定义），没选/找不到返回 null（= 白底）
  currentBg(): any {
    const id = this.data.bgId
    if (!id) return null
    return (
      this.data.customs.find((p: any) => p.id === id) ||
      this.data.posters.find((p: any) => p.id === id) ||
      null
    )
  },

  // ---- 背景 ----

  // 背景缩略图加载失败：记下失败的地址，WXML 据此换成同尺寸灰块（见 utils/thumb.ts）。
  // 这里不做「缩略图 → 原图」回退：自定义背景的 thumbUrl 与 fileUrl 是同一个地址，
  // 内置素材的 thumb 由素材清单直出（清单里没有的图根本不会列出来），
  // 真挂了再退一次原图也是同一份坏文件，不如直接给干净占位。
  onImgFail,

  // 切换底部工具：白名单外的值直接忽略（防止模板里写错 key 导致面板空白）
  switchTool(e: any) {
    const tool = e.currentTarget.dataset.tool
    if (!TOOLS.some((t) => t.key === tool)) return
    this.setData({ tool })
  },
  // 切换海报背景：记住选择并整张重画（画布内容全变，必须重走导出流程，
  // 否则保存/分享按钮拿到的还是上一版背景的图）。
  pickBg(e: any) {
    const id = (e && e.currentTarget && e.currentTarget.dataset && e.currentTarget.dataset.id) || BG_NONE
    if (id === this.data.bgId) return
    this.setData({ bgId: id, bgAuto: false, loading: true, error: false, tempFilePath: '' })
    this.saveBg(id)
    this.syncPrefs()
    this.draw()
  },
  // 上传自定义背景：选图 → 传对象存储 → 加进「我的背景」并选中。
  // 复用 /api/employee/upload（员工鉴权，与员工改头像同一条链路、同一存储层）。
  //
  // chooseMedia 是微信隐私接口：调用前须取得用户对隐私政策的同意，
  // 未同意则不打开选图器（用户不同意就不能采集照片，直接放弃本次操作）。
  async uploadBg() {
    // 判据用 canEdit 而不是 canUpload：登录员工打开**别人**的海报页时也有令牌，
    // 但那张海报不归他改，更不该让他把自己的背景图传到别人的素材库里。
    if (!this.data.canEdit) {
      wx.showModal({
        title: '提示',
        content: '登录员工账号后可以上传自己的背景图',
        showCancel: false,
      })
      return
    }
    if (this.data.uploading) return
    if (!(await ensurePrivacy(this))) {
      wx.showToast({ title: '需同意隐私政策才能使用', icon: 'none' })
      return
    }
    const that = this
    wx.chooseMedia({
      count: 1,
      mediaType: ['image'],
      success(res: any) {
        const filePath = res && res.tempFiles && res.tempFiles[0] && res.tempFiles[0].tempFilePath
        if (!filePath) return
        that.setData({ uploading: true })
        wx.showLoading({ title: '上传中', mask: true })
        wx.uploadFile({
          url: apiBaseUrl() + '/api/employee/upload',
          filePath,
          name: 'file',
          header: { Authorization: 'Bearer ' + (wx.getStorageSync('empToken') || '') },
          success(r: any) {
            wx.hideLoading()
            that.setData({ uploading: false })
            let body: any = {}
            try {
              body = JSON.parse(r.data)
            } catch {
              // 网关 502 / 反代返回 HTML 时响应体不是 JSON：先兜住再提示，
              // 否则 JSON.parse 抛错冒泡出回调，用户只看到卡住的「上传中」。
              wx.showToast({ title: '上传失败', icon: 'none' })
              return
            }
            const urls = body && body.data && body.data.urls
            if (body.code === 0 && urls && urls.length) {
              that.addCustomBg(urls[0])
            } else {
              wx.showToast({ title: body.msg || '上传失败', icon: 'none' })
            }
          },
          fail() {
            wx.hideLoading()
            that.setData({ uploading: false })
            wx.showToast({ title: '上传失败', icon: 'none' })
          },
        })
      },
    })
  },
  // 把上传结果加进自定义背景并选中。
  // id 由客户端生成（后端只校验格式）：c- + 时间戳36进制 + 4位随机，
  // 够短、可读、碰撞概率可忽略（同一员工最多 9 张）。
  addCustomBg(file: string) {
    const id = 'c-' + Date.now().toString(36) + Math.random().toString(36).slice(2, 6)
    const bg = {
      id,
      name: '我的背景',
      thumbUrl: assetUrl(file),
      fileUrl: assetUrl(file),
      raw: file, // 后端返回的原始路径，回存时原样提交
      custom: true,
    }
    const customs = this.data.customs.concat([bg])
    // 加完留在背景工具里：用户下一步多半是再挑一张或直接保存，
    // 跳走会让他以为自己点错了
    this.setData({ customs, bgId: id, bgAuto: false, tool: 'bg', loading: true, error: false, tempFilePath: '' })
    this.saveBg(id)
    this.syncPrefs()
    this.draw()
    wx.showToast({ title: '已添加背景' })
  },
  // 长按删除自定义背景（内置素材不可删——它们是编译进二进制的，删不掉）
  removeBg(e: any) {
    const id = e && e.currentTarget && e.currentTarget.dataset && e.currentTarget.dataset.id
    if (!id) return
    const that = this
    wx.showModal({
      title: '删除背景',
      content: '只从你的背景列表移除，已保存的海报不受影响',
      success(res: any) {
        if (!res.confirm) return
        const customs = that.data.customs.filter((p: any) => p.id !== id)
        // 删掉的正是当前选中项：回落到默认款，否则画布会去加载一张已不在列表里的图。
        // 传 null 而不是 ''：'' 的语义是「用户明确选了原样」，这里要的是「没得选 → 默认」。
        const patch: any = { customs }
        if (that.data.bgId === id) {
          patch.bgId = resolveBgId(null, that.data.posters, customs)
          patch.bgAuto = false
          patch.loading = true
          patch.error = false
          patch.tempFilePath = ''
          that.saveBg(patch.bgId)
        }
        that.setData(patch)
        that.syncPrefs()
        if (patch.bgId !== undefined) that.draw()
      },
    })
  },

  // ---- 版式 ----

  pickTemplate(e: any) {
    this.applyLayout({ template: e.currentTarget.dataset.key })
  },
  toggleOption(e: any) {
    const field = e.currentTarget.dataset.field
    if (!field) return
    this.applyLayout({ [field]: !(this.data.layout as any)[field] } as any)
  },
  pickTextColor(e: any) {
    this.applyLayout({ textColor: e.currentTarget.dataset.color })
  },
  pickAvatarSize(e: any) {
    const size = parseInt(e.currentTarget.dataset.size, 10)
    if (!size) return
    this.applyLayout({ avatarSize: size })
  },
  // 二维码尺寸：0 = 自动。传 0 是合法值（不是「没选」），所以不能像头像那样
  // 用 `if (!size) return` 挡掉。
  pickQrSize(e: any) {
    const size = parseInt(e.currentTarget.dataset.size, 10)
    this.applyLayout({ qrSize: isNaN(size) ? 0 : size })
  },

  // ---- 自定义文字 ----
  //
  // 输入过程与提交分开处理：
  // - 每敲一个字都只 render()（画布立刻跟着变，不导出）。导出要编码整张 PNG，
  //   一个字一次会明显卡顿，而用户此时根本不看图，只想看文字对不对。
  // - 失焦 / 回车才 applyLayout()：归一 + 落存储 + 同步后端 + 重新导出。
  //   点「保存到相册」会先触发 blur，所以不会出现「存下来的还是旧文案」。
  onCustomText(e: any) {
    const raw = (e && e.detail && e.detail.value) || ''
    // 直接改 data 不 setData：input 自己维护着输入值，回写反而会让光标跳位
    this.data.layout = { ...this.data.layout, customText: raw }
    this.render()
  },
  commitCustomText() {
    this.applyLayout({ customText: this.data.layout.customText })
  },
  clearCustomText() {
    this.applyLayout({ customText: '' })
  },

  // 按钮微调：每次 10px，按「上一次渲染算出的可拖区间」夹紧。
  // 不在 normalizeLayout 里夹：那里的上限是个防脏数据的粗框（±480），
  // 用粗框夹等于没夹——连点几下数字还在涨，海报却早就不动了。
  nudge(dx: number, dy: number) {
    const g = this._geo
    const L = this.data.layout
    const xr = g ? [g.range.contentXMin, g.range.contentXMax] : [-OFFSET_LIMIT, OFFSET_LIMIT]
    const yr = g ? [g.range.contentYMin, g.range.contentYMax] : [-OFFSET_LIMIT, OFFSET_LIMIT]
    this.applyLayout({
      offsetX: clampNum((L.offsetX || 0) + dx, xr[0], xr[1]),
      offsetY: clampNum((L.offsetY || 0) + dy, yr[0], yr[1]),
    })
  },
  offsetUp() {
    this.nudge(0, -OFFSET_STEP)
  },
  offsetDown() {
    this.nudge(0, OFFSET_STEP)
  },
  // 码的按钮微调：和内容区同一套步长与夹紧思路，但走自己那对偏移和区间。
  // 拖动仍是最顺手的，按钮是给「差一点点」和「手边正好有码热区挡着」的场景。
  nudgeQr(dx: number, dy: number) {
    const g = this._geo
    const L = this.data.layout
    const xr = g ? [g.range.qrXMin, g.range.qrXMax] : [-OFFSET_LIMIT, OFFSET_LIMIT]
    const yr = g ? [g.range.qrYMin, g.range.qrYMax] : [-OFFSET_LIMIT, OFFSET_LIMIT]
    this.applyLayout({
      qrOffsetX: clampNum((L.qrOffsetX || 0) + dx, xr[0], xr[1]),
      qrOffsetY: clampNum((L.qrOffsetY || 0) + dy, yr[0], yr[1]),
    })
  },
  qrUp() {
    this.nudgeQr(0, -OFFSET_STEP)
  },
  qrDown() {
    this.nudgeQr(0, OFFSET_STEP)
  },
  // 复位拆成两个：拖歪了想回原位，指的从来只是「我刚弄乱的那一块」。
  // 一起归零会顺手把另一块也挪回去——用户会以为自己的调整没保存上。
  resetContent() {
    this.applyLayout({ offsetX: 0, offsetY: 0 })
  },
  resetQr() {
    this.applyLayout({ qrOffsetX: 0, qrOffsetY: 0 })
  },

  // ---- 拖动调整位置 ----
  //
  // 为什么拖比点按钮好：位置是个「看着调」的活儿，用户看到海报偏上了，第一反应
  // 是按住往下拽，而不是去数「点了三次下移」。按钮保留下来做精调（每次 10px）。
  //
  // 拖谁由「按在哪儿」决定：按在二维码热区（.qr-hit）上是拖码，按在其余任何地方
  // 是拖整块信息。两个对象各有各的偏移，互不牵连——以前二维码钉死在底部居中，
  // 用户想把它挪开一点只能整张海报一起动，结果文字也跟着歪。
  //
  // 实现上有三个坑，都在下面处理了：
  // 1) 每帧都 setData + 导出图片会卡：setData 走视图层通信，canvasToTempFilePath
  //    要编码整张 PNG。所以拖动中直接改 this.data（不通知视图层）并只 render()，
  //    松手才 setData + 导出一次。
  // 2) 每帧重新加载图片会闪：背景/头像/二维码都走 loadCached，首次加载后常驻内存。
  // 3) 每帧重新量文字算包围盒会白费力气：可拖区间只跟文字宽度有关、与当前位移
  //    无关，所以按「上一次渲染算出的 range」夹紧即可。
  onDragStart(e: any) {
    if (this.data.loading || this.data.error) return
    const t = e.touches && e.touches[0]
    if (!t) return
    const ds = (e.currentTarget && e.currentTarget.dataset) || {}
    const target = ds.target === 'qr' ? 'qr' : 'content'
    const geo = this._geo
    this._drag = {
      target,
      x: t.clientX,
      y: t.clientY,
      lastX: t.clientX,
      lastY: t.clientY,
      moved: false,
      // 可拖区间：没有几何（还没画过）时退回画布尺寸级别的保守区间
      bx: geo
        ? target === 'qr'
          ? [geo.range.qrXMin, geo.range.qrXMax]
          : [geo.range.contentXMin, geo.range.contentXMax]
        : [-OFFSET_LIMIT, OFFSET_LIMIT],
      by: geo
        ? target === 'qr'
          ? [geo.range.qrYMin, geo.range.qrYMax]
          : [geo.range.contentYMin, geo.range.contentYMax]
        : [-OFFSET_LIMIT, OFFSET_LIMIT],
    }
  },
  onDragMove(e: any) {
    const d = this._drag
    const t = e.touches && e.touches[0]
    if (!d || !t) return
    // 2px 死区：手指按下时几乎必然会带 1px 抖动，不设死区会让「点一下海报」
    // 也触发重画，白掉一帧
    if (!d.moved && Math.abs(t.clientX - d.x) < 2 && Math.abs(t.clientY - d.y) < 2) return
    d.moved = true
    // 用「增量 + 每帧夹紧」而不是「起点 + 总位移」：后者拖到边界再往回拉时，
    // 前半个行程都在填补越界的差值，手指动了画面不动，像卡住一样。
    const dx = t.clientX - d.lastX
    const dy = t.clientY - d.lastY
    d.lastX = t.clientX
    d.lastY = t.clientY
    const L = this.data.layout
    // 直接改 data 不 setData：视图层不需要知道中间过程，画布本身就是反馈
    this.data.layout =
      d.target === 'qr'
        ? {
            ...L,
            qrOffsetX: clampNum(L.qrOffsetX + dx, d.bx[0], d.bx[1]),
            qrOffsetY: clampNum(L.qrOffsetY + dy, d.by[0], d.by[1]),
          }
        : {
            ...L,
            offsetX: clampNum(L.offsetX + dx, d.bx[0], d.bx[1]),
            offsetY: clampNum(L.offsetY + dy, d.by[0], d.by[1]),
          }
    this.render()
  },
  onDragEnd() {
    const d = this._drag
    this._drag = null
    if (!d || !d.moved) return
    // 松手才落视图层 / 存储 / 后端，并导出最终图
    const layout = normalizeLayout(this.data.layout)
    this.setData({ layout })
    this.saveLayout(layout)
    this.syncPrefs()
    // render 里会把热区挪到码的新位置（拖动中故意没挪）
    this.render(() => this.exportImage())
  },
  // 版式改动统一入口：归一 → 落地 → 重画 → 同步后端。
  // 必须重画并清掉 tempFilePath：版式变了但图没变的话，用户点「分享」发出去的
  // 还是旧版式那张，而且没有任何报错。
  // 不置 loading：图片都已缓存，重画 + 导出现在只要几十毫秒，盖一层「生成中」
  // 遮罩反而会在每次点选项时白闪一下。
  applyLayout(patch: any) {
    const layout = normalizeLayout({ ...this.data.layout, ...patch })
    this.setData({ layout, error: false, tempFilePath: '' })
    this.saveLayout(layout)
    this.syncPrefs()
    this.draw()
  },
  // 防抖同步：连点「上移」5 次不该发 5 个 PUT。
  syncPrefs() {
    if (!this.data.canEdit) return // 只读态（含「登录但打开别人的海报」）：一次都不写
    const that = this
    if ((this as any)._saveTimer) clearTimeout((this as any)._saveTimer)
    ;(this as any)._saveTimer = setTimeout(() => that.savePrefsNow(), 600)
  },
  // 立即保存（测试与页面 onHide 直接调，不等防抖）
  savePrefsNow() {
    if (!this.data.canEdit) return Promise.resolve(null)
    const that = this
    const body = {
      // 自动挑的节气图不落库：写默认款（= 「没有自己的选择」）。
      // 写节气 id 会把偏好固化成那一张，一个月后进页面还在发上个月的节气图。
      bgId: this.data.bgAuto ? DEFAULT_BG : this.data.bgId,
      backgrounds: this.data.customs.map((b: any) => ({ id: b.id, name: b.name, file: b.raw || b.fileUrl })),
      layout: this.data.layout,
    }
    return request('PUT', '/api/employee/poster-prefs', body)
      .then((r: any) => {
        // 后端会归一（超限截断 / 选中项被删）：以返回值为准纠正本地，
        // 否则会出现「本地显示有、后端其实没有」的错位，下次进来就少一张。
        if (r && r.code === 0 && r.data) {
          const customs = (r.data.backgrounds || []).map((b: any) => ({
            id: b.id,
            name: b.name || '我的背景',
            thumbUrl: assetUrl(b.file),
            fileUrl: assetUrl(b.file),
            raw: b.file,
            custom: true,
          }))
          that.setData({ customs })
        }
        return r
      })
      .catch(() => null) // 保存失败不影响出图，本地缓存已经生效
  },
  onHide() {
    // 离开页面时把防抖中的改动落库：用户调完版式立刻返回，600ms 的定时器
    // 可能还没触发，不 flush 就白调了。
    if ((this as any)._saveTimer) {
      clearTimeout((this as any)._saveTimer)
      ;(this as any)._saveTimer = null
      this.savePrefsNow()
    }
  },
  retry() {
    this.prepare()
  },

  // ---- 版式计算（纯函数，单测可直接断言坐标）----

  // 算出每个元素的位置。三件事必须一起决定，所以合在一个函数里：
  // 1) 内容从哪开始（内置素材顶部有烧好的标题，要下移 150）
  // 2) 文字用深色还是浅色（浅底配深字，深底配浅字）
  // 3) 二维码能有多大（内容越高，留给码的空间越小，否则会叠在一起）
  //
  // 位移是「自由」的：内容块和二维码各有自己的 offset，可以拖到画布内任意位置。
  // 边界不用固定值，而是按**内容实际包围盒**夹紧——只有量出文字真实宽度，
  // 才能既让用户拖到贴边，又不会把字拖出画布被裁掉。
  // measure 由调用方（render）传入 ctx 量宽；不传时按字号粗估（单测用）。
  computeGeometry(
    W: number,
    H: number,
    hasBg: boolean,
    measure?: (text: string, size: number, weight?: string) => number
  ): Geo {
    const L = this.data.layout
    const bg = this.currentBg()
    // 只有内置素材顶部烧了标题；自定义背景是用户自己的图，不能假设有留白
    const top = bg && !bg.custom ? TITLE_ZONE : PLAIN_TOP
    const light = L.textColor === 'light' || (L.textColor === 'auto' && hasBg)
    const s = L.avatarSize
    const cx = W / 2

    const emp = (this.data.card && this.data.card.employee) || {}
    const comp = (this.data.card && this.data.card.company) || {}
    const pad = 20 // 左右安全边距
    const innerMaxW = W - pad * 2

    const items: TextItem[] = []
    let avatar: Geo['avatar'] = null
    let card: Geo['card'] = null
    let rule: Geo['rule'] = null
    // 未位移的内容底：用来定二维码的默认大小。用未位移值是为了让「拖动内容」
    // 不会连带把二维码改大改小（那样手感很怪）
    let baseBottom = top

    if (L.template === 'center') {
      let y = top
      if (L.showAvatar) {
        avatar = { x: cx - s / 2, y, size: s }
        y += s + 24
      }
      if (L.showName) {
        items.push({ text: emp.name || '', x: cx, y: y + 20, size: 20, weight: 'bold', align: 'center', maxWidth: innerMaxW })
        y += 26
      }
      baseBottom = y
      if (L.showTitle) {
        // 姓名关掉时职位顶上来当标题：否则头像下面只剩一条 13px 的灰色小字，
        // 看着像「名字没加载出来」
        items.push({
          text: emp.title || '', x: cx, y: y + (L.showName ? 16 : 20),
          size: L.showName ? 13 : 17, align: 'center', maxWidth: innerMaxW,
          dim: L.showName,
        })
        y += L.showName ? 22 : 26
        baseBottom = y
      }
      if (L.showCompany) {
        items.push({ text: comp.fullName || '', x: cx, y: y + 18, size: 14, align: 'center', maxWidth: innerMaxW })
        y += 22
        baseBottom = y
      }
      if (L.showAddress) {
        items.push({ text: comp.address || '', x: cx, y: y + 16, size: 12, align: 'center', maxWidth: innerMaxW, dim: true })
        y += 20
        baseBottom = y
      }
      if (L.showPhone && emp.phone) {
        items.push({ text: '手机 ' + emp.phone, x: cx, y: y + 16, size: 13, align: 'center', maxWidth: innerMaxW })
        y += 20
        baseBottom = y
      }
      if (L.showLandline && emp.landline) {
        items.push({ text: '座机 ' + emp.landline, x: cx, y: y + 16, size: 13, align: 'center', maxWidth: innerMaxW })
        y += 20
        baseBottom = y
      }
      if (L.showEmail && emp.email) {
        items.push({ text: emp.email, x: cx, y: y + 15, size: 12, align: 'center', maxWidth: innerMaxW, dim: true })
        y += 19
        baseBottom = y
      }
      if (L.customText) {
        items.push({ text: L.customText, x: cx, y: y + 18, size: 13, align: 'center', maxWidth: innerMaxW, accent: true })
        y += 20
        baseBottom = y
      }
    } else if (L.template === 'minimal') {
      // 极简：只留姓名 + 职位，字号放大，其余全砍——适合「就想发张干净的名片」
      let y = top + 20
      if (L.showName) {
        items.push({ text: emp.name || '', x: cx, y, size: 24, weight: 'bold', align: 'center', maxWidth: innerMaxW })
        if (L.showTitle) {
          items.push({ text: emp.title || '', x: cx, y: y + 26, size: 13, align: 'center', maxWidth: innerMaxW, dim: true })
          y += 26
        }
      } else if (L.showTitle) {
        // 姓名关掉：职位自己当主角，字号跟着放大，不然整张海报只剩一片空
        items.push({ text: emp.title || '', x: cx, y, size: 18, align: 'center', maxWidth: innerMaxW })
      }
      baseBottom = y + 6
      if (L.customText) {
        items.push({ text: L.customText, x: cx, y: y + 34, size: 13, align: 'center', maxWidth: innerMaxW, accent: true })
        baseBottom = y + 34
      }
    } else if (L.template === 'card') {
      // 卡片：信息装进一块半透明圆角底，压住背景里花哨的部分
      const cp = 16 // 卡片内边距
      let y = top + cp
      if (L.showAvatar) {
        avatar = { x: cp * 2, y, size: s }
      }
      const tx = L.showAvatar ? cp * 2 + s + 12 : cp * 2
      if (L.showName) {
        items.push({ text: emp.name || '', x: tx, y: y + 24, size: 19, weight: 'bold', maxWidth: W - tx - cp })
      }
      if (L.showTitle) {
        items.push({
          text: emp.title || '', x: tx,
          y: L.showName ? y + 44 : y + 30,
          size: L.showName ? 12 : 15, maxWidth: W - tx - cp, dim: L.showName,
        })
      }
      y += (L.showAvatar ? s : 0) + (L.showAvatar ? 14 : 58)
      if (L.showCompany) {
        items.push({ text: comp.fullName || '', x: cp * 2, y: y + 12, size: 13, maxWidth: W - cp * 4 })
        y += 20
      }
      if (L.showAddress) {
        items.push({ text: comp.address || '', x: cp * 2, y: y + 12, size: 12, maxWidth: W - cp * 4, dim: true })
        y += 18
      }
      if (L.showPhone && emp.phone) {
        items.push({ text: '手机 ' + emp.phone, x: cp * 2, y: y + 12, size: 13, maxWidth: W - cp * 4 })
        y += 18
      }
      if (L.showLandline && emp.landline) {
        items.push({ text: '座机 ' + emp.landline, x: cp * 2, y: y + 12, size: 13, maxWidth: W - cp * 4 })
        y += 18
      }
      if (L.showEmail && emp.email) {
        items.push({ text: emp.email, x: cp * 2, y: y + 11, size: 12, maxWidth: W - cp * 4, dim: true })
        y += 17
      }
      if (L.customText) {
        items.push({ text: L.customText, x: cp * 2, y: y + 12, size: 12, maxWidth: W - cp * 4, accent: true })
        y += 18
      }
      baseBottom = y + cp
      card = { x: cp, y: top, w: W - cp * 2, h: baseBottom - top }
    } else if (L.template === 'split') {
      // 分栏：头像在左、所有文字右对齐——镜像的经典款。
      // 靠右的文字贴住右边距，与左边的头像形成对角平衡；同一份内容换个方向，
      // 观感就从「名片」变成「版式设计」。
      const right = W - pad
      if (L.showAvatar) avatar = { x: pad, y: top + 6, size: s }
      const maxW = W - pad * 2 - (L.showAvatar ? s + 16 : 0)
      let y = top + 30
      if (L.showName) {
        items.push({ text: emp.name || '', x: right, y, size: 21, weight: 'bold', align: 'right', maxWidth: maxW })
        y += 24
      }
      if (L.showTitle) {
        items.push({
          text: emp.title || '', x: right, y: L.showName ? y : top + 30,
          size: L.showName ? 13 : 17, align: 'right', maxWidth: maxW, dim: L.showName,
        })
        y += 22
      }
      baseBottom = Math.max(y, L.showAvatar ? top + 6 + s : 0)
      let infoY = L.showAvatar ? top + s + 34 : y + 10
      const row = (text: string, size: number, dim: boolean, accent?: boolean) => {
        items.push({ text, x: right, y: infoY + 14, size, align: 'right', maxWidth: W - pad * 2, dim, accent })
        infoY += size >= 14 ? 22 : 20
        baseBottom = Math.max(baseBottom, infoY)
      }
      if (L.showCompany) row(comp.fullName || '', 14, false)
      if (L.showAddress) row(comp.address || '', 12, true)
      if (L.showPhone && emp.phone) row('手机 ' + emp.phone, 13, false)
      if (L.showLandline && emp.landline) row('座机 ' + emp.landline, 13, false)
      if (L.showEmail && emp.email) row(emp.email, 12, true)
      if (L.customText) row(L.customText, 13, false, true)
    } else if (L.template === 'left') {
      // 大字：姓名放大到 27px 独占一行，下面压一条主色短线，信息紧凑左对齐。
      // 像一张「个人抬头」而不是名片——姓名本身就是画面主角。
      if (L.showAvatar) avatar = { x: W - pad - s, y: top, size: s }
      const maxW = W - pad * 2 - (L.showAvatar ? s + 16 : 0)
      let y = top + 34
      if (L.showName) {
        items.push({ text: emp.name || '', x: pad, y, size: 27, weight: 'bold', maxWidth: maxW })
        y += 8
        // 短线跟着姓名走：姓名关掉时不画，免得线上方空出一块
        rule = { x: pad, y, w: 36 }
        y += 20
      }
      if (L.showTitle) {
        items.push({ text: emp.title || '', x: pad, y, size: L.showName ? 13 : 18, maxWidth: maxW, dim: L.showName })
        y += 22
      }
      baseBottom = Math.max(y, L.showAvatar ? top + s : 0)
      let infoY = y + 8
      const row = (text: string, size: number, dim: boolean, accent?: boolean) => {
        items.push({ text, x: pad, y: infoY + 14, size, maxWidth: innerMaxW, dim, accent })
        infoY += 20
        baseBottom = Math.max(baseBottom, infoY)
      }
      if (L.showCompany) row(comp.fullName || '', 14, false)
      if (L.showAddress) row(comp.address || '', 12, true)
      if (L.showPhone && emp.phone) row('手机 ' + emp.phone, 13, false)
      if (L.showLandline && emp.landline) row('座机 ' + emp.landline, 13, false)
      if (L.showEmail && emp.email) row(emp.email, 12, true)
      if (L.customText) row(L.customText, 13, false, true)
    } else if (L.template === 'stack') {
      // 清单：每行「标签 + 值」两列对齐。信息密度最高的一套——
      // 九项全开时只有它还能看清「哪个是公司、哪个是座机」。
      let y = top + 10
      if (L.showAvatar) {
        avatar = { x: cx - s / 2, y, size: s }
        y += s + 22
      }
      if (L.showName) {
        items.push({ text: emp.name || '', x: cx, y: y + 20, size: 19, weight: 'bold', align: 'center', maxWidth: innerMaxW })
        y += 26
      }
      if (L.showTitle) {
        items.push({
          text: emp.title || '', x: cx, y: y + 14,
          size: L.showName ? 12 : 16, align: 'center', maxWidth: innerMaxW, dim: L.showName,
        })
        y += 20
      }
      baseBottom = y
      const labelW = 40
      const valX = pad + labelW
      const row = (label: string, val: string, size: number, dim: boolean) => {
        // 标签用小字：它回答「这一行是什么」，不是内容本身
        items.push({ text: label, x: pad, y: y + 14, size: 11, maxWidth: labelW, dim: true })
        items.push({ text: val, x: valX, y: y + 14, size, maxWidth: W - pad - valX, dim })
        y += 20
        baseBottom = Math.max(baseBottom, y)
      }
      if (L.showCompany && comp.fullName) row('公司', comp.fullName, 13, false)
      if (L.showAddress && comp.address) row('地址', comp.address, 12, true)
      if (L.showPhone && emp.phone) row('手机', emp.phone, 13, false)
      if (L.showLandline && emp.landline) row('座机', emp.landline, 13, false)
      if (L.showEmail && emp.email) row('邮箱', emp.email, 12, true)
      if (L.customText) row('寄语', L.customText, 13, false)
    } else if (L.template === 'poster') {
      // 画报：内容整体下沉到画布中下部，上方整块留给背景图主体——
      // 风景 / 人物类背景的上半部分往往最好看，压满文字就白瞎了。
      // 起点同时受二维码上沿约束，否则内容一多就把码顶没。
      let y = Math.min(Math.round(H * 0.42), QR_BOTTOM - 210)
      if (L.showAvatar) {
        avatar = { x: cx - s / 2, y, size: s }
        y += s + 20
      }
      if (L.showName) {
        items.push({ text: emp.name || '', x: cx, y: y + 22, size: 22, weight: 'bold', align: 'center', maxWidth: innerMaxW })
        y += 26
        rule = { x: cx - 18, y, w: 36 }
        y += 14
      }
      if (L.showTitle) {
        items.push({
          text: emp.title || '', x: cx, y: y + 12,
          size: L.showName ? 12 : 17, align: 'center', maxWidth: innerMaxW, dim: L.showName,
        })
        y += 20
      }
      baseBottom = y
      const row = (text: string, size: number, dim: boolean, accent?: boolean) => {
        items.push({ text, x: cx, y: y + 14, size, align: 'center', maxWidth: innerMaxW, dim, accent })
        y += 20
        baseBottom = Math.max(baseBottom, y)
      }
      if (L.showCompany) row(comp.fullName || '', 14, false)
      if (L.showAddress) row(comp.address || '', 12, true)
      if (L.showPhone && emp.phone) row('手机 ' + emp.phone, 13, false)
      if (L.showLandline && emp.landline) row('座机 ' + emp.landline, 13, false)
      if (L.showEmail && emp.email) row(emp.email, 12, true)
      if (L.customText) row(L.customText, 13, false, true)
    } else {
      // 经典：头像左上，右侧姓名职位，公司/地址/电话竖排在下方
      const left = pad
      if (L.showAvatar) avatar = { x: left, y: top, size: s }
      const tx = L.showAvatar ? left + s + 14 : left
      const nameY = L.showAvatar ? top + 28 : top + 22
      if (L.showName) {
        items.push({ text: emp.name || '', x: tx, y: nameY, size: 20, weight: 'bold', maxWidth: W - tx - 16 })
      }
      let y = nameY + 22
      if (L.showTitle) {
        items.push({
          text: emp.title || '', x: tx,
          y: L.showName ? y : nameY,
          size: L.showName ? 13 : 17, maxWidth: W - tx - 16, dim: L.showName,
        })
        y += 22
      }
      let infoY = L.showAvatar ? top + s + 30 : y + 6
      baseBottom = Math.max(y, L.showAvatar ? top + s : 0)
      if (L.showCompany) {
        items.push({ text: comp.fullName || '', x: left, y: infoY + 14, size: 14, maxWidth: innerMaxW })
        infoY += 22
        baseBottom = Math.max(baseBottom, infoY)
      }
      if (L.showAddress) {
        items.push({ text: comp.address || '', x: left, y: infoY + 12, size: 12, maxWidth: innerMaxW, dim: true })
        infoY += 20
        baseBottom = Math.max(baseBottom, infoY)
      }
      if (L.showPhone && emp.phone) {
        items.push({ text: '手机 ' + emp.phone, x: left, y: infoY + 14, size: 13, maxWidth: innerMaxW })
        infoY += 20
        baseBottom = Math.max(baseBottom, infoY)
      }
      // 座机与手机同字号（都是「打这个号码」），邮箱降一档并弱化：
      // 邮箱往往很长，13px 在 320 宽上很容易折行或顶到边
      if (L.showLandline && emp.landline) {
        items.push({ text: '座机 ' + emp.landline, x: left, y: infoY + 14, size: 13, maxWidth: innerMaxW })
        infoY += 20
        baseBottom = Math.max(baseBottom, infoY)
      }
      if (L.showEmail && emp.email) {
        items.push({ text: emp.email, x: left, y: infoY + 13, size: 12, maxWidth: innerMaxW, dim: true })
        infoY += 19
        baseBottom = Math.max(baseBottom, infoY)
      }
      if (L.customText) {
        items.push({ text: L.customText, x: left, y: infoY + 16, size: 13, maxWidth: innerMaxW, accent: true })
        infoY += 22
        baseBottom = Math.max(baseBottom, infoY)
      }
    }

    // 这里**不做**「内容超高就整体上移」：试过，是个陷阱——
    // 上移把 baseBottom 压到 `QR_BOTTOM - QR_MIN - QR_GAP`（358），
    // fitSize 就恒等于 56，于是 clamp(qrSize, 56, min(140, 56)) 对
    // 「小 / 中 / 大」一律返回 56，用户看到的就是「大小调整失效」。
    // 内容多时的正确取舍是：码按用户选的档位画，重叠处靠白底衬保证扫得出来
    // （见 render 里那块圆角白底）。用户要 140 就给 140，代价他自己看得见。

    // 1) 量内容包围盒（位移前）。用真实字形宽度，不用固定值——
    //    「能拖多远」取决于这段文字实际有多宽。
    const box = measureBox(items, avatar, card, measure)
    const cX = shiftRange(box.left, box.right, W, EDGE_MARGIN)
    const cY = shiftRange(box.top, box.bottom, H, EDGE_MARGIN)

    // 2) 夹紧位移：内容整体留在画布内，四周留 EDGE_MARGIN。
    const effX = clampNum(L.offsetX || 0, cX[0], cX[1])
    const effY = clampNum(L.offsetY || 0, cY[0], cY[1])
    if (effX || effY) {
      for (const it of items) {
        it.x += effX
        it.y += effY
      }
      if (avatar) {
        avatar.x += effX
        avatar.y += effY
      }
      if (card) {
        card.x += effX
        card.y += effY
      }
    }

    // 3) 二维码：尺寸默认按内容高度自适应（内容越高留给码的地方越少），
    //    也可以由用户指定档位——但指定值同样受「不与内容重叠」约束：
    //    大码压住电话号码比小码难扫得多，用户只会觉得海报坏了。
    //    位置默认底部居中，再叠加用户自己的位移，同样夹紧在画布内。
    //    纵向上下界对称：都只留 EDGE_MARGIN（提示文字另算，见 hint.show）。
    // 关掉小程序码：整块不画、不加载、不占位，码的位移区间一并归零。
    // 这是用户明确关的（做纯形象海报），所以也不该再弹「码没生成」的黄条。
    if (!L.showQr) {
      return {
        items, avatar, card, rule, light, hint: null,
        qr: { x: cx, y: QR_BOTTOM, size: 0 },
        offsetX: effX, offsetY: effY, qrOffsetX: 0, qrOffsetY: 0,
        range: {
          contentXMin: cX[0], contentXMax: cX[1],
          contentYMin: cY[0], contentYMax: cY[1],
          qrXMin: 0, qrXMax: 0, qrYMin: 0, qrYMax: 0,
        },
      }
    }

    const fitSize = QR_BOTTOM - QR_GAP - baseBottom
    const autoSize = Math.max(QR_MIN, Math.min(QR_MAX, fitSize))
    // 指定档位**严格生效**，不再被 fitSize 二次夹紧。
    // 之前夹了 `min(QR_MAX_EXPLICIT, fitSize)`，内容一多 fitSize 就掉到 56，
    // 「小 / 中 / 大」全被压成 56——用户点哪个都一样，看起来就是「调整失效」。
    // 内容放不下时码会盖住最后一两行，用白底衬保证码本身清晰可扫；
    // 想两全的用户会自己关掉几个元素、或把码选小一档。
    const qrSize = L.qrSize ? clampNum(L.qrSize, QR_MIN, QR_MAX_EXPLICIT) : autoSize
    const baseQx = (W - qrSize) / 2
    const baseQy = QR_BOTTOM - qrSize
    const qrXMin = EDGE_MARGIN - baseQx
    const qrXMax = W - EDGE_MARGIN - baseQx - qrSize
    const qrYMin = EDGE_MARGIN - baseQy
    // 下界**不**扣提示文字那 32px：默认位置（码底 = QR_BOTTOM）正好用满它，
    // 扣掉之后 qrYMax 恒为 0——「下移」按不动、码永远贴不到底边，而上方却能
    // 一路顶到画布顶（只受 EDGE_MARGIN 约束）。左右不对称得毫无道理，
    // 用户看到的就是「往上能溢出、往下卡死」。现在允许贴到画布底边，
    // 提示文字放不下时干脆不画（见 hint.show）。
    const qrYMax = H - EDGE_MARGIN - baseQy - qrSize
    const qx = baseQx + clampNum(L.qrOffsetX || 0, qrXMin, qrXMax)
    const qy = baseQy + clampNum(L.qrOffsetY || 0, qrYMin, qrYMax)
    const qr = { x: qx, y: qy, size: qrSize }

    // 提示跟着码走：码被拖到画布上半部分时，提示不能还钉在底部。
    // 但码拖到贴近底边时提示就没地方了——宁可不画，也不能夹到 H-6 压在码上
    // （压住一半的「长按识别名片」比没有更糟）。
    const hintY = qy + qrSize + HINT_GAP
    const hint = { x: qx + qrSize / 2, y: hintY, show: hintY <= H - 6 }

    return {
      items, avatar, qr, card, rule, light, hint,
      offsetX: effX, offsetY: effY,
      qrOffsetX: qx - baseQx, qrOffsetY: qy - baseQy,
      range: {
        contentXMin: cX[0], contentXMax: cX[1],
        contentYMin: cY[0], contentYMax: cY[1],
        qrXMin, qrXMax, qrYMin, qrYMax,
      },
    }
  },

  // ---- 绘制 ----

  // draw = 画一遍 + 导出图片。改背景 / 改版式走这条（用户要看到成品图）。
  draw() {
    this.render(() => this.exportImage())
  },

  // ensureCanvas 取一次 canvas 节点并缓存。
  // 拖动时每帧都要重画，每帧都 createSelectorQuery 太浪费；且 canvas.width 赋值
  // 会重置变换矩阵，重复 scale 会让画面越缩越小——缓存后只初始化一次。
  ensureCanvas(cb: (canvas: any, ctx: any) => void) {
    const that = this
    if (this._canvas && this._ctx) {
      cb(this._canvas, this._ctx)
      return
    }
    wx.createSelectorQuery()
      .select('#poster')
      .fields({ node: true, size: true })
      .exec((res: any) => {
        if (!res || !res[0] || !res[0].node) {
          // 取不到 canvas 节点：静默 return 会让页面永远停在「生成中」。
          that.setData({ loading: false, error: true, errorText: '海报生成失败' })
          return
        }
        const canvas: any = res[0].node
        const ctx = canvas.getContext('2d')
        const dpr = (wx.getSystemInfoSync() as any).pixelRatio || 2
        canvas.width = CANVAS_W * dpr
        canvas.height = CANVAS_H * dpr
        ctx.scale(dpr, dpr)
        that._canvas = canvas
        that._ctx = ctx
        cb(canvas, ctx)
      })
  },

  // render 只把画面画到 canvas 上，**不导出**。
  // 拖动时必须用这条：canvasToTempFilePath 要编码整张 PNG，几十毫秒起步，
  // 每帧都调会拖到掉帧；拖动过程只要画面跟着动就行，松手再导一次。
  render(done?: () => void) {
    const that = this
    const W = CANVAS_W
    const H = CANVAS_H
    this.ensureCanvas((canvas: any, ctx: any) => {
      const bg = that.currentBg()
      // 先铺白底：背景图加载失败（网络/域名未配）时至少还是一张白底海报
      ctx.fillStyle = '#ffffff'
      ctx.fillRect(0, 0, W, H)

      const paint = (hasBg: boolean) => {
        // 量宽函数交给几何计算：夹紧要按真实字形宽度算，粗估会让「贴边」
        // 变成「差两个字出画」。measureText 只对当前 ctx.font 有效，
        // 所以每次量之前都要重设 font。
        const geo = that.computeGeometry(W, H, hasBg, (text: string, size: number, weight?: string) => {
          ctx.font = `${weight || ''} ${size}px sans-serif`.trim()
          return that.textWidth(ctx, text)
        })
        that._geo = geo
        that.reconcileOffsets(geo)
        that.syncQrRect(geo)
        // 自定义背景是用户随手拍的图，底部可能是白墙 / 天空——白字压上去直接糊掉。
        // 内置素材的暗部是生成时烧好的，不需要再叠。
        if (bg && bg.custom && geo.light) {
          const first = geo.avatar ? geo.avatar.y : geo.items.length ? geo.items[0].y - 20 : H * 0.4
          drawScrim(ctx, W, H, Math.max(0, first - 40))
        }
        that.drawContent(ctx, W, H, geo)
        // 头像 → 小程序码 → 完成：串行挂在 onload 里，保证两张图都画完再导出。
        // 并行加载 + 立即导出会拿到一张「只有背景没有头像和码」的半成品。
        that.loadCached(canvas, empAvatar(that.data.card), (img: any) => {
          if (img && geo.avatar) drawAvatar(ctx, img, geo.avatar)
          // 码被关掉时 size 为 0：不加载、不占位、不提示，直接收工
          if (!geo.qr.size) {
            if (done) done()
            return
          }
          that.loadCached(canvas, that.data.qrUrl, (qimg: any) => {
            if (qimg) {
              // 白底衬**无条件**垫：深色底上不垫会糊成一片；码放大后还可能压住
              // 最后一两行文字，不垫就变成「字从码缝里透出来」，扫码成功率直接掉。
              // 圆角 + 5px 内缩，在浅底上也不突兀（白衬融进白底，肉眼看不出来）。
              ctx.fillStyle = '#ffffff'
              roundRect(ctx, geo.qr.x - 5, geo.qr.y - 5, geo.qr.size + 10, geo.qr.size + 10, 6)
              ctx.fill()
              ctx.drawImage(qimg, geo.qr.x, geo.qr.y, geo.qr.size, geo.qr.size)
            } else {
              // 没有码：画一个占位框，而不是留一片空白。
              // 留白 + 「长按识别名片」的组合会让用户以为图是好的，
              // 直接发出去才发现对方扫不出东西。
              drawQrPlaceholder(ctx, geo)
              // URL 拿到了但图加载失败（域名未配 downloadFile 白名单 / 对象被删）：
              // 和「接口没给码」一样要暴露出来，否则用户只会看到一张缺码的海报。
              if (that.data.qrUrl && !that.data.qrError && !that._drag) {
                that.setData({ qrError: true, qrMsg: '小程序码图片加载失败，请检查下载域名配置' })
              }
            }
            // 提示只在真的有码、且画得下时画：码都没有还提示「长按识别」是骗人
            if (qimg && geo.hint && geo.hint.show) {
              ctx.textAlign = 'center'
              ctx.fillStyle = geo.light ? 'rgba(255,255,255,0.78)' : '#666'
              ctx.font = '12px sans-serif'
              ctx.fillText('长按识别名片', geo.hint.x, geo.hint.y)
              ctx.textAlign = 'left'
            }
            if (done) done()
          })
        })
      }

      if (bg && bg.fileUrl) {
        that.loadCached(canvas, bg.fileUrl, (img: any) => {
          // 自定义背景是任意比例，按 cover 裁剪铺满（否则会拉伸变形）
          if (img) drawCover(ctx, img, W, H)
          paint(!!img)
        })
      } else {
        paint(false)
      }
    })
  },

  // reconcileOffsets 把几何夹紧后的位移写回 data / 存储。
  // 不做这一步，「下移」连点 30 次会让数字涨到 +300，而海报早就贴到画布底不动了——
  // 用户看到的是一串骗人的数字，而且往回拖时前半段完全没反应（死区）。
  // 拖动中直接改 this.data（视图层不需要中间值），松手由 onDragEnd 统一落库。
  reconcileOffsets(geo: Geo) {
    const L = this.data.layout
    if (
      L.offsetX === geo.offsetX && L.offsetY === geo.offsetY &&
      L.qrOffsetX === geo.qrOffsetX && L.qrOffsetY === geo.qrOffsetY
    ) {
      return
    }
    const layout = {
      ...L,
      offsetX: geo.offsetX, offsetY: geo.offsetY,
      qrOffsetX: geo.qrOffsetX, qrOffsetY: geo.qrOffsetY,
    }
    this.data.layout = layout
    if (this._drag) return // 拖动中：不 setData，避免每帧走一次视图层通信
    this.setData({ layout })
    this.saveLayout(layout)
  },

  // syncQrRect 把二维码热区挪到画布上码的位置。
  // 拖动过程中不更新：手指已经被那个热区捕获，位置对不上也不影响后续 move 事件；
  // 每帧 setData 反而会拖慢画面。
  syncQrRect(geo: Geo) {
    if (this._drag) return
    const cur = this.data.qrRect
    const next = {
      left: Math.round(geo.qr.x),
      top: Math.round(geo.qr.y),
      size: Math.round(geo.qr.size),
    }
    if (cur && cur.left === next.left && cur.top === next.top && cur.size === next.size) return
    this.setData({ qrRect: next })
  },

  // lastGeo 最近一次渲染的几何（拖动夹紧要用它的可拖区间）。
  // 还没画过时给一个兜底区间：宁可第一次拖动被夹得保守些，也不要让拖动抛异常。
  lastGeo(): Geo | null {
    return this._geo || null
  },

  // exportImage 把当前画布导成临时文件，供保存 / 分享使用。
  exportImage() {
    const that = this
    const canvas = this._canvas
    if (!canvas) {
      this.setData({ loading: false, error: true, errorText: '海报导出失败' })
      return
    }
    wx.canvasToTempFilePath({
      canvas,
      success: (r: any) => that.setData({ tempFilePath: r.tempFilePath, loading: false }),
      fail: () => {
        // 导出失败（画布为空 / 系统限制）：拿不到图片，后面保存与分享
        // 都无从下手，必须落到失败态让用户重试，不能只吞掉回调。
        that.setData({ loading: false, error: true, errorText: '海报导出失败' })
      },
    })
  },
  drawContent(ctx: any, W: number, _H: number, geo: Geo) {
    // 卡片底先画：它要压在背景之上、文字之下
    if (geo.card) {
      ctx.fillStyle = geo.light ? 'rgba(0,0,0,0.42)' : 'rgba(255,255,255,0.86)'
      roundRect(ctx, geo.card.x, geo.card.y, geo.card.w, geo.card.h, 10)
      ctx.fill()
    }

    // 主色分隔线（大字 / 画报模板）：画在文字之前，免得盖住下一行文字的笔画。
    // 不用 stroke 而用 fillRect：2px 的实心块在任何 dpr 下都不会糊。
    if (geo.rule) {
      ctx.fillStyle = geo.light ? 'rgba(255,255,255,0.78)' : '#1a8cff'
      ctx.fillRect(geo.rule.x, geo.rule.y, geo.rule.w, 2)
    }

    // 浅色字加投影兜底：素材下半部虽已压深，个别浅色海报（春分/小满）亮度
    // 仍偏高，没有投影会出现「白字糊在浅底上」。
    if (geo.light) {
      ctx.shadowColor = 'rgba(0, 0, 0, 0.55)'
      ctx.shadowBlur = 6
      ctx.shadowOffsetY = 1
    }

    for (const it of geo.items) {
      if (!it.text) continue
      ctx.fillStyle = geo.light
        ? it.accent
          ? '#9ad0ff'
          : it.dim
            ? 'rgba(255,255,255,0.82)'
            : '#ffffff'
        : it.accent
          ? '#1a8cff'
          : it.dim
            ? '#666666'
            : '#222222'
      ctx.font = `${it.weight || ''} ${it.size}px sans-serif`.trim()
      ctx.textAlign = it.align || 'left'
      ctx.fillText(this.clip(ctx, it.text, it.maxWidth), it.x, it.y)
    }
    ctx.textAlign = 'left'

    ctx.shadowColor = 'rgba(0, 0, 0, 0)'
    ctx.shadowBlur = 0
    ctx.shadowOffsetY = 0
  },
  // 文字超宽时用省略号收尾：公司全称 / 地址在 320px 宽的画布上很容易溢出，
  // 溢出后会盖到二维码上，比截断难看得多。
  clip(ctx: any, text: string, maxWidth: number): string {
    if (!text) return ''
    if (this.textWidth(ctx, text) <= maxWidth) return text
    let s = text
    while (s.length > 1 && this.textWidth(ctx, s + '…') > maxWidth) {
      s = s.slice(0, -1)
    }
    return s + '…'
  },
  textWidth(ctx: any, text: string): number {
    try {
      const m = ctx.measureText(text)
      return (m && m.width) || 0
    } catch (e) {
      // 老基础库 / 测试桩没有 measureText：按字号粗估，够用即可
      return text.length * 12
    }
  },
  // loadCached 带缓存的图片加载：同一 src 只真正加载一次，之后直接回缓存。
  // 拖动时每帧都要重画背景 + 头像 + 二维码，不走缓存的话每帧都在等网络/解码，
  // 画面会一顿一顿的。
  //
  // 失败也缓存，但只锁 3 秒：拖动时每帧都重画，不缓存失败就会每帧发一次网络请求
  // （断网 / 域名未配时尤其明显）；永久缓存又会让一次网络抖动把这张图在本页
  // 彻底判死。3 秒后允许重试，兼顾两头。
  loadCached(canvas: any, src: string, cb: (img: any) => void) {
    if (!src || !canvas || typeof canvas.createImage !== 'function') {
      cb(null)
      return
    }
    const cache = this._imgs || (this._imgs = {})
    if (cache[src]) {
      cb(cache[src])
      return
    }
    const fails = this._imgFails || (this._imgFails = {})
    const failedAt = fails[src]
    if (failedAt && Date.now() - failedAt < IMG_FAIL_TTL) {
      cb(null)
      return
    }
    const img = canvas.createImage()
    img.onload = () => {
      delete fails[src]
      cache[src] = img
      cb(img)
    }
    img.onerror = () => {
      fails[src] = Date.now()
      cb(null)
    }
    img.src = src
  },
  // 「发送给好友」：海报裂变闭环的主路径（生成 → 分享 → 好友看海报 →
  // 扫海报上的码进来记为 poster 来源）。此前这一页只有「保存到相册」，
  // 海报能不能发出去全靠用户自己想起来去相册里找——闭环断在最关键的一步。
  share() {
    if (!this.data.tempFilePath) {
      wx.showToast({ title: '海报生成中…', icon: 'none' })
      return
    }
    // showShareImageMenu 基础库 2.14.3 起才支持（官方文档），老版本直接调用
    // 会抛 unknown api；先探测，低版本退回「先存相册再手动发」，
    // 不至于点了按钮毫无反应（用户会以为是 bug）。
    if (typeof wx.canIUse !== 'function' || !wx.canIUse('showShareImageMenu')) {
      wx.showModal({
        title: '提示',
        content: '当前微信版本不支持直接分享图片，请先保存到相册，再从相册发给好友',
        showCancel: false,
      })
      return
    }
    const that = this
    wx.showShareImageMenu({
      path: this.data.tempFilePath,
      success: () => {
        // 口径与首页转发（onShareAppMessage）一致：微信只回「接口调用成功」
        // （文档原文如此），弹窗打开即 success，不告知用户是否真的发出去——
        // 故按「发起分享」计数，与首页转发同一把尺子，不追求确认送达。
        track('share', that.data.staffId)
      },
      fail: () => {},
    })
  },
  async save() {
    if (!this.data.tempFilePath) {
      wx.showToast({ title: '海报生成中…', icon: 'none' })
      return
    }
    // saveImageToPhotosAlbum 属微信隐私接口（相册写入），调用前须取得隐私政策同意。
    // 未同意则不写入相册——用户不同意就不能替他往设备里存东西。
    if (!(await ensurePrivacy(this))) {
      wx.showToast({ title: '需同意隐私政策才能使用', icon: 'none' })
      return
    }
    wx.saveImageToPhotosAlbum({
      filePath: this.data.tempFilePath,
      success: () => {
        // 海报保存成功才计一次（poster_save）：用户愿意把海报存进相册，
        // 说明有线下发送的意图——比「打开过海报页」强得多的意向信号。
        // 保存失败（权限拒绝 / 写入失败）不计，那只是没做成，不是没意愿。
        track('poster_save', this.data.staffId)
        wx.showToast({ title: '已保存到相册' })
      },
      fail: (e: any) => {
        if (e.errMsg && e.errMsg.indexOf('auth') >= 0) {
          wx.showModal({ title: '提示', content: '需要相册权限，请在设置中开启', showCancel: false })
        } else {
          wx.showToast({ title: '保存失败', icon: 'none' })
        }
      },
    })
  },
  // 右上角「转发」：转发出去的是海报页本身——好友打开即看到同一张海报，
  // 可继续保存或直接转发，是裂变链路的下一段（此前这一页没有转发出口，
  // 从海报页点右上角转发拿不到任何分享配置）。
  onShareAppMessage() {
    const emp = this.data.card && this.data.card.employee
    const comp = this.data.card && this.data.card.company
    const name = (emp && emp.name) || ''
    // 与分享图片同口径：发起转发即计一次 share（targetId=0，名片级转发）
    track('share', this.data.staffId)
    return {
      title: (name ? name + '的名片' : '') || (comp && (comp.shortName || comp.fullName)) || '电子名片',
      path: '/pages/poster/poster?staffId=' + this.data.staffId,
    }
  },
})

// ---- 纯函数（不依赖页面实例，便于单测）----

// clampNum 数值夹紧。顺带挡掉 NaN / Infinity：存储里被手工写成 "abc" 时
// parseInt 得到 NaN，不兜住会一路算出 NaN 坐标，画布直接一片空白。
export function clampNum(v: number, lo: number, hi: number): number {
  if (!isFinite(v)) return lo
  return v < lo ? lo : v > hi ? hi : v
}

// shiftRange 由「内容未位移时占据的 [lo, hi] 区间」反推可拖区间。
// 想让左边缘停在 margin 处 → 位移最小是 margin - lo；
// 想让右边缘停在 canvas - margin 处 → 位移最大是 canvas - margin - hi。
// 区间反了（内容比画布还宽）时返回 [0, 0]：宁可钉死不动，
// 也不要把内容推到一个更糟的位置上。
export function shiftRange(lo: number, hi: number, canvas: number, margin: number): [number, number] {
  const min = margin - lo
  const max = canvas - margin - hi
  return min > max ? [0, 0] : [min, max]
}

// measureBox 量内容包围盒（位移前）。
// 左/右按文字实际宽度（超长文字会被 clip 截断，所以按 maxWidth 封顶）；
// 上/下按字号估字形上下沿——中文常用字大约在基线上方 0.82em、下方 0.2em，
// 夹紧够用了，不值得为每个字去查 ascent/descent。
function measureBox(
  items: TextItem[],
  avatar: { x: number; y: number; size: number } | null,
  card: { x: number; y: number; w: number; h: number } | null,
  measure?: (text: string, size: number, weight?: string) => number
) {
  let left = Infinity
  let right = -Infinity
  let top = Infinity
  let bottom = -Infinity
  const grow = (l: number, r: number, t: number, b: number) => {
    if (l < left) left = l
    if (r > right) right = r
    if (t < top) top = t
    if (b > bottom) bottom = b
  }
  for (const it of items) {
    if (!it.text) continue
    // 没传 measure（单测 / 还没拿到 ctx）：按 1em/字粗估，宁可估宽一点
    const w = Math.min(
      measure ? measure(it.text, it.size, it.weight) : it.text.length * it.size,
      it.maxWidth
    )
    const l = it.align === 'center' ? it.x - w / 2 : it.x
    grow(l, l + w, it.y - it.size * 0.82, it.y + it.size * 0.2)
  }
  if (avatar) grow(avatar.x, avatar.x + avatar.size, avatar.y, avatar.y + avatar.size)
  if (card) grow(card.x, card.x + card.w, card.y, card.y + card.h)
  if (left === Infinity) return { left: 0, right: 0, top: 0, bottom: 0 }
  return { left, right, top, bottom }
}

// normalizeLayout 客户端侧的版式归一，与后端 handler/poster.go 的
// posterLayoutPatch.resolve 保持同一套规则。
// 为什么要两边都做：后端挡住脏数据入库，客户端挡住脏数据进画布——
// 少一边就会出现「接口正常但海报画错」这种最难查的问题。
export function normalizeLayout(input: any): Layout {
  const out: Layout = { ...DEFAULT_LAYOUT }
  if (!input || typeof input !== 'object') return out
  const tpl = TEMPLATES.map((t) => t.key)
  if (tpl.indexOf(input.template) >= 0) out.template = input.template
  if (input.textColor === 'auto' || input.textColor === 'light' || input.textColor === 'dark') {
    out.textColor = input.textColor
  }
  for (const k of [
    'showAvatar', 'showName', 'showTitle', 'showCompany',
    'showAddress', 'showPhone', 'showLandline', 'showEmail', 'showQr',
  ] as const) {
    if (typeof input[k] === 'boolean') out[k] = input[k]
  }
  if (typeof input.customText === 'string') out.customText = normalizeCustomText(input.customText)
  const oy = parseInt(input.offsetY, 10)
  if (!isNaN(oy)) out.offsetY = clampNum(oy, -OFFSET_LIMIT, OFFSET_LIMIT)
  const ox = parseInt(input.offsetX, 10)
  if (!isNaN(ox)) out.offsetX = clampNum(ox, -OFFSET_LIMIT, OFFSET_LIMIT)
  const qy = parseInt(input.qrOffsetY, 10)
  if (!isNaN(qy)) out.qrOffsetY = clampNum(qy, -OFFSET_LIMIT, OFFSET_LIMIT)
  const qx = parseInt(input.qrOffsetX, 10)
  if (!isNaN(qx)) out.qrOffsetX = clampNum(qx, -OFFSET_LIMIT, OFFSET_LIMIT)
  const as = parseInt(input.avatarSize, 10)
  if (!isNaN(as)) out.avatarSize = nearestAvatarSize(as)
  // 二维码尺寸按档位吸附（与头像同一思路）：手工改库 / 老客户端传来的
  // 77 这类值没有对应档位可高亮，吸附到最近的档位，界面才不会「选中态消失」
  const qs = parseInt(input.qrSize, 10)
  if (!isNaN(qs)) out.qrSize = nearestQrSize(qs)
  return out
}

// normalizeCustomText 自定义文字归一：去首尾空白、把换行/制表/连续空格压成
// 单个空格、剔除控制字符，最后按字符数截断。
// 为什么必须压平换行：canvas 的 fillText 不认 \n（会画成一个方块或直接吞掉），
// 而输入框里用户很自然会敲回车——不处理就会得到一条莫名其妙缺字的标语。
export function normalizeCustomText(v: string): string {
  if (!v) return ''
  const flat = v
    .replace(/[\u0000-\u001f\u007f]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  // 按码点截断，别把 emoji 劈成两半
  return Array.from(flat).slice(0, CUSTOM_TEXT_MAX).join('')
}

// 当天该自动选的那张海报：**节日优先于节气**，但每一级都要「素材真在清单里」。
//
// 为什么要逐级回落而不是「节日命中就直接用节日」：素材库是会变的（老后端没有
// 新加的节日图、运营下架了某张）。2026-03-20 既是龙抬头又是春分，若后端清单里
// 只有 poster-chunfen，正确的表现是选春分那张，而不是因为缺一张龙抬头就一路
// 退到商务默认款——节气图的意义被一张缺失的素材连带抹掉，说不过去。
//
// 返回 null = 今天既没有可用的节日图也没有节气图（含表外年份），
// 由调用方走默认商务款。
function pickToday(
  festival: Festival | null,
  term: SolarTerm | null,
  posters: any[]
): { id: string; name: string; kind: 'festival' | 'term' } | null {
  if (festival && posters.some((p) => p.id === festival.posterId)) {
    return { id: festival.posterId, name: festival.name, kind: 'festival' }
  }
  if (term && posters.some((p) => p.id === term.posterId)) {
    return { id: term.posterId, name: term.name, kind: 'term' }
  }
  return null
}

// myStaffId 本机已知的「登录员工编号」（0 = 不知道）。
// 从 globalData 读而不是每次打接口——「我的」页登录成功后就写好了，
// 而海报页的主入口正是「我的 → 生成海报」。取值失败（小程序环境外、单测未桩）
// 一律返回 0，由调用方降级处理。
function myStaffId(): number {
  try {
    const app: any = typeof getApp === 'function' ? (getApp as any)() : null
    return app && app.globalData ? Number(app.globalData.myStaffId) || 0 : 0
  } catch {
    return 0
  }
}

// visitorTipOf 只读态的一行说明（纯函数，单测可断言）。
// 有名有姓时点出「这是谁的名片」——访客是从别人那儿转来的，先确认人再看内容；
// 取不到名字就只说能做什么，绝不蹦出「未知员工」这种内部味的话。
function visitorTipOf(card: any): string {
  const name = card && card.employee ? String(card.employee.name || '') : ''
  return name ? `${name}的名片海报 · 保存或转发帮 TA 传播` : '保存或转发这张名片海报'
}

// resolveBgId 选中项兜底。三种情况要分开：
//   null      从没选过 → 默认商务款（节气会过时，立春的图十月发很怪）
//   ''        明确选了「原样」→ 白底，不能又被默认款顶掉
//   具体 id   上次选的可能已被删除 / 素材库升级后下架 → 回落默认款
// 拿着一个画不出来的 id 会让画布去加载空图，用户看到的是「背景突然没了」。
// resolveBgId 定「进来时选中哪张」。
//   saved   = null/undefined 从没选过；'' 明确选了白底；其余是选过的 id
//   preferId = 当天该用的素材 id（节日优先，其次节气；都不是传 undefined）
// 从没选过时优先 preferId（节日/节气图），否则商务默认款，再否则白底。
//
// 注意「saved 正好等于 DEFAULT_BG」由调用方折算成 null 再进来（见 buildState）：
// 默认款是兜底值而不是用户的审美表态，把它当「没选过」才能让节气自动选
// 在每个节气日都生效，而不是只在用户一生中第一次进页面时生效一次。
export function resolveBgId(
  saved: string | null,
  posters: any[],
  customs: any[],
  preferId?: string
): string {
  if (saved === null || saved === undefined) {
    if (preferId && posters.some((p) => p.id === preferId)) return preferId
    return posters.some((p) => p.id === DEFAULT_BG) ? DEFAULT_BG : BG_NONE
  }
  if (!saved) return BG_NONE
  if (customs.some((p) => p.id === saved)) return saved
  if (posters.some((p) => p.id === saved)) return saved
  if (posters.some((p) => p.id === DEFAULT_BG)) return DEFAULT_BG
  return BG_NONE
}

// nearestAvatarSize 取最接近的合法档位（与后端同一规则：等距取较小档）
export function nearestAvatarSize(v: number): number {
  let best = AVATAR_SIZES[1]
  let bestDiff = -1
  for (const s of AVATAR_SIZES) {
    const d = Math.abs(v - s)
    if (bestDiff < 0 || d < bestDiff) {
      best = s
      bestDiff = d
    }
  }
  return best
}

// nearestQrSize 二维码尺寸吸附到最近档位。
// 0（自动）不参与「最近」比较：30 应该吸到 76 而不是「自动」——
// 用户给了一个具体数值，说明他要的是固定大小，不是自适应。
export function nearestQrSize(v: number): number {
  if (!v || v <= 0) return 0
  let best = QR_SIZES[1]
  let bestDiff = -1
  for (const s of QR_SIZES) {
    if (!s) continue
    const d = Math.abs(v - s)
    if (bestDiff < 0 || d < bestDiff) {
      best = s
      bestDiff = d
    }
  }
  return best
}

// 头像画成圆形：方图压在渐变上边角生硬，圆形更像名片。
// 用 clip 而非需要圆角图片的方案，避免额外依赖。
function drawAvatar(ctx: any, img: any, geo: { x: number; y: number; size: number }) {
  ctx.save()
  ctx.beginPath()
  ctx.arc(geo.x + geo.size / 2, geo.y + geo.size / 2, geo.size / 2, 0, Math.PI * 2)
  ctx.closePath()
  ctx.clip()
  ctx.drawImage(img, geo.x, geo.y, geo.size, geo.size)
  ctx.restore()
}

// drawCover 按 cover 裁剪铺满：自定义背景是用户随手拍的图，比例五花八门，
// 直接拉伸会把人脸压扁。
function drawCover(ctx: any, img: any, W: number, H: number) {
  const iw = img.width || W
  const ih = img.height || H
  const scale = Math.max(W / iw, H / ih)
  const dw = iw * scale
  const dh = ih * scale
  ctx.drawImage(img, (W - dw) / 2, (H - dh) / 2, dw, dh)
}

// drawScrim 从 fromY 到画布底铺一层「透明 → 黑」的渐变。
// 老基础库没有 createLinearGradient 时退成一层半透明黑：效果差一点，
// 但至少文字还能读——总比白字糊在白墙上强。
function drawScrim(ctx: any, W: number, H: number, fromY: number) {
  const h = H - fromY
  if (h <= 0) return
  if (typeof ctx.createLinearGradient === 'function') {
    const g = ctx.createLinearGradient(0, fromY, 0, H)
    g.addColorStop(0, 'rgba(0,0,0,0)')
    g.addColorStop(1, 'rgba(0,0,0,0.62)')
    ctx.fillStyle = g
  } else {
    ctx.fillStyle = 'rgba(0,0,0,0.45)'
  }
  ctx.fillRect(0, fromY, W, h)
}

// roundRect 手写圆角矩形：老基础库的 ctx.roundRect 不一定有，
// 而 card 模板没有圆角就是一块生硬的方块。
function roundRect(ctx: any, x: number, y: number, w: number, h: number, r: number) {
  const rr = Math.min(r, w / 2, h / 2)
  ctx.beginPath()
  ctx.moveTo(x + rr, y)
  ctx.lineTo(x + w - rr, y)
  ctx.arc(x + w - rr, y + rr, rr, -Math.PI / 2, 0)
  ctx.lineTo(x + w, y + h - rr)
  ctx.arc(x + w - rr, y + h - rr, rr, 0, Math.PI / 2)
  ctx.lineTo(x + rr, y + h)
  ctx.arc(x + rr, y + h - rr, rr, Math.PI / 2, Math.PI)
  ctx.lineTo(x, y + rr)
  ctx.arc(x + rr, y + rr, rr, Math.PI, Math.PI * 1.5)
  ctx.closePath()
}

// drawQrPlaceholder 二维码缺失时的占位框。
// 为什么不留白：海报上唯一的入口就是这张码，留白 + 「长按识别名片」会让
// 用户以为图是好的，直接发出去对方却扫不出东西。画个虚线框写「未生成」，
// 至少他自己能一眼看出这张图还不能发。
function drawQrPlaceholder(ctx: any, geo: Geo) {
  const { x, y, size } = geo.qr
  ctx.fillStyle = geo.light ? 'rgba(255,255,255,0.10)' : 'rgba(0,0,0,0.05)'
  roundRect(ctx, x, y, size, size, 8)
  ctx.fill()
  // setLineDash 在老基础库可能没有：没有就不画虚线，实线一样能表达「框」
  if (typeof ctx.setLineDash === 'function') ctx.setLineDash([6, 4])
  ctx.strokeStyle = geo.light ? 'rgba(255,255,255,0.55)' : 'rgba(0,0,0,0.3)'
  ctx.lineWidth = 1
  ctx.stroke()
  if (typeof ctx.setLineDash === 'function') ctx.setLineDash([])

  ctx.textAlign = 'center'
  ctx.fillStyle = geo.light ? 'rgba(255,255,255,0.8)' : '#666'
  ctx.font = '12px sans-serif'
  ctx.fillText('小程序码', x + size / 2, y + size / 2 - 2)
  ctx.font = '10px sans-serif'
  ctx.fillStyle = geo.light ? 'rgba(255,255,255,0.6)' : '#999'
  ctx.fillText('未生成', x + size / 2, y + size / 2 + 14)
  ctx.textAlign = 'left'
}

function empAvatar(card: any): string {
  return card && card.employee ? card.employee.avatar : ''
}
