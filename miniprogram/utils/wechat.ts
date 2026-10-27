import { request } from './request'

// 微信身份采集：openid（静默）+ 昵称（需用户授权）。
// 用于申请开通与员工登录时记录微信身份，供 PC 后台核实申请人/登录者。

// 获取微信 openid：wx.login 拿临时 code，后端经 jscode2session 换 openid。
// openid 在本小程序内稳定，缓存到 storage 避免重复换。
// 后端未配置 WX 凭据 / 换取失败时返回空串——不影响账号密码登录与申请（降级）。
export function getOpenid(): Promise<string> {
  return new Promise((resolve) => {
    const cached = wx.getStorageSync('wxOpenid')
    if (cached) {
      resolve(cached)
      return
    }
    wx.login({
      success(res: any) {
        if (!res.code) {
          resolve('')
          return
        }
        request('POST', '/api/wechat/openid', { code: res.code })
          .then((r: any) => {
            if (r && r.code === 0 && r.openid) {
              wx.setStorageSync('wxOpenid', r.openid)
              resolve(r.openid)
            } else {
              resolve('') // 未配置凭据等：降级为空
            }
          })
          .catch(() => resolve(''))
      },
      fail: () => resolve(''),
    })
  })
}

// 本文件刻意**不再**提供 getWechatNickname()。
//
// 原因：wx.getUserProfile 已于 2022-10-25 被平台收回，此后调用统一返回
// 「默认灰色头像 + 昵称『微信用户』」，拿不到真实微信昵称。保留这个 helper
// 只会让人以为「调它就能拿到昵称」，于是把占位值当真名写进表单、提交入库。
//
// 要真昵称请用官方「头像昵称填写能力」：表单里放
//   <input type="nickname">（昵称）与 <button open-type="chooseAvatar">（头像），
// 由微信在聚焦/点击时自动填充真实值。申请页 pages/apply/open 已按此实现。
// 另注：这两者都要求 MP 后台已声明对应隐私类型，否则平台直接禁用（errno 112）。
