// 富文本视频块：按块的 type 属性选源渲染（与 company-video 的多档思路一致，
// 但这里档位写在内容里——每个块自带 type，不由后台配置统一决定）。
//
// 三种 type 的渲染分支：
//   channels（缺省，存量内容无 type）  → channel-video，feed-id > feed-token > channel-id
//   tencent                           → txv-video 插件（vid 取片，原位播放）
//   mp4                               → 标准 video（src 直链）
//
// 画幅（2026-11-06）：只有 mp4 档是原生 <video>，能拿 loadedmetadata；
// 与首页 company-video 共用 utils/mp4box.ts 的等比高度算法，覆盖 WXSS 的
// 360rpx 固定高度后再用 contain 完整呈现（既不裁切也不留黑边）。
// txv-video / channel-video 是插件组件，没有 loadedmetadata，各自保持原样——
// 不为「三档一致」改变插件行为。富文本一页可能有多个块，所以每个实例自己
// 量自己的播放器宽度，换块时清掉上一段视频算出的高度。
//
// 边界（与 company-video 同源，此处不重复兜底）：
//   - tencent 档依赖 app.json 声明插件 + MP 后台授权；未声明的部署上 txv-video
//     是未注册组件、渲染不出来——编辑器侧应提示客户「部署未开启腾讯视频档时勿用」；
//   - mp4 档的 src 域名在正式版需加入小程序「业务域名」白名单；
//   - channel-video 官方只开放 binderror，无播放事件——视频号块的播放统计缺失
//     是官方组件的边界（company-video 文件头有同款说明）。
//
// 播放埋点（video_play）覆盖 mp4 与 tencent 两档（video/txv-video 都有 bindplay）：
// video_play 在统计口径里属于「内容浏览」组，富文本块挂在案例/服务详情页，
// 归属 staffId 由页面透传。组件实例级去重：暂停恢复再播不重复计。
import { track } from '../../utils/track'
import { mp4BoxStyleFor } from '../../utils/mp4box'

// 真机上 wx 由小程序框架注入；单测里用 vi.stubGlobal 顶替。
// 本项目 tsconfig 没引 wx 的 typings，就近声明一次（不污染全局）。
declare const wx: any

// 腾讯视频插件要求 playerid 在同一页面内全局唯一。富文本一页可能有多个块，
// 写死 richVideo 会让多个播放器互相串（README 记过这个隐患）。模块级自增在
// 整个小程序生命周期内单调递增，比随机数可靠，也不依赖调用方传 index。
let playerSeq = 0

Component({
  options: { addGlobalClass: true },
  properties: {
    // 块类型：'channels'（缺省，存量内容无 type）| 'tencent' | 'mp4'
    videoType: String,
    // ---- channels 三件套（优先级 feedId > feedToken > channelId）----
    // 同主体：视频号视频 feedId（优先，可自动播放）
    feedId: String,
    // 跨主体：feed-token（feedId 为空时启用，强制不能自动播放）
    feedToken: String,
    // 视频号 ID（sph 开头）：前两者都缺失时的兼容回退，只展示视频号主页
    channelId: String,
    // ---- tencent：腾讯视频 vid（插件取片唯一凭证）----
    vid: String,
    // ---- mp4：直链地址 ----
    src: String,
    // 埋点归属：video_play 计到哪位员工的名片账上（详情页透传）
    staffId: Number,
  },
  data: {
    // 播放埋点去重标记：同一组件实例只报一次（bindplay 在「暂停后继续播放」时会再触发）
    playTracked: false,
    // 首次渲染给个非空保底值（与旧行为一致），attached 后立即换成实例唯一的 id。
    // 那时 autoplay=false、用户还没点播放，属性变化不会打断观看。
    playerId: 'richVideo',
    // 行内高度：拿到 mp4 源文件宽高后按实测容器宽度算出，覆盖 WXSS 的 360rpx。
    // 空串 = 维持默认高度（最多留黑边，绝不裁切）。
    mp4BoxStyle: '',
  },
  lifetimes: {
    attached() {
      this.setData({ playerId: `richVideo_${++playerSeq}` })
    },
  },
  observers: {
    // 换块（详情页复用组件实例切内容）时重置去重：换了视频应允许再计一次。
    // 同时清掉上一段视频算出的画幅高度，等新视频的 metadata 重算。
    'videoType, vid, src, feedId, feedToken, channelId': function () {
      this.setData({ playTracked: false, mp4BoxStyle: '' })
    },
  },
  methods: {
    // video_play 埋点（mp4 / tencent 档；channels 档无播放事件，见文件头）
    onVideoPlay() {
      if (this.data.playTracked) return
      this.setData({ playTracked: true })
      track('video_play', this.data.staffId || 0)
    },
    // 按视频源文件分辨率调整播放窗口（与首页 company-video 同源）。
    // loadedmetadata 带 {width,height,duration}（基础库 2.7.0+），这是拿到
    // 真实宽高的唯一途径；容器宽度要现场量——详情页有左右内边距，不同机型
    // 宽度不同，用 750rpx 硬算会偏高，黑边又回来了。
    onVideoMeta(e?: { detail?: { width?: number; height?: number } }) {
      const d = (e && e.detail) || {}
      const w = Number(d.width) || 0
      const h = Number(d.height) || 0
      if (!(w > 0) || !(h > 0)) return
      const that = this
      try {
        wx.createSelectorQuery()
          .in(this)
          .select('.rich-video')
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
        console.warn('[rich-video] 读取播放器宽度失败，保持默认画幅', err)
      }
    },
    // txv-video / video 的报错打出来：体验版开调试即可见，别让「块不显示」无从排查
    onVideoError(e?: { detail?: Record<string, unknown> }) {
      console.warn('[rich-video] 视频块报错', (e && e.detail) || e || '')
    },
  },
})
