// 公司简介视频：按后端 videoSource 选档渲染，主源报错时（可选）回落到 MP4。
//
// 四档都能配兜底，但走的是两条不同的地址槽：
//   channels / tencent 主源失败 → 用 videoUrl（这两档下它没有别的用途）
//   upload / url       主 MP4 失败 → 用 backupUrl（videoUrl 是这一档的主源，
//                      再加一个地址只能另开一列，见后端迁移 0038）
// 开关统一是 fallback。自托管档只切一次，备用也失败才显失败态。
//
// 画幅（2026-11-05 二次修正）：原生 MP4 档**按源文件分辨率调整容器高度**，
// 再用 object-fit="contain" 完整呈现。早先为了「满屏」把固定 440rpx 容器配上
// cover，结果左右各裁掉约 7%，两侧字幕/LOGO 直接消失——裁内容不可接受。
// 视频号 / 腾讯视频两档是各自的插件组件，没有 loadedmetadata 可用，仍按原样。
//
// 为什么不做单一来源：不同客户能接受的成本与资质条件不一样——
//   channels 微信视频号：零存储零流量、官方无资质要求
//   upload   云存储上传：落在客户自己的对象存储，直传不经我们的服务器
//   url      外部 MP4 直链：最灵活，流量与视听类目资质由客户自行承担
//   tencent  腾讯视频插件：视频托管在腾讯视频，原位播放、画面无视频号角标，
//            但插件要在 app.json 静态声明（未授权会连累整个小程序编译失败），
//            故默认关闭、由部署方显式开启（后台开关 MP_TENCENT_VIDEO）
// 选档由后台显式完成（不是隐式自动降级链），出问题时能一眼看出当前走的是哪一路。
//
// 视频号档内还分两种凭证：
//   feed-id     同主体（视频号与小程序同主体），可自动播放，稳定且可从视频号助手直接复制
//   feed-token  跨主体专用——官方明确 feed-id「仅同主体时生效」，不同主体必须改用它，
//               代价是强制 autoplay=false。平台模式下客户的视频号与我们小程序必然不同主体，
//               走的就是这条。feed-id 优先：两个都填时只认 feed-id。
//
// 渲染档位：channels / channels-token 用 channel-video；txv 用插件的 txv-video；
// mp4 用 video；none 回封面占位。
//
// 播放埋点（video_play）覆盖 mp4 与 txv 两档：官方 channel-video 组件只开放 binderror
// 一个事件，没有播放回调（video 组件自 1.0.0 起有 bindplay）——视频号档的播放
// 统计缺失是文档级边界，不是漏接；要补只能等官方开放事件，或改用页面级
// 「视频区曝光」近似（那会把「看到没播」算成兴趣，口径变浑，故意不做）。
//
// txv-video 是腾讯视频插件组件，只在 miniprogram/scripts/enable-tencent-video.mjs
// 打开开关后（app.json 声明插件 + 本组件 index.json 注册组件）才可用。
import { track } from '../../utils/track'
import { onImgFail } from '../../utils/thumb'

// 腾讯视频插件注入到小程序的全局函数。本项目 tsconfig 没引 wx 的 typings，
// 就近声明一次，免得 tsc 报 Cannot find name（模块内声明，不污染全局）。
declare function requirePlugin(name: string): any
// 同理声明量播放器宽度用的 wx.createSelectorQuery。声明不生成代码，
// 真机上 wx 由小程序框架注入；单测里用 vi.stubGlobal 顶替。
declare const wx: any

// 画幅安全区间（height / width）。16:9=0.5625、4:3=0.75 都按原样；
// 9:16 竖屏若真按 1.78 撑高，公司简介区会把整页顶下去，钳到 4/3 后
// 由 contain 留左右黑边——仍不裁画面，属可接受的降级。
const MIN_MP4_RATIO = 0.4
const MAX_MP4_RATIO = 4 / 3

// 由「容器实测宽度」与「视频原始宽高」算出播放器行内样式。
// 返回空串 = 不覆盖 WXSS 的 440rpx 默认高度：最多留黑边，绝不裁切。
// 抽成纯函数是为了能单测像素计算，不必把 wx 的链式查询造一遍。
export function mp4BoxStyleFor(boxWidth: number, width: number, height: number): string {
  if (!(boxWidth > 0) || !(width > 0) || !(height > 0)) return ''
  const raw = height / width
  if (!Number.isFinite(raw)) return ''
  const ratio = Math.min(MAX_MP4_RATIO, Math.max(MIN_MP4_RATIO, raw))
  return `height:${Math.round(boxWidth * ratio)}px`
}

