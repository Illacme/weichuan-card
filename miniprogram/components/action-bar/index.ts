import { saveCard } from '../../utils/contact'
import { track } from '../../utils/track'

Component({
  options: { addGlobalClass: true },
  properties: {
    employee: Object,
    company: Object,
    staffId: Number,
  },
  methods: {
    onCall() {
      const phone = this.data.employee.phone
      if (!phone) return
      wx.makePhoneCall({ phoneNumber: phone })
      track('call', this.data.staffId)
    },
    onSave() {
      // ensurePrivacy 需要页面实例来 selectComponent('#privacy-consent')。
      // 组件里拿不到 page，只能从页面栈取栈顶——action-bar 只挂在名片首页底部，
      // 栈顶就是它。页面未挂组件时 ensurePrivacy 会放行，不会卡住这次点击。
      const pages = getCurrentPages()
      const page = pages[pages.length - 1]
      saveCard(page, this.data.employee, this.data.company).catch(() => {
        // 授权流程本身出错不该影响埋点，也不该把异常抛给点击事件
      })
      track('save', this.data.staffId)
    },
  },
})
