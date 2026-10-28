import { describe, it, expect } from 'vitest'
import { thumbnailURL } from '../utils/thumb'

describe('thumbnailURL', () => {
  it('在扩展名前插入 _thumb（本地相对路径）', () => {
    expect(thumbnailURL('/uploads/abcd1234.jpg')).toBe('/uploads/abcd1234_thumb.jpg')
  })

  it('本地 png 同样生效', () => {
    expect(thumbnailURL('/uploads/abcd1234.png')).toBe('/uploads/abcd1234_thumb.png')
  })

  it('云存储绝对 URL 在扩展名前插入 _thumb', () => {
    expect(thumbnailURL('https://cdn.xxx/uploads/abcd1234.jpg')).toBe(
      'https://cdn.xxx/uploads/abcd1234_thumb.jpg',
    )
  })

  it('带查询串的 URL 保留查询串', () => {
    expect(thumbnailURL('https://cdn.xxx/abc.png?v=1#frag')).toBe(
      'https://cdn.xxx/abc_thumb.png?v=1#frag',
    )
  })

  it('非 jpg/png 原样返回（后端不生成缩略图）', () => {
    expect(thumbnailURL('/uploads/abc.gif')).toBe('/uploads/abc.gif')
    expect(thumbnailURL('/uploads/abc.webp')).toBe('/uploads/abc.webp')
    expect(thumbnailURL('/uploads/abc.mp4')).toBe('/uploads/abc.mp4')
  })

  it('空值返回空串', () => {
    expect(thumbnailURL('')).toBe('')
    expect(thumbnailURL(null)).toBe('')
    expect(thumbnailURL(undefined)).toBe('')
  })

  it('无扩展名的地址原样返回（如微信头像 qlogo 路径）', () => {
    expect(thumbnailURL('https://thirdwx.qlogo.cn/mmopen/xxx/132')).toBe(
      'https://thirdwx.qlogo.cn/mmopen/xxx/132',
    )
  })
})
