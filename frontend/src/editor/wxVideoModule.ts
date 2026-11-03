import {
  Boot,
  SlateTransforms,
  type IDomEditor,
  type IModuleConf,
  type SlateElement,
} from '@wangeditor/editor'
import { h } from 'snabbdom'

/**
 * 视频块自定义元素：让 wangEditor 识别并序列化
 * `<wx-video ...></wx-video>`。
 * 该标签与小程序端 rich-video 组件约定一致（见 miniprogram/utils/richtext.ts 的类型说明）。
 *
 * type 属性决定三种来源（缺省 channels，兼容存量视频号内容）：
 *
 * channels（视频号，缺省）三属性的分工（渲染优先级从高到低）：
 *   feed-id     某一条视频的标识，有它才能播指定视频（视频号助手 → 动态管理复制），**仅同主体生效**
 *   feed-token  跨主体凭证。视频号与小程序不同主体时 feed-id 无效，必须用它（基础库 2.31.1+，
 *               非个人主体），代价是强制不能自动播放
 *   channel-id  视频号 ID（**sph 开头**），只能展示视频号主页；为兼容存量内容保留读写
 *
 * tencent（腾讯视频）：
 *   vid         腾讯视频取片凭证（v.qq.com 播放页 page/ 与 .html 之间那段）。
 *               渲染走 txv-video 插件——部署未开启「腾讯视频档」时块渲染不出来，
 *               编辑弹窗会提示；插件已声明并授权的部署才能用。
 *
 * mp4（直链）：
 *   src         可直接播放的 MP4 地址（对象存储上传或客户自备 CDN）。
 *               正式版要求 src 域名在小程序「业务域名」白名单内。
 *
 * 必须在创建编辑器之前注册，且全局只注册一次。
 */
export const WX_VIDEO_TYPE = 'wxVideo'

// 空段落的序列化结果（getHtml 里用来剥掉「仅为光标落点」而补的尾巴）
const EMPTY_P_HTML = '<p><br></p>'

let registered = false

function isWxVideo(node: any): boolean {
  return !!node && node.type === WX_VIDEO_TYPE
}

function isEmptyParagraph(node: any): boolean {
  if (!node || node.type !== 'paragraph') return false
  const texts = node.children || []
  return texts.length <= 1 && !String(texts[0]?.text ?? '')
}

