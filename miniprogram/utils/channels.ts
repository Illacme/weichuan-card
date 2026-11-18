// wx.openChannelsActivity 薄封装：跳转播放视频号视频。
//
// 首页公司视频（company-video）与富文本视频块（rich-video）都用它——两处各写一遍，
// 迟早会漂成「一处提示升级、另一处静默失败」，而静默失败正是这类跳转最常见、
// 也最难被发现的坏形态：封面/卡片点下去什么都不发生，用户以为页面卡了。
//
// 三条失败路径都必须给可读反馈：
//   1. 参数缺失（channelId = finderUserName，feedId）——官方接口两个都要，
//      缺一个就不发请求（必然失败，还浪费一次点击）；
//   2. 基础库 < 2.19.2 没有这个接口——低版本微信在真实用户里仍有存量，
//      不提示就是「怎么点都没反应」；
//   3. fail 回调（视频已删 / 设为私密 / feedId 不属于该视频号）——先打原始原因
//      便于现场排查，再给用户一句人话。
//
// 不计 video_play：跳转后播放发生在视频号里，我们观测不到。把「点了一下」记成播放
// 会让统计口径变浑（与 channel-video 内嵌档没有播放事件的处理一致）。

// 真机上 wx 由小程序框架注入；单测里用 vi.stubGlobal 顶替。
// 本项目 tsconfig 没引 wx 的 typings，就近声明一次（不污染全局）。
declare const wx: any

/**
 * 打开视频号视频页。
 *
 * @param channelId 视频号 ID（sph 开头），官方参数名 finderUserName
 * @param feedId    视频号视频 feedId（视频号助手 → 动态管理复制）
 * @param tag       日志前缀，区分是哪个组件调用的（如 'company-video'）
 * @returns true=已发起跳转；false=没发起（参数缺失 / 基础库不支持）
 */
export function openChannelsVideo(channelId: string, feedId: string, tag = 'channels'): boolean {
  const cid = (channelId || '').trim()
  const fid = (feedId || '').trim()
  if (!cid || !fid) {
    console.warn(`[${tag}] 跳转播放缺少 channelId 或 feedId`, { channelId: cid, feedId: fid })
    wx.showToast({ title: '视频号配置不完整，请联系管理员', icon: 'none' })
    return false
  }
  if (typeof wx.openChannelsActivity !== 'function') {
    console.warn(`[${tag}] 当前基础库不支持 wx.openChannelsActivity`)
    wx.showToast({ title: '微信版本过低，请升级后观看', icon: 'none' })
    return false
  }
  wx.openChannelsActivity({
    finderUserName: cid,
    feedId: fid,
    fail: (err: any) => {
      console.warn(`[${tag}] 打开视频号视频失败`, err)
      wx.showToast({ title: '暂时无法打开，请稍后再试', icon: 'none' })
    },
  })
  return true
}
