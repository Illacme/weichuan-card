import { onThumbError } from '../../utils/thumb'

Component({
  options: { addGlobalClass: true },
  properties: {
    services: Array,
    staffId: Number, // 从首页透传，子页分享/返回名片用（跳转页文档 §5）
    // 是否在封面图**下方**再显示一行标题。缺省 false：竖版卡把标题烧在了图内，
    // 再渲染就重复；客户换成不带字的素图后在后台打开（0050）。
    showName: { type: Boolean, value: false },
  },
  methods: {
    onImgErr: onThumbError,
    onTap(e: any) {
      const url =
        '/pages/service/detail/detail?id=' +
        e.currentTarget.dataset.id +
        (this.data.staffId ? '&staffId=' + this.data.staffId : '')
      wx.navigateTo({ url })
    },
  },
})