// 必须与 index.wxml 里 <txv-video playerid="..."> 一字不差——
// 两者对不上时 getTxvContext 返回 null，表现就是「点了没反应」这种静默失败。
// 有静态守门用例钉住这个一致性。
const TXV_PLAYER_ID = 'companyVideo'
// app.json 里 plugins 的 key（全小写，不是官方示例里的 tencentVideo）。
const TXV_PLUGIN_NAME = 'tencentvideo'
// 起播等待上限。play() 只是「发起播放」，真正起播靠 bindplay 回调。
// vid 未过审、网络断了这类情况下插件可能既不播也不报 error，
// 没有这个兜底，用户会对着一个永远转圈的加载态。
const TXV_START_TIMEOUT_MS = 10000

let txvModule: any = null
let txvModuleLoaded = false

// 取插件的播放器上下文，用于手动 play()。
// 任何一环失败都返回 null（不抛错），调用方据此退化回插件自带的播放按钮——
// 这条路必须在「腾讯视频档默认关闭、插件未在 app.json 声明」的部署上也是安全的。
function getTxvContext(playerId: string): any {
  if (!txvModuleLoaded) {
    txvModuleLoaded = true
    try {
      txvModule = typeof requirePlugin === 'function' ? requirePlugin(TXV_PLUGIN_NAME) : null
    } catch (e) {
      // 未声明插件时 requirePlugin 直接抛错。tencent 档默认关闭，正常走不到这里，
      // 但组件注册不能因为插件缺失而整体失败，所以照样兜住。
      console.warn('[company-video] 腾讯视频插件未声明，无法手动控制播放', e)
      txvModule = null
    }
  }
  if (!txvModule || typeof txvModule.getTxvContext !== 'function') return null
  try {
    return txvModule.getTxvContext(playerId) || null
  } catch (e) {
    // 组件还没渲染完时取上下文会失败。点击时通常已经渲染完了，
    // 真碰上就退化，不打断用户。
    console.warn('[company-video] 取 TxvContext 失败（组件可能尚未渲染完成）', e)
    return null
  }
}

function pickMode(
  source: string,
  feedId: string,
  feedToken: string,
  vid: string,
  videoUrl: string,
  fallback: boolean,
  failed: boolean
): string {
  const url = (videoUrl || '').trim()
  if (source === 'channels' && !failed) {
    if ((feedId || '').trim()) return 'channels'
    if ((feedToken || '').trim()) return 'channels-token'
  }
  // 腾讯视频插件：vid 是唯一取片凭证。与视频号跨主体档不同，它就在页面内原位播放，
  // 画面上也没有视频号名称与推荐角标——这是客户「原位播放 + 干净画面」诉求的正解之一。
  if (source === 'tencent' && (vid || '').trim() && !failed) return 'txv'
  // 云存储上传 / 外部直链本身就是 MP4，直接用 video 播放
  if ((source === 'upload' || source === 'url') && url) return 'mp4'
  // 兜底：只有视频号 / 腾讯视频主源失败、后台开了开关、且真填了 MP4 时才顶上
  if (failed && fallback && url) return 'mp4'
  return 'none'
}

