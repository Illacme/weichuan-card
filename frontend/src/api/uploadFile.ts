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
//
// 进度：两条 XHR 都接 upload.onprogress，并把「当前走哪条路」一起报出去。
// 大视频几十上百 MB，用户点完「选择文件」如果什么都没有，只会以为页面卡死；
// 而直传失败会**静默回退**成经服务器中转，那一刻进度必然从头开始——
// 不把阶段说出来，用户看到的就是「进度条倒退」这个说不通的现象。
export type UploadPhase =
  | 'presign' // 正在申请直传链接（还没开始传字节）
  | 'direct' // 浏览器直接 PUT 到对象存储
  | 'server' // 直传不可用，改走应用服务器中转

export interface UploadProgress {
  phase: UploadPhase
  /** 0~100。同一阶段内只前进；换阶段会重新从 0 计（见上）。 */
  percent: number
  loaded: number
  total: number
}

export interface UploadFileOptions {
  /** 普通上传地址，缺省 /api/admin/upload */
  action?: string
  /** 预签名地址，缺省由 action 推导（/upload → /upload/presign） */
  presignAction?: string
  /** 进度回调；不传则完全不碰 XHR 的 upload 对象（老调用方零影响）。 */
  onProgress?: (p: UploadProgress) => void
}

/** 上传单个文件，返回可长期访问的 URL；失败抛错（message 可直接展示给用户）。 */
export async function uploadFile(file: File, opts: UploadFileOptions = {}): Promise<string> {
  const action = opts.action || '/api/admin/upload'
  const presignAction = opts.presignAction || action.replace('/upload', '/upload/presign')
  const ext = (file.name.split('.').pop() || '').toLowerCase()
  const contentType = file.type || 'application/octet-stream'

  // 进度只在有订阅者时计算，避免给每个调用方平白添一次回调开销。
  const report = (phase: UploadPhase, loaded = 0, total = file.size, done = false) => {
    if (!opts.onProgress) return
    const t = total > 0 ? total : file.size
    // 完成前封顶 99：XHR 的 progress 事件可能先于服务端落盘完成，
    // 100% 只能在 onload 之后给，否则会出现「显示传完了却还不返回」。
    const percent = done ? 100 : t > 0 ? Math.max(0, Math.min(99, Math.round((loaded / t) * 100))) : 0
    opts.onProgress({ phase, percent, loaded, total: t })
  }
  // 没订阅进度就别生成回调，bindProgress 也就不会往 xhr.upload 上挂东西：
  // 富文本编辑器等老调用方不该因为这次改动多一个空监听器。
  const onXhrProgress = (phase: UploadPhase): ProgressFn | undefined =>
    opts.onProgress ? (loaded, total) => report(phase, loaded, total) : undefined

  report('presign')

  // 1) 预签名直传：任何一步不顺（接口 404、code!=0、PUT 失败）都落到回退，
  //    绝不因为「直传不可用」让上传整体失败——本地磁盘驱动就是这种部署。
  try {
    const p = await presign(presignAction, ext, contentType, file.size)
    if (p && p.code === 0 && p.data && p.data.putUrl) {
      await putDirect(p.data.putUrl, file, contentType, onXhrProgress('direct'))
      report('direct', file.size, file.size, true)
      return p.data.url
    }
  } catch {
    /* 预签名不可用：继续走普通上传 */
  }

  // 2) 回退。先报一次 phase='server' 且 0%，调用方据此把进度条重置并
  //    给出「已改用服务器中转」的原因，而不是让进度莫名其妙倒退。
  report('server', 0)
  return serverUpload(action, file, onXhrProgress('server'), () =>
    report('server', file.size, file.size, true)
  )
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

type ProgressFn = (loaded: number, total: number) => void

/** 给 xhr.upload 挂进度监听。跨域 PUT 也能收到 progress 事件（不读响应体）。 */
function bindProgress(xhr: XMLHttpRequest, onProgress?: ProgressFn): void {
  if (!onProgress || !xhr.upload) return
  xhr.upload.onprogress = (e) => {
    // lengthComputable 为 false 时 total 是 0，别拿它算百分比（会得到 NaN）
    if (e.lengthComputable && e.total > 0) onProgress(e.loaded, e.total)
  }
}

function putDirect(putUrl: string, file: File, contentType: string, onProgress?: ProgressFn): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', putUrl)
    xhr.setRequestHeader('Content-Type', contentType)
    bindProgress(xhr, onProgress)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve()
      else reject(new Error('直传失败（HTTP ' + xhr.status + '）'))
    }
    xhr.onerror = () => reject(new Error('直传网络错误'))
    xhr.send(file)
  })
}

function serverUpload(action: string, file: File, onProgress?: ProgressFn, onDone?: () => void): Promise<string> {
  return new Promise((resolve, reject) => {
    const form = new FormData()
    form.append('file', file)
    const xhr = new XMLHttpRequest()
    xhr.open('POST', action)
    bindProgress(xhr, onProgress)
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
      onDone?.()
      resolve(url)
    }
    xhr.onerror = () => reject(new Error('上传网络错误'))
    xhr.send(form)
  })
}
