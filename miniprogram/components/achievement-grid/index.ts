import { onThumbError } from '../../utils/thumb'

Component({
  options: { addGlobalClass: true },
  properties: {
    categories: Array,
    staffId: Number, // 从首页透传，子页分享/返回名片用（跳转页文档 §5）
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