Component({
  options: { addGlobalClass: true },
  properties: {
    videoSource: String,
    // 同主体凭证：视频号视频 feedId（优先）
    feedId: String,
    // 跨主体凭证：feed-token（feedId 为空时启用）
    feedToken: String,
    // 腾讯视频 vid（tencent 档唯一凭证）。插件须在 app.json 静态声明，
    // 未声明时 txv-video 是未注册组件，渲染不出来——故默认由部署方关闭该档，
    // 声明插件并开启开关后这里才会收到非空 vid。
    vid: String,
    videoUrl: String,
    // 自托管 MP4（upload / url）档的备用地址。与主源分开传：videoUrl 在这两档
    // 是主视频源，在 channels / tencent 两档又兼作兜底源，一个槽装不下两种语义。
    // 只有这两档的主 MP4 失败时才用它，且只切一次（见 onVideoError）。
    backupUrl: String,
    fallback: Boolean,
    // 进页面就自动播放。**默认 false**，且必须显式下传到 <txv-video>：
    // 腾讯视频插件的 autoplay 默认是 true（官方参数表如此，与微信原生 video 的
    // false 相反）。不写这个属性 = 继承插件的默认值 = 一进名片页就带声音自己播。
    autoplay: Boolean,
    cover: String,
    // 埋点归属：视频播放计到哪位员工的名片账上（首页透传）
    staffId: Number,
  },
  data: {
    mode: 'none',
    failed: false,
    // 封面图加载失败的地址：WXML 用 `cover && coverFail !== cover` 判断是否还画它，
    // 失败就退到同风格的蓝底占位（.cv-ph）。记 URL 而不是布尔标记，换封面自动重试。
    coverFail: '',
    // 播放埋点去重标记：同一组件实例只报一次。微信 video 的 bindplay 在
    // 「暂停后继续播放」时会再次触发，不去重的话一次观看被记成多次——
    // 「访客反复看了几遍」与「手滑暂停再播」在统计里无法区分。
    playTracked: false,
    // 腾讯视频档的启动遮罩状态（只对这一档有意义，见 index.wxml 注释）。
    // txvStarted：是否**真的开播过**。决定用「全屏遮罩」（首次）还是「中央小遮罩」（暂停）。
    txvStarted: false,
    // txvPlaying：当前是否正在播。由 bindplay / bindpause / bindended 维护——
    // 少了 pause/ended 这两个回调，遮罩就是一次性的：播过之后再也不出现，
    // 暂停了想继续只能去角落里翻插件那个小按钮（2026-11-04 用户反馈的正是这个）。
    txvPlaying: false,
    // txvStarting：已点击、正在等起播。用来把播放图标换成转圈，给点击一个即时反馈。
    txvStarting: false,
    // txvMaskOff：遮罩永久关闭。只在「根本点不动」的退化路径上置位
    // （取不到播放器上下文 / play() 抛错 / 起播超时），此后一切交给插件 UI。
    txvMaskOff: false,
    // txvEnded：已播完。播完再点要从头播，光调 play() 在部分机型上没有反应，得先 seek(0)。
    txvEnded: false,
    // MP4 档的加载失败态。以前只在控制台打一行 warn，访客看到的是一块
    // 黑屏 + 00:00（2026-11-05 真实反馈），既不知道失败也无从恢复。
    // mp4Src 单独存一份：重试要先把 src 清空让 <video> 销毁再设回来，
    // 对同一个地址直接 setData 不会重新发请求。
    mp4Error: false,
    mp4Src: '',
    // 播放器行内高度：bindloadedmetadata 拿到源文件宽高后按容器宽度算出，
    // 覆盖 .cv-video 的 440rpx 默认高度，让窗口比例等于视频比例。
    // 空串 = 还没拿到 metadata（或量不到宽度），此时保持默认 + contain，
    // 只可能留黑边，不会裁内容。换地址/切备用/重试时都要复位。
    mp4BoxStyle: '',
    // 自托管 MP4 档是否已经切过备用地址。只切一次：备用地址也失败就显示失败态，
    // 否则主源与备用互相切会形成无限循环，且每次失败都重置出一块新的黑屏。
    // 换档 / 换地址时由 observer 复位，等于一次全新尝试。
    mp4FallbackUsed: false,
  },
  observers: {
    // 任一入参变化都重算档位（后台改配置后刷新即可生效，无需重新进入页面）。
    // 同时重置去重标记：档位变了等于换了一段视频，应当允许再计一次播放。
    // backupUrl 也进监听列表：后台改了备用地址就该把「已切过备用」的标记复位，
    // 否则刷新后仍是旧状态，主源一失败直接显示失败态，新配置的备用地址根本没机会用。
    // 回调不声明它——JS 允许少接参数，取值一律在 onVideoError 里读 this.data。
    'videoSource, feedId, feedToken, vid, videoUrl, fallback, backupUrl': function (
      videoSource: string,
      feedId: string,
      feedToken: string,
      vid: string,
      videoUrl: string,
      fallback: boolean
    ) {
      this.setData({
        failed: false,
        playTracked: false,
        // 换了档 / 换了地址就当是一次全新的尝试：清掉旧的失败态与播放器
        mp4Error: false,
        mp4FallbackUsed: false,
        mp4Src: (videoUrl || '').trim(),
        // 换了一段视频：旧视频算出来的高度不能沿用，等新 metadata 重算
        mp4BoxStyle: '',
        mode: pickMode(
          videoSource || '',
          feedId || '',
          feedToken || '',
          vid || '',
          videoUrl || '',
          !!fallback,
          false
        ),
      })
    },
  },
  lifetimes: {
    // 组件销毁时清掉起播超时：否则超时回调会对着已卸载的实例 setData。
    detached() {
      this.clearStartTimer()
    },
  },
  methods: {
    // 封面图加载失败：记下失败的地址，WXML 据此退到蓝底占位（见 utils/thumb.ts）
    onImgFail,
    // 清掉「等起播」的超时器。组件销毁、真的开播、点击失败三条路都要调，
    // 否则会对已卸载的实例 setData（开发者工具会告警）。
    clearStartTimer() {
      const t = (this as any)._startTimer
      if (!t) return
      clearTimeout(t)
      ;(this as any)._startTimer = null
    },
    // 点了视频区域（首次启动的全屏遮罩 / 暂停态的中央遮罩都会走到这里）。
    // 插件自带播放键默认在左下角，小到没人会注意到，所以由遮罩接这一下。
    onTapTxvPlay() {
      // 连点保护：已经在等起播了就别再叠一次 play()
      if (this.data.txvStarting) return
      const ctx = getTxvContext(TXV_PLAYER_ID)
      if (!ctx) {
        // 拿不到播放器上下文（插件没声明 / 组件还没渲染完）：把遮罩永久撤掉，
        // 界面退回插件的按钮。宁可多一次点击，也不能让用户点一块点不动的板。
        console.warn('[company-video] 取不到 TxvContext，退回插件自带播放按钮')
        this.setData({ txvMaskOff: true, txvStarting: false })
        return
      }
      // 播完再点 = 从头看一遍。ended 之后直接 play() 在部分机型上没反应，
      // 得先 seek(0)；seek 失败也不能拦住后面的 play()，所以单独兜一层。
      if (this.data.txvEnded) {
        try {
          ctx.seek(0)
        } catch (e) {
          console.warn('[company-video] 重播前 seek(0) 失败，继续尝试播放', e)
        }
        this.setData({ txvEnded: false })
      }
      this.setData({ txvStarting: true })
      try {
        ctx.play()
      } catch (e) {
        console.warn('[company-video] 调用 play() 失败，退回插件自带播放按钮', e)
        this.setData({ txvMaskOff: true, txvStarting: false })
        return
      }
      this.clearStartTimer()
      ;(this as any)._startTimer = setTimeout(() => {
        ;(this as any)._startTimer = null
        // 超时还没开播：永久撤遮罩，把界面交回插件（它自己会画错误提示），
        // 别让用户对着一个永远转的圈，也别让后续每次点击都卡在同一个死循环里。
        if (this.data.txvPlaying) return
        this.setData({ txvMaskOff: true, txvStarting: false })
      }, TXV_START_TIMEOUT_MS)
    },
    // video_play 埋点（mp4 / txv 档；channel-video 无播放事件，见文件头说明）。
    // 组件实例级去重：暂停恢复再播不重复计。
    onVideoPlay() {
      // 真的开播了：撤遮罩、清掉起播超时。
      this.clearStartTimer()
      if (!this.data.txvStarted || !this.data.txvPlaying || this.data.txvStarting) {
        this.setData({ txvStarted: true, txvPlaying: true, txvStarting: false, txvEnded: false })
      }
      if (this.data.playTracked) return
      this.setData({ playTracked: true })
      track('video_play', this.data.staffId || 0)
    },
    // 暂停：把中央的「继续播放」放出来。
    // 触发源有三个——插件的暂停键、双击手势、切后台被系统暂停——都走这一个回调，
    // 所以不用自己判断是谁按的。
    onVideoPause() {
      this.clearStartTimer()
      if (this.data.txvPlaying || this.data.txvStarting) {
        this.setData({ txvPlaying: false, txvStarting: false })
      }
    },
    // 播完：回到「点一下再看一遍」的状态。播过（txvStarted）才会出现中央遮罩，
    // 所以这里不用动 txvStarted——它记的是「这个播放器启动过了」，不是「正在播」。
    onVideoEnded() {
      this.clearStartTimer()
      this.setData({ txvPlaying: false, txvStarting: false, txvEnded: true })
    },
    // 按视频源文件分辨率调整播放窗口（2026-11-05 用户纠正 cover 裁切后）。
    // 原生 video 的 loadedmetadata（基础库 2.7.0+）带 {width,height,duration}，
    // 这是拿到真实宽高的唯一途径；容器宽度要现场量——首页卡片有左右内边距，
    // 不同机型宽度不同，拿 750rpx 硬算会偏高，黑边又回来了。
    // 算出高度写进行内 style，配合 object-fit="contain"：
    // 容器比例 == 视频比例，既不裁切也不留黑边。
    onVideoMeta(e?: { detail?: { width?: number; height?: number } }) {
      const d = (e && e.detail) || {}
      const w = Number(d.width) || 0
      const h = Number(d.height) || 0
      if (!(w > 0) || !(h > 0)) return
      const that = this
      try {
        wx.createSelectorQuery()
          .in(this)
          .select('.cv-mp4')
          .boundingClientRect((rect: any) => {
            const bw = rect && Number(rect.width) > 0 ? Number(rect.width) : 0
            const style = mp4BoxStyleFor(bw, w, h)
            // 量不到宽度就维持默认高度（contain 留黑边），不猜、不裁；
            // 同值不重复 setData，避免 metadata 多次回调引发无谓重渲染。
            if (style && style !== (that.data as any).mp4BoxStyle) {
              that.setData({ mp4BoxStyle: style })
            }
          })
          .exec()
      } catch (err) {
        console.warn('[company-video] 读取播放器宽度失败，保持默认画幅', err)
      }
    },
    onVideoError(e?: { detail?: Record<string, unknown> }) {
      const s: any = this.data
      const currentSrc = (s.mp4Src || '').trim()
      // feed-token 无效、基础库不支持、vid 未过审、视频被删……官方只在 binderror 的
      // detail 里给原因。吞掉它，界面上永远只是「停在封面图」，排查只能靠猜——
      // 打出来，体验版开调试即可见。MP4 档额外把**当前** src 一起打：这类失败九成是
      // 地址不对（相对路径 / 证书 526 / 对象不存在），光看 errMsg 定位不了。
      // 必须打 mp4Src 而不是 videoUrl：切到备用地址后再报错，打主源地址会把人带偏。
      console.warn(
        '[company-video] 视频组件报错',
        (e && e.detail) || e || '',
        'src=', s.mode === 'mp4' ? currentSrc : (s.videoUrl || ''),
        s.mp4FallbackUsed ? '(备用地址)' : ''
      )
      // upload / url 两档本来就是 MP4，再回落没有意义；改显一个可操作的错误态。
      // 用 mode 判而不是只看 videoSource：视频号 / 腾讯视频兜底切过来的 MP4
      // （mode 已经是 mp4）再失败时，同样该让玩家看见而不是继续黑屏。
      if (s.mode === 'mp4' || s.videoSource === 'upload' || s.videoSource === 'url') {
        // 自托管 MP4 档：主源失败时先切备用地址（视频号 / 腾讯视频档的 videoUrl
        // 兜底不适用——它们本来就是「已经切过一次」了，再切只会来回打转）。
        // 三个条件缺一不可：开了兜底、真填了备用地址、还没切过。
        const selfHosted = s.videoSource === 'upload' || s.videoSource === 'url'
        const backup = (s.backupUrl || '').trim()
        if (selfHosted && !!s.fallback && backup && !s.mp4FallbackUsed && backup !== currentSrc) {
          console.warn('[company-video] 主 MP4 加载失败，切换到备用地址', backup)
          // 与重试同理：先清空 src 让 <video> 销毁，再在渲染完成回调里设回备用地址。
          // 直接 setData 换 src，部分机型上不会重新发请求，表现是「切了但没生效」。
          // mp4Error 一并清掉：此刻还没到该认输的时候，不要闪一下失败态。
          // mp4BoxStyle 复位：备用视频可能有别的分辨率，旧高度先撤掉等新 metadata。
          this.setData(
            { mp4Error: false, mp4FallbackUsed: true, mp4Src: '', mp4BoxStyle: '' },
            () => {
              this.setData({ mp4Src: backup })
            }
          )
          return
        }
        this.setData({ mp4Error: true })
        return
      }
      // 这里只剩视频号 / 腾讯视频主源失败：按后台开关决定要不要切兜底 MP4
      const src = s.videoSource || ''
      if (src !== 'channels' && src !== 'tencent') return
      const next = pickMode(
        src,
        s.feedId || '',
        s.feedToken || '',
        s.vid || '',
        s.videoUrl || '',
        !!s.fallback,
        true
      )
      if (next !== s.mode) {
        this.setData({
          failed: true,
          mode: next,
          mp4Src: (s.videoUrl || '').trim(),
          mp4BoxStyle: '',
        })
      }
    },
    // 重新加载 MP4：先把 src 清空让 <video> 销毁，再在 setData 回调里设回地址。
    // 用回调而不是固定 setTimeout——前者是微信官方的「渲染完成」语义，
    // 后者在慢机器上可能赶不上一次渲染，等于没重试。
    // 重试的是**当前正在用的**地址：已经切到备用地址后再点「重新加载」，
    // 应该重试备用，而不是跳回刚失败过的主源（那样兜底永远兜不住）。
    onRetryMp4() {
      const url = (((this.data as any).mp4Src || '') || ((this.data as any).videoUrl || '')).trim()
      // 重建播放器时把旧高度一起撤掉：同一地址重播不该沿用上一次算出的尺寸，
      // 真机上 metadata 会再来一次并重算。
      this.setData({ mp4Error: false, mp4Src: '', mp4BoxStyle: '' }, () => {
        this.setData({ mp4Src: url })
      })
    },
  },
})
