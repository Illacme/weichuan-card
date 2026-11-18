import { onThumbError } from '../../utils/thumb'

Component({
  options: { addGlobalClass: true },
  properties: {
    categories: Array,
    staffId: Number, // 从首页透传，子页分享/返回名片用（跳转页文档 §5）
    // 是否在封面图**下方**再显示一行名称。缺省 false：设计稿把名称画进了封面图，
    // 再渲染就重复；客户换成不带字的素图后在后台打开（0050）。
    showName: { type: Boolean, value: false },
  },
  methods: {
    onImgErr: onThumbError,
    onTap(e: any) {
      const url =
        '/pages/project/list/list?categoryId=' +
        e.currentTarget.dataset.id +
        (this.data.staffId ? '&staffId=' + this.data.staffId : '')
      wx.navigateTo({ url })
    },
  },
})
