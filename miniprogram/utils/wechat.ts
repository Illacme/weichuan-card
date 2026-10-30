import { request } from './request'
import { getCompanyId } from './tenant'

// 微信身份采集：openid（静默）+ 昵称（需用户授权）。
// 用于申请开通与员工登录时记录微信身份，供 PC 后台核实申请人/登录者。

// 获取微信 openid：wx.login 拿临时 code，后端经 jscode2session 换 openid。
// openid 在本小程序内稳定，缓存到 storage 避免重复换。
// 后端未配置 WX 凭据 / 换取失败时返回空串——不影响账号密码登录与申请（降级）。
//
// 必须带上当前企业 companyId：本项目支持「企业独立小程序」（每企业可配自己的
// appid），换 openid 用的是该企业自己的小程序凭据（见后端 wxAppOf(companyID)）。
// 不带的话后端一律按 company_id=0（平台全局应用）换，企业独立版下换出来的 openid
// 与申请人实际所用小程序不是一个，后台审核列表里看到的就会是空或错值。
// 缓存按企业分键：同一台机器切换企业时不会把 A 企业的 openid 串到 B 企业。
export function getOpenid(): Promise<string> {
  return new Promise((resolve) => {
    const cid = getCompanyId()
    const cacheKey = cid > 0 ? 'wxOpenid_' + cid : 'wxOpenid'
    const cached = wx.getStorageSync(cacheKey)
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
        request('POST', '/api/wechat/openid', { code: res.code, companyId: cid })
          .then((r: any) => {
            if (r && r.code === 0 && r.openid) {
              wx.setStorageSync(cacheKey, r.openid)
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

// 取一个**新的** wx.login code，供微信一键登录 / 绑定微信使用。
//
// 与 getOpenid 刻意不同：getOpenid 把 openid 缓存进 storage（openid 不变，缓存是为了
// 少打一次接口），而一键登录必须每次拿新 code——code 一次性、5 分钟过期，
// 拿缓存的 openid 等于让后端没法确认「这次请求真的来自这个微信」。
//
// 失败（用户拒绝 / 微信未就绪）返回空串，由调用方提示重试；不 reject，
// 免得上层到处写 try/catch。
export function wxLoginCode(): Promise<string> {
  return new Promise((resolve) => {
    wx.login({
      success(res: any) {
        resolve(res && res.code ? res.code : '')
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
