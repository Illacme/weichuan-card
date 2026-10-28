// employee-card 组件：手机/座机/邮箱点击复制后，提示必须明确复制的是什么。
// 名片上三类联系方式并排，只弹「已复制」用户分不清刚存的是哪个号；带 data-type
// 后 onCopy 拼出「已复制手机号 / 座机号 / 邮箱」，一目了然。
//
// 测试桩：mock 全局 Component 捕获选项；mock wx.setClipboardData（同步触发 success）
// 与 wx.showToast（捕获 title）。与 company_video.spec.ts 同款套路。
import { describe, it, expect, vi, beforeEach } from 'vitest'

let captured: any = null
let toastTitle: string | null = null

const setClipboard = vi.fn()
const showToast = vi.fn()

vi.stubGlobal('Component', (opts: any) => {
  captured = opts
})
vi.stubGlobal('wx', {
  setClipboardData(o: any) {
    setClipboard(o)
    if (o && typeof o.success === 'function') o.success()
  },
  showToast(o: any) {
    showToast(o)
    toastTitle = o && o.title
  },
})

function instance(opts: any) {
  const inst: any = {
    data: JSON.parse(JSON.stringify(opts.data || {})),
    setData(patch: any) {
      Object.assign(this.data, patch)
    },
  }
  for (const k of Object.keys(opts)) {
    if (typeof opts[k] === 'function') inst[k] = opts[k].bind(inst)
  }
  if (opts.methods) {
    for (const k of Object.keys(opts.methods)) {
      inst[k] = opts.methods[k].bind(inst)
    }
  }
  inst.observers = opts.observers || {}
  return inst
}

async function loadCard() {
  vi.resetModules()
  await import('../components/employee-card/index')
  if (!captured) throw new Error('组件未注册')
  return captured
}

async function tap(type: string, value: string) {
  const c = instance(await loadCard())
  c.onCopy({ currentTarget: { dataset: { copy: value, type } } })
  return toastTitle
}

beforeEach(() => {
  captured = null
  toastTitle = null
  setClipboard.mockClear()
  showToast.mockClear()
})

describe('employee-card 复制提示明确化', () => {
  it('复制手机号 → 已复制手机号', async () => {
    expect(await tap('手机号', '13800001111')).toBe('已复制手机号')
  })

  it('复制副手机号 → 已复制副手机号', async () => {
    expect(await tap('副手机号', '13900002222')).toBe('已复制副手机号')
  })

  it('复制座机号 → 已复制座机号', async () => {
    expect(await tap('座机号', '021-12345678')).toBe('已复制座机号')
  })

  it('复制邮箱 → 已复制邮箱', async () => {
    expect(await tap('邮箱', 'a@b.com')).toBe('已复制邮箱')
  })

  it('复制内容为空时不弹提示（避免空剪贴板还报成功）', async () => {
    const c = instance(await loadCard())
    c.onCopy({ currentTarget: { dataset: { copy: '', type: '手机号' } } })
    expect(showToast).not.toHaveBeenCalled()
  })
})
