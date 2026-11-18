// 空分类网格守门：后台把「业绩分类显示数量」设为 0 时，端上不该留一条悬空分隔线。
//
// ink 风格的 .cat-grid 有 border-bottom（见 app.wxss），categories 为空时原本照样渲染，
// 于是「经典业绩」标题下只剩一条没有内容的横线。这里钉住组件根节点按数组长度条件渲染，
// 防止有人为了「占位」把空态又改回无条件渲染。
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const grid = readFileSync(resolve(root, 'miniprogram/components/achievement-grid/index.wxml'), 'utf-8')
const indexTs = readFileSync(resolve(root, 'miniprogram/pages/index/index.ts'), 'utf-8')

describe('业绩分类网格空态', () => {
  it('量尺自检：能抓到 .cat-grid 根节点（守门不空转）', () => {
    const roots = grid.match(/<view class="cat-grid"[^>]*>/g) || []
    expect(roots).toHaveLength(1)
  })

  it('categories 为空时整块不渲染（否则 ink 会留下悬空分隔线）', () => {
    const rootTag = grid.match(/<view class="cat-grid"[^>]*>/) || []
    expect(rootTag[0]).toMatch(/wx:if="\{\{categories\.length\}\}"/)
  })

  it('首页仍按 categoryLimit=0 截出空数组，空态才真的会触发', () => {
    expect(indexTs).toMatch(/slice\(0, categoryLimit\)/)
    expect(indexTs).toMatch(/Math\.max\(0, Math\.min\(20, layout\.categoryLimit \?\? 20\)\)/)
  })
})
