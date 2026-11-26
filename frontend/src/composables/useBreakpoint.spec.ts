import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { deviceOf, TABLET_MAX_WIDTH, PHONE_MAX_WIDTH } from './useBreakpoint'

describe('deviceOf 断点映射', () => {
  it('手机：<= 767', () => {
    expect(deviceOf(320)).toBe('phone')
    expect(deviceOf(375)).toBe('phone')
    expect(deviceOf(PHONE_MAX_WIDTH)).toBe('phone')
  })

  it('平板：768 - 1199（含上下界）', () => {
    expect(deviceOf(PHONE_MAX_WIDTH + 1)).toBe('tablet')
    expect(deviceOf(768)).toBe('tablet')
    expect(deviceOf(900)).toBe('tablet')
    expect(deviceOf(TABLET_MAX_WIDTH)).toBe('tablet')
  })

  it('桌面：>= 1200', () => {
    expect(deviceOf(TABLET_MAX_WIDTH + 1)).toBe('desktop')
    expect(deviceOf(1280)).toBe('desktop')
    expect(deviceOf(1920)).toBe('desktop')
  })
})

// 全局响应式规则与这里的断点定义是「同一套口径的两半」：JS 决定渲染哪些列，
// CSS 决定剩下的表单/弹窗怎么排版。只守 JS 那半，CSS 被删了照样绿。
describe('responsive.css 手机端表单规则', () => {
  const css = readFileSync('src/styles/responsive.css', 'utf8')

  it('页面主体表单与弹窗表单都在手机上 label 顶对齐', () => {
    expect(css, '弹窗表单规则被删').toContain('.el-dialog .el-form .el-form-item__label')
    expect(css, '页面主体表单规则被删（四个内容页会退回横向挤压）')
      .toContain('.admin-main .el-form .el-form-item__label')
    // label-width 是内联 width，不用 !important 覆盖不掉
    expect(css, 'label 内联宽度没被覆盖').toMatch(
      /\.admin-main \.el-form \.el-form-item__label\s*\{[^}]*width:\s*auto\s*!important/s,
    )
    // content 若还留 Element Plus 默认的 margin-left，顶对齐后输入会右移一截
    expect(css).toMatch(
      /\.admin-main \.el-form \.el-form-item__content\s*\{[^}]*margin-left:\s*0\s*!important/s,
    )
  })
})
