// 富文本 + 视频块混排解析：把 <wx-video …> 拆成独立片段
// 渲染策略（对应后端约定）：视频片段用 <rich-video> 组件，其余用 <rich-text>
//
// type 属性决定块的三种来源（与后台富文本编辑器、company-video 五档口径对齐）：
//   （无 type / "channels"）视频号块——三属性分工（别再混用）：
//     feed-id     视频号「某一条视频」的唯一标识，视频号助手 → 动态管理复制。**仅同主体生效**。
//     feed-token  跨主体凭证。官方明确 feed-id 只支持同主体，视频号与小程序不同主体时必须用它
//                 （基础库 2.31.1+，非个人主体），代价是强制不能自动播放。
//     channel-id  视频号 ID，**sph 开头**（`gh_` 是公众号 ID，早期注释写错，勿沿用）。
//                 只能映射到 finder-user-name 展示视频号主页，播不了指定视频。
//     优先级 feed-id > feed-token > channel-id。后两者是兼容与跨主体通道，
//     直接丢弃存量内容里的 channel-id 会让这些片段整块消失。
//   type="tencent" 腾讯视频块——vid 是唯一取片凭证（v.qq.com 播放页 page/ 与 .html 之间那段）。
//     渲染走 txv-video 插件；插件未在 app.json 声明的部署上该块渲染不出来（与 company-video
//     的 tencent 档同一边界），所以编辑器侧就应劝阻未开启「腾讯视频档」的客户使用。
//   type="mp4" MP4 直链块——src 是可直接播放的 MP4 地址（上传到对象存储的或客户自备 CDN）。
//     正式版要求 src 的域名在小程序「业务域名」白名单内（与公司视频 url 档同一边界）。
export interface RichSegment {
  type: 'html' | 'video'
  // html 片段；video 段时为「展示用主标识」：
  // channels = feedId || feedToken || channelId，tencent = vid，mp4 = src（供旧调用方直接取值）
  content: string
  feedId?: string
  feedToken?: string
  channelId?: string
  videoType?: 'channels' | 'tencent' | 'mp4'
  vid?: string
  src?: string
}

// 匹配 <wx-video ...> 或 <wx-video ... />，成对写法时闭合标签一并消费，
// 避免 </wx-video> 漏进后续 html 片段被 rich-text 渲染成可见文本。
// 属性不再写死在正则里（原写法只认 channel-id），改为整段捕获后按名提取，顺序无关。
const VIDEO_RE = /<wx-video\s+([^>]*?)\/?>(\s*<\/wx-video>)?/g

// 从属性串里取指定属性值；不存在或值为空都返回空串
function attr(attrs: string, name: string): string {
  const m = new RegExp(name + '="([^"]*)"').exec(attrs)
  return m ? m[1] : ''
}

export function parseRich(html: string): RichSegment[] {
  const segs: RichSegment[] = []
  if (!html) return segs
  let last = 0
  let m: RegExpExecArray | null
  while ((m = VIDEO_RE.exec(html)) !== null) {
    // type 缺省按 channels：存量内容（约早于本注释的版本）没有 type 属性
    const rawType = (attr(m[1], 'type') || 'channels').toLowerCase()
    const videoType = rawType === 'tencent' || rawType === 'mp4' ? rawType : 'channels'
    const feedId = attr(m[1], 'feed-id')
    const feedToken = attr(m[1], 'feed-token')
    const channelId = attr(m[1], 'channel-id')
    const vid = attr(m[1], 'vid')
    const src = attr(m[1], 'src')
    // 各类型取片凭证齐全才构成视频块；否则标签是空的、渲染不出任何东西——
    // 当作普通文本留给 rich-text，否则会切出一个空白 video 段，把前后 html 无谓地割裂开。
    const playable =
      videoType === 'tencent' ? !!vid : videoType === 'mp4' ? !!src : !!(feedId || feedToken || channelId)
    if (!playable) continue
    if (m.index > last) {
      segs.push({ type: 'html', content: html.slice(last, m.index) })
    }
    segs.push({
      type: 'video',
      content:
        videoType === 'tencent' ? vid : videoType === 'mp4' ? src : feedId || feedToken || channelId,
      feedId,
      feedToken,
      channelId,
      videoType,
      vid,
      src,
    })
    last = VIDEO_RE.lastIndex
  }
  if (last < html.length) {
    segs.push({ type: 'html', content: html.slice(last) })
  }
  return segs
}

// 供 PC 管理端富文本编辑器插入视频号块时生成标准标记。
// channelId 可省略：只填 feed-id 就能播指定视频，视频号 ID 只是留档。
export function buildWxVideo(feedId: string, channelId = '', feedToken = ''): string {
  const attrs: string[] = []
  if (feedId) attrs.push(`feed-id="${feedId}"`)
  if (feedToken) attrs.push(`feed-token="${feedToken}"`)
  if (channelId) attrs.push(`channel-id="${channelId}"`)
  return `<wx-video ${attrs.join(' ')}></wx-video>`
}
