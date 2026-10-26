// 保存名片到手机通讯录（员工名片区「保存名片」与底部「存入通讯录」共用）
// 座机与名片展示同一套回退规则：优先本人 landline，为空用公司总机 tel。
// 权限拒绝与主动取消是两种fail：errMsg 含 deny 的是拒绝授权（需引导去设置开启），
// 其余按「已取消」处理（首页文档 §5 对拒绝场景有明确文案要求）。
export function saveCard(
  emp: { name?: string; title?: string; phone?: string; landline?: string; email?: string },
  comp: { fullName?: string; address?: string; tel?: string }
): void {
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
            // 隐私授权被拒：不是用户「取消」，而是没同意隐私政策。
            // 混进「已取消」分支会让用户以为是自己点的，措辞必须分开。
            wx.showModal({
              title: '需要隐私授权',
              content: '保存名片到通讯录需要你同意隐私政策，请前往设置中开启后重试',
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
