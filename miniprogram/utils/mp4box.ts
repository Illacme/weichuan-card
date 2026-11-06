// MP4 播放器画幅计算：首页 company-video 与详情页 rich-video 共用。
//
// 由来（2026-11-05）：固定高度的容器配 object-fit="contain" 会留黑边，
// 配 "cover" 会把画面左右各裁掉约 7%，两侧字幕 / LOGO 直接消失——用户明确
// 否掉了「用裁切凑满屏」。正解是拿源文件宽高算出与视频等比的容器高度，
// 再用 contain 完整呈现：容器比例 == 视频比例，既不裁也不留黑边。
//
// 抽成共享纯函数而不是复制两份：两处渲染点的比例安全区间必须一致，
// 复制一份迟早漂移；纯函数也方便单测像素计算，不必把 wx 的链式查询造一遍。
//
// 比例（height / width）安全区间：16:9=0.5625、4:3=0.75 原样；9:16 竖屏
// 若按 1.78 撑高会把整页顶下去，钳到 4/3 后由 contain 留左右黑边——
// 仍不裁画面，属可接受的降级。
export const MIN_MP4_RATIO = 0.4
export const MAX_MP4_RATIO = 4 / 3

// 由「容器实测宽度（px）」与「视频原始宽高」算出行内样式 height。
// 返回空串 = 不覆盖 WXSS 默认高度：最多留黑边，绝不裁切。
export function mp4BoxStyleFor(boxWidth: number, width: number, height: number): string {
  if (!(boxWidth > 0) || !(width > 0) || !(height > 0)) return ''
  const raw = height / width
  if (!Number.isFinite(raw)) return ''
  const ratio = Math.min(MAX_MP4_RATIO, Math.max(MIN_MP4_RATIO, raw))
  return `height:${Math.round(boxWidth * ratio)}px`
}
