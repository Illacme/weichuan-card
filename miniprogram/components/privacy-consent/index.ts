// 微信隐私授权弹窗（组件）。
//
// 为什么做成组件而不是各页自己写：微信侧的隐私授权状态是**全局一次性**的
// （同意一次，之后所有已声明的隐私接口都能直接调用），弹窗只需要出现在
// 任何一个页面里；做成组件后由页面用 selectComponent 调 showPrivacyDialog() 触发，
// 避免每个页面重复一份弹窗结构与状态机。
//
// 同意按钮必须是 <button open-type="agreePrivacyAuthorization">：
// 基础库会校验该按钮**确实被点击过**（buttonId 要与被点按钮的 id 一致），
// 用普通按钮 + 自己的状态标记是无效的，微信侧不会记录同意。
//
// 组件不直接决定「要不要弹」：那由 utils/privacy.ts 判定（needConsent），
// 组件只负责呈现与上报用户的两个选择。
import { markAgreed, openContract, privacyContractName, settle } from '../../utils/privacy'

Component({
  options: { addGlobalClass: true },
  properties: {
    // 是否显示弹窗。由外部 setData 控制，不用内部状态——
    // 外部（页面）才知道授权流程进行到哪一步。
    visible: {
      type: Boolean,
      value: false,
    },
  },
  data: {
    // 微信侧配置的《小程序用户隐私保护指引》名称，用于弹窗里的说明文案
    contractName: '',
  },
  observers: {
    visible(v: boolean) {
      if (v) {
        this.setData({ contractName: privacyContractName() })
      }
    },
  },
  methods: {
    // catchtouchmove 需要组件内声明处理函数（否则控制台报 noop is not a function）；
    // 遮罩靠它阻止背景滚动。
    noop() {},
    // 用户点了「同意」：bindagreeprivacyauthorization 事件触发即代表
    // 微信侧已收到同意同步（此事件只在 open-type="agreePrivacyAuthorization"
    // 的按钮上有效），此时结束等待、后续隐私接口可正常调用。
    onAgree() {
      markAgreed()
      settle(true)
      this.triggerEvent('agree')
    },
    // 用户点了「不同意」：不调 markAgreed，下次再触发隐私接口时微信会
    // 再次要求授权（微信侧会按规则决定是否再弹官方弹窗）。
    //
    // 必须 settle(false)：调用方在 await 授权结果，不结算就等于把这次点击吞了
    // （用户点了「不同意」却什么也没发生，比明确拒绝更糟）。
    onDisagree() {
      settle(false)
      this.triggerEvent('disagree')
    },
    // 「查看隐私政策」：打开微信侧配置的完整指引页。
    onViewContract() {
      openContract()
    },
  },
})
