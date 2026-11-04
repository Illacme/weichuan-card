// 小程序端富文本解析纯逻辑测试。
// 说明：这些是纯 TS 函数（无 wx / DOM 依赖），挂在 frontend 的 vitest 下统一运行，
// 避免为 miniprogram 再引入一套 Node 工具链（小程序包保持零依赖，见 miniprogram/README）。
import { describe, it, expect } from 'vitest'
import { parseRich, buildWxVideo, constrainImages, type RichSegment } from '../utils/richtext'

describe('parseRich', () => {
  it('空输入返回空数组', () => {
    expect(parseRich('')).toEqual([])
  })

  it('无视频号块时整体作为单个 html 片段', () => {
    const segs = parseRich('<p>hello</p>')
    expect(segs).toEqual<RichSegment[]>([{ type: 'html', content: '<p>hello</p>' }])
  })

  it('新格式（feed-id）被拆成独立 video 片段，前后 html 保留', () => {
    const segs = parseRich('<p>a</p><wx-video feed-id="feed_1"></wx-video><p>b</p>')
    expect(segs).toEqual<RichSegment[]>([
      { type: 'html', content: '<p>a</p>' },
      {
        type: 'video',
        content: 'feed_1',
        feedId: 'feed_1',
        feedToken: '',
        channelId: '',
        videoType: 'channels',
        vid: '',
        src: '',
      },
      { type: 'html', content: '<p>b</p>' },
    ])
  })

  it('跨主体格式（feed-token）被拆成独立 video 片段', () => {
    const segs = parseRich('<wx-video feed-token="tok_1"></wx-video>')
    expect(segs).toEqual<RichSegment[]>([
      {
        type: 'video',
        content: 'tok_1',
        feedId: '',
        feedToken: 'tok_1',
        channelId: '',
        videoType: 'channels',
        vid: '',
        src: '',
      },
    ])
  })

  it('存量格式（只有 channel-id）仍能被解析，回退旧行为而不是整块消失', () => {
    const segs = parseRich('<wx-video channel-id="sph_1"></wx-video>')
    expect(segs).toEqual<RichSegment[]>([
      {
        type: 'video',
        content: 'sph_1',
        feedId: '',
        feedToken: '',
        channelId: 'sph_1',
        videoType: 'channels',
        vid: '',
        src: '',
      },
    ])
  })

  it('type="tencent"：vid 作为主标识与取片凭证', () => {
    const segs = parseRich('<p>a</p><wx-video type="tencent" vid="v_abc"></wx-video><p>b</p>')
    expect(segs).toEqual<RichSegment[]>([
      { type: 'html', content: '<p>a</p>' },
      {
        type: 'video',
        content: 'v_abc',
        feedId: '',
        feedToken: '',
        channelId: '',
        videoType: 'tencent',
        vid: 'v_abc',
        src: '',
      },
      { type: 'html', content: '<p>b</p>' },
    ])
  })

  it('type="mp4"：src 作为主标识与播放地址', () => {
    const segs = parseRich('<wx-video type="mp4" src="https://cdn.example.com/v.mp4"></wx-video>')
    expect(segs).toEqual<RichSegment[]>([
      {
        type: 'video',
        content: 'https://cdn.example.com/v.mp4',
        feedId: '',
        feedToken: '',
        channelId: '',
        videoType: 'mp4',
        vid: '',
        src: 'https://cdn.example.com/v.mp4',
      },
    ])
  })

  it('tencent 缺 vid / mp4 缺 src 时不拆分（凭证不全渲染不出任何东西）', () => {
    // 两种空块都留给 rich-text 当文本，避免切出空白 video 段割裂前后 html
    expect(parseRich('<wx-video type="tencent"></wx-video>')).toEqual<RichSegment[]>([
      { type: 'html', content: '<wx-video type="tencent"></wx-video>' },
    ])
    expect(parseRich('<wx-video type="mp4" src=""></wx-video>')).toEqual<RichSegment[]>([
      { type: 'html', content: '<wx-video type="mp4" src=""></wx-video>' },
    ])
  })

  it('未知 type 值按 channels 处理（向前兼容不炸存量）', () => {
    const segs = parseRich('<wx-video type="future" feed-id="f_1"></wx-video>')
    expect(segs).toEqual<RichSegment[]>([
      {
        type: 'video',
        content: 'f_1',
        feedId: 'f_1',
        feedToken: '',
        channelId: '',
        videoType: 'channels',
        vid: '',
        src: '',
      },
    ])
  })

  it('三种来源混排互不干扰', () => {
    const segs = parseRich(
      'a<wx-video feed-id="f_a"></wx-video>b' +
        '<wx-video type="tencent" vid="v_b"></wx-video>c' +
        '<wx-video type="mp4" src="https://x/v.mp4"></wx-video>d'
    )
    expect(segs.map((s) => s.type)).toEqual([
      'html',
      'video',
      'html',
      'video',
      'html',
      'video',
      'html',
    ])
    expect(segs.map((s) => s.content)).toEqual([
      'a',
      'f_a',
      'b',
      'v_b',
      'c',
      'https://x/v.mp4',
      'd',
    ])
    expect(segs[1].videoType).toBe('channels')
    expect(segs[3].videoType).toBe('tencent')
    expect(segs[5].videoType).toBe('mp4')
  })

  it('优先级 feed-id > feed-token > channel-id', () => {
    const all = parseRich(
      '<wx-video channel-id="sph_1" feed-token="tok_1" feed-id="feed_1"></wx-video>'
    )
    expect(all[0].content).toBe('feed_1')
    // 没有 feed-id 时轮到 feed-token
    const noFeed = parseRich('<wx-video channel-id="sph_1" feed-token="tok_1"></wx-video>')
    expect(noFeed[0].content).toBe('tok_1')
  })

  it('兼容自闭合写法且属性顺序无关', () => {
    const segs = parseRich('<wx-video data-x="1" feed-id="feed_2" />')
    expect(segs).toEqual<RichSegment[]>([
      {
        type: 'video',
        content: 'feed_2',
        feedId: 'feed_2',
        feedToken: '',
        channelId: '',
        videoType: 'channels',
        vid: '',
        src: '',
      },
    ])
  })

  it('支持多个视频号块', () => {
    const segs = parseRich(
      'a<wx-video feed-id="f_a"></wx-video>b<wx-video feed-token="t_b"></wx-video>c'
    )
    expect(segs.map((s) => s.type)).toEqual(['html', 'video', 'html', 'video', 'html'])
    expect(segs.map((s) => s.content)).toEqual(['a', 'f_a', 'b', 't_b', 'c'])
  })

  it('不含任何有效属性的 wx-video 不拆分为 video 片段', () => {
    const segs = parseRich('<wx-video></wx-video>')
    expect(segs).toEqual<RichSegment[]>([{ type: 'html', content: '<wx-video></wx-video>' }])
  })

  it('属性存在但值为空同样不拆分（避免切出空白 video 段割裂前后 html）', () => {
    const segs = parseRich('<wx-video feed-id="" feed-token=""></wx-video>')
    expect(segs).toEqual<RichSegment[]>([
      { type: 'html', content: '<wx-video feed-id="" feed-token=""></wx-video>' },
    ])
  })

  it('连续调用互不影响（全局正则 lastIndex 状态安全）', () => {
    const html = '<wx-video feed-id="f_1"></wx-video>tail'
    expect(parseRich(html)).toEqual(parseRich(html))
    expect(parseRich(html).length).toBe(2)
  })
})

