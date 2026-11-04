// 统一的上传实现：**优先预签名直传**（绕开应用服务器，大图/视频不占应用带宽），
// 不可用时回退普通 multipart 上传。两个调用方共享这一份契约：
//   - `components/UploadField.vue`（el-upload 的 http-request）
//   - `components/RichEditor.vue`（wangEditor 图片菜单的 customUpload）
//
// 为什么值得抽出来：两条链路各自的失败语义完全一样（预签名 404/501 → 回退；
// PUT 失败 → 回退；最后仍失败 → 抛错由调用方提示），复制一份必然漂移。
//
// 后端契约（与 internal/handler/storage.go 一致）：
//   GET  /api/admin/upload/presign?ext=&contentType=&size=
//        → { code: 0, data: { putUrl, url, ... } }；本地磁盘等不支持直传时 code != 0
//        size 是**配额计量**的唯一来源：直传的字节永远不会经过服务端，后端只能靠它记账
//        （不传 = 这次上传不计用量，配额统计就漏了）。
//   PUT  <putUrl>（body 为原始文件，Content-Type 同 contentType）→ 2xx 即成功
//   POST /api/admin/upload（multipart，字段名 file）
//        → { code: 0, data: { urls: [url] } }
// 鉴权靠同源 httpOnly Cookie，**不拼 Bearer**（localStorage 里没有 token）。
export interface UploadFileOptions {
  /** 普通上传地址，缺省 /api/admin/upload */
  action?: string
  /** 预签名地址，缺省由 action 推导（/upload → /upload/presign） */
  presignAction?: string
}

/** 上传单个文件，返回可长期访问的 URL；失败抛错（message 可直接展示给用户）。 */
export async function uploadFile(file: File, opts: UploadFileOptions = {}): Promise<string> {
  const action = opts.action || '/api/admin/upload'
  const presignAction = opts.presignAction || action.replace('/upload', '/upload/presign')
  const ext = (file.name.split('.').pop() || '').toLowerCase()
  const contentType = file.type || 'application/octet-stream'

  // 1) 预签名直传：任何一步不顺（接口 404、code!=0、PUT 失败）都落到回退，
  //    绝不因为「直传不可用」让上传整体失败——本地磁盘驱动就是这种部署。
  try {
    const p = await presign(presignAction, ext, contentType, file.size)
    if (p && p.code === 0 && p.data && p.data.putUrl) {
      await putDirect(p.data.putUrl, file, contentType)
      return p.data.url
    }
  } catch {
    /* 预签名不可用：继续走普通上传 */
  }

  return serverUpload(action, file)
}

async function presign(url: string, ext: string, contentType: string, size: number): Promise<any> {
  // action 可能自带查询串（如 /api/admin/upload?companyId=2），此时必须用 & 续接，
  // 否则拼出 a?b=1?ext=png 这种地址，ext 整个丢进 companyId 的值里，服务端拿不到扩展名。
  const sep = url.indexOf('?') === -1 ? '?' : '&'
  const full =
    url +
    sep +
    'ext=' + encodeURIComponent(ext) +
    '&contentType=' + encodeURIComponent(contentType) +
    // size 让后端能记一次用量（直传字节它看不到）；拿不到就退化为不计量
    '&size=' + (Number.isFinite(size) && size > 0 ? Math.round(size) : 0)
  const resp = await fetch(full)
  return resp.json()
}

function putDirect(putUrl: string, file: File, contentType: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', putUrl)
    xhr.setRequestHeader('Content-Type', contentType)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve()
      else reject(new Error('直传失败（HTTP ' + xhr.status + '）'))
    }
    xhr.onerror = () => reject(new Error('直传网络错误'))
    xhr.send(file)
  })
}

function serverUpload(action: string, file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const form = new FormData()
    form.append('file', file)
    const xhr = new XMLHttpRequest()
    xhr.open('POST', action)
    xhr.onload = () => {
      let body: any
      try {
        body = JSON.parse(xhr.responseText)
      } catch {
        // 非 JSON 响应（网关错误页 / 登录失效被重定向）——报状态码比报「解析失败」有用
        reject(new Error('上传失败（HTTP ' + xhr.status + '）'))
        return
      }
      if (body.code !== 0) {
        reject(new Error(body.msg || '上传失败'))
        return
      }
      const url = ((body.data && body.data.urls) || [])[0]
      if (!url) {
        reject(new Error('上传失败：服务端未返回文件地址'))
        return
      }
      resolve(url)
    }
    xhr.onerror = () => reject(new Error('上传网络错误'))
    xhr.send(form)
  })
}
