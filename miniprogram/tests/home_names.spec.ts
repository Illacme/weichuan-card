// 首页「封面图下方是否显示名称」守门（0050，两个开关各自独立）。
//
// 客户现状：设计稿把名称直接画进了封面图——业绩分类是「居住住宅」这类带字方图，
// 业务服务是竖版卡片，图上已有标题。端上再渲染一行就是**重复**，所以默认关闭。
// 客户若换成不带字的素图，要能在后台打开开关让名称补回图下。
//
// 这条链路横跨四层，任一层漏改都是「后台打开了、小程序没变」的静默失败：
//   home_layouts 两列（迁移 0050）
//   → admin GET/PUT 与公开 GET 下发
//   → index.ts 解析并 setData
//   → 两个网格组件按 showName 决定渲染与否
//
// 三类最坏的错法，下面逐个钉：
//   1) 缺省写成 true —— 所有存量租户的首页立刻多出一行重复文字；
//   2) 布尔指针用零值判断「未提交」—— 客户永远关不掉已经打开的开关；
//   3) 把「无图」也一起藏掉 —— 占位块里没有名称信息，不显示就彻底认不出是什么。
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')
const read = (p: string) => readFileSync(resolve(REPO, p), 'utf-8')

const CAT_WXML = read('miniprogram/components/achievement-grid/index.wxml')
const CAT_TS = read('miniprogram/components/achievement-grid/index.ts')
const SVC_WXML = read('miniprogram/components/service-grid/index.wxml')
const SVC_TS = read('miniprogram/components/service-grid/index.ts')
const INDEX_WXML = read('miniprogram/pages/index/index.wxml')
const INDEX_TS = read('miniprogram/pages/index/index.ts')
const TYPES = read('miniprogram/utils/types.ts')
const MODEL = read('backend/internal/model/models.go')
const MIGRATION = read('backend/migrations/history/0050_home_show_names.sql')

/** 取某个标签的属性片段，用来判断 wx:if 的条件到底写了什么 */
function tagWith(src: string, marker: string): string {
  const line = src.split('\n').find((l) => l.includes(marker))
  return line || ''
}

