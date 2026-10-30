import { request } from './request'

// 审核结果订阅消息（微信「一次性订阅」）。
//
// 约束：用户同意一次 → 只能下发一条。所以这里只服务「审核结果」这类低频、
// 用户强预期的事件；**不做**「名片被访问」这种高频提醒——同意一次只收到第一条，
// 之后再也不响，用户会以为功能坏了，那比没有更糟。
//
// 两个必须记住的接线要点：
//  1. wx.requestSubscribeMessage **必须在用户点击事件的同步栈里调用**。
//     await 之后再调会被微信拒绝（它要靠手势判断「这是用户主动同意的」）。
//     所以本模块拆成两步：页面 onLoad 异步预取模板 ID，
//     点击时调 requestAuditSubscribe()（同步、fire-and-forget）。
//  2. 用户拒绝 / 关掉弹窗都不影响业务：不同意只是收不到通知。
//     绝不能因为没同意就拦住提交——那是拿「通知」去要挟主流程。
//
// 另一个容易踩的点：**模板 ID 必须来自后端**，不能写死在端上。企业独立版
// 各家小程序各有各的模板；写死会让「平台能发、独立版发不出去」这种问题
// 只在某个客户的真机上暴露。

// 模块级缓存：一次冷启动只查一次（模板 ID 在运行期不会变）。
// null = 还没查过；'' = 查过但没有（未配置 / 请求失败）。
let tmplId: string | null = null

// loadAuditTemplate 预取「审核结果通知」的模板 ID。页面 onLoad 调一次即可。
//
// 未配置时得到空串，后续 requestAuditSubscribe 直接跳过——不能拿空 tmplIds
// 去调微信（会直接 fail），用户看到的是「点了没反应」。
export async function loadAuditTemplate(): Promise<string> {
  if (tmplId !== null) return tmplId
  try {
    const r = await request('GET', '/api/subscribe/config')
    if (r && r.code === 0 && r.data) {
      // 只有拿到确定结果才缓存：网络异常时不缓存，下次进页面还能再试
      // （缓存空串会让整个会话都收不到通知，而用户完全无从察觉）
      tmplId = (r.data as any).auditTemplateId || ''
    }
  } catch {
    // 静默：通知是可选增强，取不到不该在页面上报错
  }
  return tmplId || ''
}

// requestAuditSubscribe 请求订阅授权。
//
// **必须同步调用**（点击事件里直接调，前面不能有 await）。
// 不返回 Promise：微信的回调有 accept / reject / ban 三种，三种都不影响业务，
// 调用方不需要等它，也不该等——等它等于把提交流程卡在一个可选动作上。
export function requestAuditSubscribe(): void {
  const id = tmplId
  if (!id) return // 未配置 / 预取失败：静默跳过，不弹空窗
  wx.requestSubscribeMessage({
    tmplIds: [id],
    success() {},
    fail() {},
  })
}
