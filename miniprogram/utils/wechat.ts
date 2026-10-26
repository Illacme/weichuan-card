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

// 获取微信昵称（getUserProfile 授权弹窗）。
// 注意：必须在用户点击手势里同步调用 wx.getUserProfile 才会弹窗，
// 因此本函数被调用时就地触发（调用方应在 tap 处理里、任何 await 之前调用）。
// 用户拒绝 / 基础库不支持时返回空串，不阻塞主流程。
//
// 重要（2026-10-26 核实官方公告后修正）：
// wx.getUserProfile 已于 2022-10-25 起被平台**收回**——生效期后发布的小程序新版本
// 调用它统一返回「默认灰色头像 + 昵称『微信用户』」，拿不到真实微信昵称。
// 所以这里必须把「微信用户」这个占位值识别出来当作「没拿到」，
// 否则会把「微信用户」当成真昵称写进表单并提交入库（假数据）。
// 想要真昵称需改用「头像昵称填写能力」（<input type="nickname">）。
const ANONYMOUS_NICKNAME = '微信用户'

export function getWechatNickname(): Promise<string> {
  return new Promise((resolve) => {
    const wxAny = wx as any
    if (typeof wxAny.getUserProfile !== 'function') {
      resolve('')
      return
    }
    wxAny.getUserProfile({
      desc: '用于展示您的微信昵称，便于管理员核实身份',
      success(res: any) {
        const name = (res.userInfo && res.userInfo.nickName) || ''
        // 平台收回后固定返回「微信用户」：当没拿到处理，别把占位值当真名提交
        resolve(name === ANONYMOUS_NICKNAME ? '' : name)
      },
      fail: () => resolve(''),
    })
  })
}