describe('constrainImages（图片宽度收口）', () => {
  // 背景：<rich-text> 按节点树自绘，**外部 wxss 命中不了内部节点**，只有内联样式生效。
  // 不补这一步，手机上一张 1200px 宽的图会把详情页撑出横向滚动条。

  it('无 style 的 img 补上内联宽度约束', () => {
    expect(constrainImages('<p><img src="a.png"></p>')).toBe(
      `<p><img src="a.png" style="max-width:100%;height:auto"></p>`
    )
  })

  it('自闭合写法同样补，且保留 />', () => {
    expect(constrainImages('<img src="a.png" />')).toBe(
      `<img src="a.png" style="max-width:100%;height:auto" />`
    )
  })

  it('已有 style：合并而不是覆盖（客户自己设的样式要留住）', () => {
    expect(constrainImages('<img src="a.png" style="border:1px solid red">')).toBe(
      `<img src="a.png" style="border:1px solid red;max-width:100%;height:auto">`
    )
  })

  it('原有 style 末尾已带分号时不重复加分号', () => {
    expect(constrainImages(`<img src="a.png" style="border:0;">`)).toBe(
      `<img src="a.png" style="border:0;max-width:100%;height:auto">`
    )
  })

  it('空 style（wangEditor 序列化出的 style=""）也补上约束', () => {
    expect(constrainImages('<img src="a.png" alt="p.png" data-href="" style=""/>')).toBe(
      `<img src="a.png" alt="p.png" data-href="" style="max-width:100%;height:auto"/>`
    )
  })

  it('已有 width/height（编辑器里缩放过）：追加 max-width 并让 height 自适应', () => {
    // 后面的 height:auto 覆盖前面的 height:400px（CSS 同优先级后者胜），宽高比不丢
    expect(constrainImages('<img src="a.png" style="width: 300px;height: 400px;"/>')).toBe(
      `<img src="a.png" style="width: 300px;height: 400px;max-width:100%;height:auto"/>`
    )
  })

  it('已经带 max-width 的原样返回（幂等，反复保存不叠加）', () => {
    const tag = '<img src="a.png" style="max-width:80%">'
    expect(constrainImages(tag)).toBe(tag)
    expect(constrainImages(constrainImages(tag))).toBe(tag)
  })

  it('样式值里含 $ 不会被当成替换模式吃掉', () => {
    // String.replace 的第二参数里 $ 有特殊含义，拼接实现必须避开
    expect(constrainImages('<img src="a.png" style="background:url(x$1.png)">')).toBe(
      `<img src="a.png" style="background:url(x$1.png);max-width:100%;height:auto">`
    )
  })

  it('无 img 时原样返回（含无 img 的空串）', () => {
    expect(constrainImages('<p>纯文字</p>')).toBe('<p>纯文字</p>')
    expect(constrainImages('')).toBe('')
  })

  it('parseRich 输出已收口：图片在 html 片段里带上约束', () => {
    const segs = parseRich('<p><img src="a.png"></p><wx-video feed-id="f_1"></wx-video>')
    expect(segs[0].content).toBe('<p><img src="a.png" style="max-width:100%;height:auto"></p>')
    // 视频片段不受影响（它走 <rich-video> 组件，尺寸由组件自己管）
    expect(segs[1].type).toBe('video')
  })
})

describe('buildWxVideo', () => {
  it('只传 feedId 时只输出 feed-id 属性', () => {
    expect(buildWxVideo('f_abc')).toBe('<wx-video feed-id="f_abc"></wx-video>')
  })

  it('传了视频号 ID 时按 feed-id / channel-id 顺序输出', () => {
    expect(buildWxVideo('f_abc', 'sph_1')).toBe(
      '<wx-video feed-id="f_abc" channel-id="sph_1"></wx-video>'
    )
  })

  it('跨主体：只传 feedToken 时只输出 feed-token 属性', () => {
    expect(buildWxVideo('', '', 'tok_1')).toBe('<wx-video feed-token="tok_1"></wx-video>')
  })

  it('buildWxVideo 的产出可被 parseRich 还原', () => {
    const segs = parseRich(buildWxVideo('f_roundtrip'))
    expect(segs).toEqual<RichSegment[]>([
      {
        type: 'video',
        content: 'f_roundtrip',
        feedId: 'f_roundtrip',
        feedToken: '',
        channelId: '',
        videoType: 'channels',
        vid: '',
        src: '',
      },
    ])
  })
})