export function registerWxVideoModule() {
  if (registered) return
  registered = true

  // 标记为 void（原子块，内部不可编辑）。
  // 泛型 <T extends IDomEditor> 与 Boot 的 PluginType 签名保持一致（否则 TS 推断不收敛）。
  const withWxVideo = <T extends IDomEditor>(editor: T): T => {
    const { isVoid, normalizeNode, getHtml } = editor

    editor.isVoid = (elem: SlateElement) => {
      const e = elem as any
      if (e.type === WX_VIDEO_TYPE) return true
      return isVoid(elem)
    }

    // 视频号块是 void 块：内部没有可编辑的文本节点。当它位于文档末尾时，slate
    // 找不到「块之后」的落点，表现为**光标无法定位到视频模块后面、无法在后面继续输入**。
    // 这里在它后面补一个空段落，给光标一个真实的落脚点。
    //
    // 为什么写在 normalizeNode 而不是只在插入时补一次：
    //  · wangEditor 初始化内容时会调 DomEditor.normalizeContent()，逐个对**顶层节点**
    //    调用 normalizeNode —— 所以「打开已有内容」也会补上，存量数据同样受益；
    //  · normalize 在每次 operation 后都会跑，用户把补出来的空段落删掉后会自动再补，
    //    不会出现「删了就再也点不到后面」的死角。
    editor.normalizeNode = (entry: any) => {
      const [node, path] = entry || []
      if (
        isWxVideo(node) &&
        Array.isArray(path) &&
        path.length === 1 &&
        path[0] === (editor.children || []).length - 1
      ) {
        SlateTransforms.insertNodes(
          editor,
          { type: 'paragraph', children: [{ text: '' }] } as any,
          { at: [path[0] + 1] }
        )
        return
      }
      normalizeNode(entry)
    }

    // 上面补的空段落只是编辑器的「光标落点」，不该进数据库、也不该渲染到小程序端
    // （会在详情页末尾多出一行空白）。序列化时剥掉。
    // 只剥「紧跟在视频号块后面的空段落」，避免吃掉用户自己敲出来的空行。
    editor.getHtml = (): string => {
      const html = getHtml()
      const children = (editor.children || []) as any[]
      if (children.length < 2) return html
      if (!isWxVideo(children[children.length - 2])) return html
      if (!isEmptyParagraph(children[children.length - 1])) return html
      return html.endsWith(EMPTY_P_HTML) ? html.slice(0, html.length - EMPTY_P_HTML.length) : html
    }

    return editor
  }

  // 编辑区渲染：显示为一块绿色占位（视频号图标 + 标识）。
  // ⚠️ 关键修复：renderElem 必须返回 snabbdom VNode（与 wangEditor 内部同版本实例），
  // 此前返回原生 document.createElement 节点会导致视图层把它当 VNode 子节点处理，
  // 触发 `o2.forEach is not a function` + `Unable to find a host window element`，
  // 在 Vue dev 模式下被重抛从而冻结整个父组件的响应式更新。
  // 这里用 wangEditor 已依赖的 snabbdom（^3.1.0，与本项目安装版本一致）的 h 构造 VNode，
  // 并通过 props / dataset 适配 wangEditor 注册的 snabbdom 模块。
  const renderElemConf = {
    type: WX_VIDEO_TYPE,
    renderElem: (elem: any) => {
      const videoType: string = elem.videoType || 'channels'
      const feedId = elem.feedId || ''
      const feedToken = elem.feedToken || ''
      const channelId = elem.channelId || ''
      const vid = elem.vid || ''
      const src = elem.src || ''
      // 各类型的主标识展示；feed-token 往往很长，截断显示避免撑破占位块。
      let kind = '▶ 视频号'
      let label = ''
      if (videoType === 'tencent') {
        kind = '▶ 腾讯视频'
        label = vid
      } else if (videoType === 'mp4') {
        kind = '▶ 视频'
        label = src
      } else {
        // 展示优先级与渲染一致：feed-id（同主体）> feed-token（跨主体）> channel-id（仅主页）。
        label = feedId ? feedId : feedToken ? `跨主体 ${feedToken.slice(0, 10)}…` : channelId ? `视频号主页 ${channelId}` : '未设置'
      }
      // MP4 地址可能很长，截断显示
      if (videoType === 'mp4' && label.length > 40) label = label.slice(0, 40) + '…'
      return h(
        'div',
        {
          props: { contentEditable: false, className: 'wx-video-block' },
          // 编辑弹窗靠 data-* 回填表单。注意：不要写 elem.id——wangEditor 的 slate 节点
          // 没有 id 属性（getElemsByType 返回的 id 是 findKey 动态包装的），elem.id 恒为
          // undefined，靠它定位节点会静默失败。
          dataset: {
            wEType: WX_VIDEO_TYPE,
            videoType,
            feedId,
            feedToken,
            channelId,
            vid,
            src,
          },
        },
        [
          h('span', { props: { className: 'wx-video-ic' } }, kind),
          h('span', { props: { className: 'wx-video-id' } }, label),
        ]
      )
    },
  }

  // 序列化为 <wx-video ...></wx-video>（小程序端可解析）。
  // 缺失的属性不输出，避免写出 feed-id="" 这种空属性。
  // type 只在非视频号档输出：存量内容（与小程序旧解析）没有 type 属性，
  // 缺省即 channels——输出 type="channels" 对它们无害，但保持「缺省即存量形态」
  // 能让新旧两端各自独立演进时互不拖累。
  const elemToHtmlConf = {
    type: WX_VIDEO_TYPE,
    elemToHtml: (elem: any) => {
      const attrs: string[] = []
      if (elem.videoType === 'tencent' || elem.videoType === 'mp4') attrs.push(`type="${elem.videoType}"`)
      if (elem.videoType === 'tencent') {
        if (elem.vid) attrs.push(`vid="${elem.vid}"`)
      } else if (elem.videoType === 'mp4') {
        if (elem.src) attrs.push(`src="${elem.src}"`)
      } else {
        if (elem.feedId) attrs.push(`feed-id="${elem.feedId}"`)
        if (elem.feedToken) attrs.push(`feed-token="${elem.feedToken}"`)
        if (elem.channelId) attrs.push(`channel-id="${elem.channelId}"`)
      }
      return `<wx-video ${attrs.join(' ')}></wx-video>`
    },
  }

  // 解析：<wx-video ...> 还原为元素节点。type 缺省按 channels（存量内容无 type）；
  // 各类型的属性全量读入，不因 type 丢弃——手工改过 HTML 的块属性更可抗。
  const parseHtmlConf = {
    selector: 'wx-video',
    parseElemHtml: (dom: any) => {
      const t = (dom.getAttribute('type') || '').toLowerCase()
      return {
        type: WX_VIDEO_TYPE,
        videoType: t === 'tencent' || t === 'mp4' ? t : 'channels',
        feedId: dom.getAttribute('feed-id') || '',
        feedToken: dom.getAttribute('feed-token') || '',
        channelId: dom.getAttribute('channel-id') || '',
        vid: dom.getAttribute('vid') || '',
        src: dom.getAttribute('src') || '',
        children: [{ text: '' }],
      }
    },
  }

  // 装配：上游 conf 类型签名（snabbdom VNode）与运行时可接受的返回值一致；
  // 仅 renderElem 的入参形态按 wangEditor 约定的对象字面量声明，装配时统一断言收敛。
  const module: Partial<IModuleConf> = {
    editorPlugin: withWxVideo,
    renderElems: [renderElemConf as any],
    elemsToHtml: [elemToHtmlConf as any],
    parseElemsHtml: [parseHtmlConf as any],
  }

  Boot.registerModule(module)
}