describe('封面图下方名称显示开关（0050）', () => {
  it('量尺自检：黄金 fixture 能被取到，证明按行取标签不是空转', () => {
    const fixture = [
      '<view class="cat-name" wx:if="{{!item.icon || (showName && item.name)}}">{{item.name}}</view>',
      '<view class="svc-title" wx:if="{{showName && item.title}}">{{item.title}}</view>',
    ].join('\n')
    expect(tagWith(fixture, 'cat-name')).toContain('showName')
    expect(tagWith(fixture, 'svc-title')).toContain('showName')
    expect(tagWith(fixture, 'not-there')).toBe('')
  })

  it('业绩分类：有图时按开关渲染名称，无图时始终渲染', () => {
    const line = tagWith(CAT_WXML, 'class="cat-name"')
    expect(line, '缺 cat-name 行').toBeTruthy()
    expect(line, '无图时必须仍显示名称（占位块里没有名称信息）').toContain('!item.icon')
    expect(line, '有图时必须受 showName 控制').toContain('showName')
    expect(line, '名称为空时不该渲染空行').toContain('item.name')
  })

  it('业务服务：有图时按开关渲染标题，无图时始终渲染', () => {
    const line = tagWith(SVC_WXML, 'class="svc-title"')
    expect(line, '缺 svc-title 行').toBeTruthy()
    expect(line).toContain('!item.icon')
    expect(line).toContain('showName')
    expect(line).toContain('item.title')
  })

  it('两个组件的 showName 缺省必须是 false（否则存量租户立刻多一行重复文字）', () => {
    for (const [name, src] of [['achievement-grid', CAT_TS], ['service-grid', SVC_TS]] as const) {
      expect(src, `${name} 未声明 showName 属性`).toMatch(/showName\s*:\s*\{\s*type:\s*Boolean/)
      expect(src, `${name} 的 showName 缺省不是 false`).toMatch(
        /showName\s*:\s*\{\s*type:\s*Boolean\s*,\s*value:\s*false\s*\}/
      )
    }
  })

  it('首页把两个开关分别透传给对应组件', () => {
    expect(tagWith(INDEX_WXML, '<achievement-grid')).toContain('showName="{{showCategoryName}}"')
    expect(tagWith(INDEX_WXML, '<service-grid')).toContain('showName="{{showServiceName}}"')
  })

  it('index.ts 初始值 false，且用 === true 解析（挡住字符串 "false" 被当真值）', () => {
    expect(INDEX_TS).toMatch(/showCategoryName:\s*false/)
    expect(INDEX_TS).toMatch(/showServiceName:\s*false/)
    expect(INDEX_TS).toMatch(/layout\.showCategoryName\s*===\s*true/)
    expect(INDEX_TS).toMatch(/layout\.showServiceName\s*===\s*true/)
    // setData 里也要带上，否则组件拿到的永远是初始值
    expect(INDEX_TS).toMatch(/showCategoryName,\s*\n\s*showFeaturedName,\s*\n\s*showServiceName,/)
  })

  it('类型契约带这两个可选字段', () => {
    expect(TYPES).toMatch(/showCategoryName\?:\s*boolean/)
    expect(TYPES).toMatch(/showServiceName\?:\s*boolean/)
  })

  it('后端两列缺省 false，且拆成两列（分类图与服务卡常不是同一批素材）', () => {
    expect(MODEL).toMatch(/ShowCategoryName\s+bool\s+`gorm:"column:show_category_name;default:false"/)
    expect(MODEL).toMatch(/ShowServiceName\s+bool\s+`gorm:"column:show_service_name;default:false"/)
    expect(MIGRATION).toContain('show_category_name')
    expect(MIGRATION).toContain('show_service_name')
    expect(MIGRATION, '迁移默认值必须是 0').toMatch(/show_category_name[^,]*DEFAULT 0/)
    expect(MIGRATION).toMatch(/show_service_name[^,]*DEFAULT 0/)
  })
})


describe('精选案例封面图下方名称显示（0065）', () => {
  const FEAT_WXML = readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), '../components/featured-projects/index.wxml'),
    'utf-8',
  )
  const FEAT_TS = readFileSync(
    resolve(dirname(fileURLToPath(import.meta.url)), '../components/featured-projects/index.ts'),
    'utf-8',
  )

  it('精选案例：有图时按开关渲染名称，无图时始终渲染，且不与 wx:for 同标签', () => {
    const line = tagWith(FEAT_WXML, 'class="feat-name"')
    expect(line, '缺 feat-name 行').toBeTruthy()
    expect(line, '无图时必须仍显示名称').toContain('!p.cover')
    expect(line, '有图时必须受 showName 控制').toContain('showName')
    expect(line, '名称为空时不该渲染空行').toContain('p.name')
    // WXML 硬约束：条件与循环不能同标签（小程序取不到 for-index）
    expect(line).not.toContain('wx:for')
  })

  it('组件 showName 缺省 false（否则存量租户立刻多一行重复文字）', () => {
    expect(FEAT_TS).toMatch(/showName\s*:\s*\{\s*type:\s*Boolean/)
    expect(FEAT_TS).toMatch(/showName\s*:\s*\{\s*type:\s*Boolean\s*,\s*value:\s*false\s*\}/)
  })

  it('首页透传 showFeaturedName 给精选案例组件；index.ts 用 === true 解析', () => {
    expect(tagWith(INDEX_WXML, '<featured-projects')).toContain('showName="{{showFeaturedName}}"')
    expect(INDEX_TS).toMatch(/showFeaturedName:\s*false/)
    expect(INDEX_TS).toMatch(/layout\.showFeaturedName\s*===\s*true/)
  })

  it('迁移与模型：show_featured_name 缺省 0 / false', () => {
    const migration = read('backend/migrations/history/0065_home_show_featured_name.sql')
    expect(MODEL).toMatch(/ShowFeaturedName\s+bool\s+`gorm:"column:show_featured_name;default:false"/)
    expect(migration).toContain('show_featured_name')
    expect(migration).toMatch(/show_featured_name[^;]*DEFAULT 0/)
  })
})
