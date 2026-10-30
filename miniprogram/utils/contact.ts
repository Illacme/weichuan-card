// 保存名片到手机通讯录（员工名片区「保存名片」与底部「存入通讯录」共用）
// 座机与名片展示同一套回退规则：优先本人 landline，为空用公司总机 tel。
//
// addPhoneContact 属**微信隐私接口**，fail 至少有两种，含义完全不同，不能混为一谈：
//   · 未授权（errMsg 含 privacy，如 privacy permission is not authorized）
//     → 用户还没同意隐私政策。此时应主动拉起授权弹窗，同意后重试一次。
//   · 平台未声明（errno 112 / api scope is not declared in the privacy agreement）
//     → 公众平台的《用户隐私保护指引》里没勾「通讯录（仅写入）」。
//       这种失败**再让用户点一百次同意也没用**，只能让开发者去后台补声明。
//   · deny（auth deny）→ 通讯录权限被系统/微信拒绝，才该引导去设置。
//
// 2026-10-30 的死循环事故就是没区分前两种：errMsg 带 "privacy" 就一律提示
// 「请重新点击并在弹窗中选择同意」，用户点了同意仍失败，再点再提示，无限循环。
import { ensurePrivacy, privacyFailKind, requireAuthorize } from './privacy'

export async function saveCard(
  page: any,
  emp: { name?: string; title?: string; phone?: string; landline?: string; email?: string },
  comp: { fullName?: string; address?: string; tel?: string }
): Promise<void> {
  // 页面没挂 privacy-consent 组件时 ensurePrivacy 会改走微信官方弹窗
  if (!(await ensurePrivacy(page))) return

  const landline = emp.landline || comp.tel || ''

  // 真正写通讯录。retried 用来保证「因未授权失败 → 补授权 → 重试」只发生一次，
  // 否则一旦微信侧持续拒绝就成了新的死循环。
  function add(retried: boolean): void {
    wx.addPhoneContact({
      firstName: emp.name || '',
      title: emp.title || '',
      mobilePhoneNumber: emp.phone || '',
      email: emp.email || '',
      organization: comp.fullName || '',
      addressStreet: comp.address || '',
      workPhoneNumber: landline,
      success: () => wx.showToast({ title: '已保存' }),
      fail: async (err: any) => {
        const kind = privacyFailKind(err)
        const msg = (err && err.errMsg) || ''
        // 失败原因五花八门（未授权 / 未声明 / 权限被拒 / 用户取消），
        // 留一条原始 errMsg 在控制台，排查时不用靠猜文案
        console.warn('[contact] addPhoneContact fail:', kind, msg)

        // 平台配置问题，用户侧无解——如实说明，别再让用户点「同意」
        if (kind === 'undeclared') {
          wx.showModal({
            title: '功能暂不可用',
            content: '小程序尚未在微信后台声明「通讯录」权限，请联系管理员补充隐私声明后再试',
            showCancel: false,
          })
          return
        }

        if (kind === 'unauthorized') {
          if (!retried) {
            // 主动拉起官方隐私弹窗：同意后重试一次，用户不用再手点一遍
            // （原来的写法只是提示「请重新点击」，点了还是同一个失败，等于死循环）
            const ok = await requireAuthorize()
            if (ok) {
              add(true)
              return
            }
          }
          wx.showModal({
            title: '需要隐私授权',
            content: '保存名片到通讯录需要同意隐私政策，未同意时无法继续',
            showCancel: false,
          })
          return
        }

        if (msg.indexOf('deny') >= 0) {
          // 拒绝授权：长文案用 modal，toast 单行会截断
          wx.showModal({
            title: '需要通讯录权限',
            content: '需要通讯录权限才可以保存名片，请前往微信设置开启权限',
            showCancel: false,
          })
          return
        }

        wx.showToast({ title: '已取消', icon: 'none' })
      },
    })
  }

  wx.showModal({
    title: '保存名片',
    content: '将把该联系人存入手机通讯录，是否继续？',
    success: (r) => {
      if (!r.confirm) return
      add(false)
    },
  })
}
