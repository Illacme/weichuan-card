import { describe, it, expect } from 'vitest'
import {
  channelsVideoValues,
  channelsVideoWarning,
  emptyVideoValues,
  type VideoValues,
} from './wxVideoForm'

// 视频块表单的归一 / 校验是「行为」而不是「文案」：
// 这里钉的是三个真会出事的点——
//   1. 只有 redirect 才算跳转，脏值回落 embed（端上只认这一个非默认值）；
//   2. 切来源时把别的来源的凭证清空，别留 type="mp4" 却带 feed-id 的混搭脏数据；
//   3. 跳转先决条件排在「feedId / feed-token 二选一」**之前**：
//      选了跳转却只填 feed-token 时必须被拦下，而不是因为满足二选一就放行。
describe('wxVideoForm 视频块表单', () => {
  const values = (patch: Partial<VideoValues> = {}): VideoValues => ({
    ...emptyVideoValues(),
    ...patch,
  })

  describe('channelsVideoValues', () => {
    it('三个凭证 trim 后写入，缺省方式为内嵌播放', () => {
      const v = channelsVideoValues({
        feedId: '  f_1 ',
        feedToken: ' tok_1 ',
        channelId: ' sph_1 ',
        playMode: '',
      })

      expect(v.feedId).toBe('f_1')
      expect(v.feedToken).toBe('tok_1')
      expect(v.channelId).toBe('sph_1')
      expect(v.playMode).toBe('embed')
    })

    it('只认 redirect，其余（含脏值）一律回落 embed', () => {
      expect(
        channelsVideoValues({ feedId: '', feedToken: '', channelId: '', playMode: 'redirect' })
          .playMode
      ).toBe('redirect')
      expect(
        channelsVideoValues({ feedId: '', feedToken: '', channelId: '', playMode: 'REDIRECT' })
          .playMode
      ).toBe('embed')
      expect(
        channelsVideoValues({ feedId: '', feedToken: '', channelId: '', playMode: 'future' })
          .playMode
      ).toBe('embed')
    })

    it('切到视频号档时清空腾讯视频 / MP4 的凭证', () => {
      const v = channelsVideoValues({
        feedId: 'f_1',
        feedToken: '',
        channelId: '',
        playMode: 'embed',
      })
      // 归一结果里不该带出任何其它来源的字段值（值集由函数重新构造，不继承入参）
      expect(v.videoType).toBe('channels')
      expect(v.vid).toBe('')
      expect(v.src).toBe('')
    })
  })

  describe('channelsVideoWarning', () => {
    it('内嵌 + feedId：通过', () => {
      expect(channelsVideoWarning(values({ feedId: 'f_1' }))).toBe('')
    })

    it('内嵌 + 只有 feed-token：通过（跨主体靠它播）', () => {
      expect(channelsVideoWarning(values({ feedToken: 'tok_1' }))).toBe('')
    })

    it('内嵌 + 两个凭证都没有：提示二选一', () => {
      expect(channelsVideoWarning(values())).toBe(
        '请填写 feedId（同主体）或 feed-token（跨主体）'
      )
    })

    it('跳转 + 视频号 ID 与 feedId 齐全：通过', () => {
      expect(
        channelsVideoWarning(
          values({ playMode: 'redirect', channelId: 'sph_1', feedId: 'f_1' })
        )
      ).toBe('')
    })

    // ↓ 顺序钉死：二选一若排在前面，这条会返回空串（因为 feed-token 让二选一成立），
    // 于是「跳转但缺 feedId」的块能存下去，真机上点开没反应。
    it('跳转 + 只有视频号 ID 与 feed-token：必须拦下并点名缺 feedId', () => {
      const v = values({ playMode: 'redirect', channelId: 'sph_1', feedToken: 'tok_1' })
      expect(v.feedToken).not.toBe('') // 量尺：feed-token 确实非空，二选一本身是满足的
      expect(channelsVideoWarning(v)).toBe('跳转播放需要填写 feedId')
    })

    it('跳转 + 只有 feedId：点名缺视频号 ID（finderUserName）', () => {
      expect(
        channelsVideoWarning(values({ playMode: 'redirect', feedId: 'f_1' }))
      ).toBe('跳转播放需要填写视频号 ID（finderUserName）')
    })

    it('跳转 + 两个都缺：提示同时填写，而不是「二选一」', () => {
      // 这一条专门钉提示顺序：若把「feedId / feed-token 二选一」提到前面，
      // 这里会先返回那条泛提示——拦是拦得住，但会让作者以为填 feed-token 就能跳转。
      const msg = channelsVideoWarning(values({ playMode: 'redirect' }))
      expect(msg).not.toContain('或 feed-token')
      expect(msg).toBe('跳转播放需要同时填写视频号 ID 与 feedId')
    })
  })
})
