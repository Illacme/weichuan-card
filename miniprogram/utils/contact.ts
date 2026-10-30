// 保存名片到手机通讯录（员工名片区「保存名片」与底部「存入通讯录」共用）
// 座机与名片展示同一套回退规则：优先本人 landline，为空用公司总机 tel。
//
// addPhoneContact 属**微信隐私接口**：用户没同意隐私政策时，微信会直接 fail
// （errMsg 含 privacy），代码只能弹提示——而此前弹的是「请前往设置中开启」，
// 完全指错方向：隐私授权不是设置里的开关，而是要在小程序内的弹窗点「同意」。
// 所以调用前必须先走 ensurePrivacy 把同意拿到手（页面需挂 privacy-consent 组件）。
//
// 权限拒绝与主动取消是两种 fail：errMsg 含 deny 的是通讯录权限被拒（需引导去设置），
// 其余按「已取消」处理（首页文档 §5 对拒绝场景有明确文案要求）。
import { ensurePrivacy } from './privacy'

export async function saveCard(
  page: any,
  emp: { name?: string; title?: string; phone?: string; landline?: string; email?: string },
  comp: { fullName?: string; address?: string; tel?: string }
): Promise<void> {
  // 页面没挂 privacy-consent 组件时 ensurePrivacy 会放行（退化成微信官方弹窗兜底）
  if (!(await ensurePrivacy(page))) return

  const landline = emp.landline || comp.tel || ''
  wx.showModal({
    title: '保存名片',
    content: '将把该联系人存入手机通讯录，是否继续？',
    success: (r) => {
      if (!r.confirm) return
      wx.addPhoneContact({
        firstName: emp.name || '',
        title: emp.title || '',
        mobilePhoneNumber: emp.phone || '',
        email: emp.email || '',
        organization: comp.fullName || '',
        addressStreet: comp.address || '',
        workPhoneNumber: landline,
        success: () => wx.showToast({ title: '已保存' }),
        fail: (err: any) => {
          const msg = (err && err.errMsg) || ''
          if (msg.indexOf('privacy') >= 0) {
            // 前置 ensurePrivacy 已通过却仍报 privacy：微信侧仍判定未同意
            // （官方弹窗被拒 / 基础库异常）。这里不能说「去设置」——用户要做的
            // 是再点一次并在弹窗里选「同意」。
            wx.showModal({
              title: '需要隐私授权',
              content: '请重新点击「存入通讯录」，并在弹出的隐私提示中选择「同意」',
              showCancel: false,
            })
          } else if (msg.indexOf('deny') >= 0) {
            // 拒绝授权：长文案用 modal，toast 单行会截断
            wx.showModal({
              title: '需要通讯录权限',
              content: '需要通讯录权限才可以保存名片，请前往微信设置开启权限',
              showCancel: false,
            })
          } else {
            wx.showToast({ title: '已取消', icon: 'none' })
          }
        },
      })
    },
  })
}
