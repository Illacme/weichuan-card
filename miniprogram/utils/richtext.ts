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

// 图片宽度收口。
//
// 为什么必须在数据侧做、而不是写一条 wxss：`<rich-text nodes="…">` 里的节点**不是真实
// DOM**，微信把它按节点树自绘，外部样式表命中不了内部节点——只有节点自带的内联样式生效。
// 而编辑器（wangEditor 图片菜单 / 粘贴外链图）插进来的 `<img>` 只带原始像素尺寸，
// 一张 1200px 宽的图会把详情页撑出横向滚动条，用户看到的是一半内容加一条横滚条。
//
// 约束放在「渲染」这一步、而不是编辑器保存时，有两个不能绕开的理由：
//   1）存量内容（历史外链图、粘贴进来的图）也一起受益，不必回写数据库；
//   2）wangEditor 的图片节点**只序列化 width / height**（basic-modules 的 image
//      elemToHtml 硬编码了这两个键），max-width 之类写进节点会在下次保存时被丢掉——
//      也就是说「存进库里的 max-width」随时会被下一个编辑者抹掉，靠不住。
//      渲染侧补一次是幂等的，且永远成立。
const IMG_TAG_RE = /<img\b[^>]*>/gi
const IMG_HAS_MAX_WIDTH_RE = /max-width\s*:/i
const IMG_STYLE_ATTR_RE = /\sstyle\s*=\s*("([^"]*)"|'([^']*)')/i
const IMG_STYLE = 'max-width:100%;height:auto'

export function constrainImages(html: string): string {
  // 没有 <img> 就直接返回原串：绝大多数段落走这条路径，连正则都不跑
  if (!html || html.indexOf('<img') === -1) return html
  return html.replace(IMG_TAG_RE, (tag) => {
    // 幂等：已有 max-width（客户自己写了宽度，或第二次经过这里）原样放过
    if (IMG_HAS_MAX_WIDTH_RE.test(tag)) return tag
    const m = IMG_STYLE_ATTR_RE.exec(tag)
    if (m) {
      const quoted = m[1] // 含引号的原始值，如 "color:red"
      const quote = quoted[0]
      const cur = (m[2] !== undefined ? m[2] : m[3]).trim()
      const merged = cur ? (cur.endsWith(';') ? cur : cur + ';') + IMG_STYLE : IMG_STYLE
      // 用切片拼接而不是 String.replace：样式串里可能有 `$`（如 `$1`），
      // 会被当成替换模式而吞掉内容。
      return tag.slice(0, m.index) + ` style=${quote}${merged}${quote}` + tag.slice(m.index + m[0].length)
    }
    // 无 style：插在结束符之前（自闭合 `<img …/>` 与普通 `<img …>` 都可能出现）
    return tag.replace(/\s*\/?>$/, (end) => ` style="${IMG_STYLE}"${end}`)
  })
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
      segs.push({ type: 'html', content: constrainImages(html.slice(last, m.index)) })
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
    segs.push({ type: 'html', content: constrainImages(html.slice(last)) })
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
