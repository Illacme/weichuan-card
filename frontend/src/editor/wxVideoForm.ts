/**
 * 视频块表单取值与校验（纯函数）。
 *
 * 为什么单独抽出来：`confirmVideo` 里「先校验跳转先决条件、再校验 feedId/feed-token 二选一」
 * 这个顺序决定用户看到哪条提示（两种顺序都会拦住非法块，但不是所有提示都同样有用），
 * 且「切来源清空其它来源凭证」「脏 playMode 回落 embed」都是取值层面的约定。
 * 这些约定用源码扫描钉不住（把两个 if 换个位置，字符串一个不少），
 * 所以做成可被单测直接调用的纯函数。
 *
 * 三个来源共用同一个 <wx-video> 块；切换来源时必须把其它来源的凭证清空，
 * 否则会留下 `type="mp4"` 却带 `feed-id` 的混搭脏数据。
 */

export type VideoKind = 'channels' | 'tencent' | 'mp4'

/**
 * 视频号档的播放方式：
 *   embed     <channel-video> 原位内嵌（缺省，历史行为）
 *   redirect  wx.openChannelsActivity 跳转打开视频号视频页（无主体限制，但离开小程序）
 */
export type PlayMode = 'embed' | 'redirect'

export interface VideoValues {
  videoType: VideoKind
  feedId: string
  feedToken: string
  channelId: string
  playMode: PlayMode
  vid: string
  src: string
}

/** 各来源的「空值基线」：所有凭证都为空，播放方式取缺省的 embed。 */
export function emptyVideoValues(): VideoValues {
  return {
    videoType: 'channels',
    feedId: '',
    feedToken: '',
    channelId: '',
    playMode: 'embed',
    vid: '',
    src: '',
  }
}

/**
 * 按视频号档归一化：三个凭证 trim 后写入，其余来源的字段一律清空。
 * playMode 只认 'redirect' 这一个非默认值——脏值回落 embed，与后端
 * NormalizeVideoChannelPlayMode / 小程序端解析同一口径。
 */
export function channelsVideoValues(input: {
  feedId: string
  feedToken: string
  channelId: string
  playMode: string
}): VideoValues {
  return {
    ...emptyVideoValues(),
    videoType: 'channels',
    feedId: (input.feedId || '').trim(),
    feedToken: (input.feedToken || '').trim(),
    channelId: (input.channelId || '').trim(),
    playMode: input.playMode === 'redirect' ? 'redirect' : 'embed',
  }
}

/**
 * 视频号档的保存前校验：返回空串表示通过，否则返回给用户看的提示。
 *
 * 顺序是刻意的：跳转的先决条件（channelId + feedId）排在「feedId / feed-token 二选一」之前。
 * 两种顺序都能拦住非法配置（跳转那一条单独也能拦），差别在**说给用户听的话**：
 * wx.openChannelsActivity 只接 finderUserName + feedId，feed-token 在那个接口里没有位置，
 * 所以「选了跳转但两个都没有」时该说「跳转需要视频号 ID 与 feedId」，
 * 而不是先冒一条「填 feedId 或 feed-token」——后者会让人以为填 token 就能跳转。
 */
export function channelsVideoWarning(v: VideoValues): string {
  if (v.playMode === 'redirect') {
    if (!v.channelId && !v.feedId) return '跳转播放需要同时填写视频号 ID 与 feedId'
    if (!v.channelId) return '跳转播放需要填写视频号 ID（finderUserName）'
    if (!v.feedId) return '跳转播放需要填写 feedId'
  }
  if (!v.feedId && !v.feedToken) return '请填写 feedId（同主体）或 feed-token（跨主体）'
  return ''
}
