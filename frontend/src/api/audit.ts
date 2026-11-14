import http, { toPaged, type Paged } from './request'

// 操作审计日志（只读查询）。
// 后台能改系统设置（含密钥）、重置他人密码、删除超管、启停企业，这些操作必须留痕，
// 否则出事无法回答「谁在什么时候改了什么」。
export interface AuditLog {
  id: number
  actorType: string // admin / employee / anonymous / system
  actorId: number
  actorName: string // 冗余用户名：账号删除后仍可追溯
  companyId: number
  action: string // setting.update / admin.reset_password ...
  targetType: string
  targetId: string
  detail: string // JSON 变更摘要（敏感值已脱敏为 ******）
  ip: string
  result: string // success / fail
  createdAt: string
}

// 查询审计日志（平台超管）。支持 action / actorType / actorId / result 过滤与分页。
// 与其它管理端列表同构：data 是当前页数组，total/page/size 在顶层（见 api/request.ts）。
export function listAuditLogs(params: {
  page?: number
  size?: number
  action?: string
  actorType?: string
  actorId?: string
  result?: string
}): Promise<Paged<AuditLog>> {
  return http.get('/admin/audit-logs', { params }).then((r) => toPaged<AuditLog>(r))
}

// 动作的中文名：界面给运营看，不能把 setting.update 这样的键直接摊开。
export const ACTION_LABELS: Record<string, string> = {
  'setting.update': '修改系统设置',
  'login.fail': '登录失败',
  'login.success': '登录成功',
  logout: '登出',
  'password.change': '修改自己的密码',
  // 小程序端管理员：令牌落在手机本地且可直接审批申请，绑定微信等于开一个管理员入口
  'admin.mobile_login': '管理员小程序登录',
  'admin.wx_bind': '管理员绑定微信',
  // P1 后台代解绑：解绑会踢掉该账号已签发的令牌，属安全处置动作
  'admin.wx_unbind': '解绑管理员微信',
  'employee.wx_unbind': '解绑员工微信',
  // 按完整 openid 反查绑定关系：只读但破例留痕（把微信身份关联到后台账号）
  'wechat.lookup': '按 openid 反查绑定',
  'admin.create': '新增平台超管',
  'admin.rename': '改管理员用户名',
  'admin.reset_password': '重置管理员密码',
  'admin.delete': '删除平台超管',
  'company.create': '新建企业',
  'company.update': '编辑企业',
  'company.status': '启停企业',
  'company.admin.create': '新增企业管理员',
  'company.admin.reset_password': '重置企业管理员密码',
  'employee.create': '新增员工',
  'employee.update': '编辑员工',
  'employee.reset_password': '重置员工密码',
  'employee.status': '启停员工',
  // PIPL 权利入口：注销是不可逆的（员工与其申请单/统计一并删除），导出个人信息本身也是敏感动作
  'employee.cancel': '员工本人注销账号',
  'employee.export': '导出员工个人信息',
  // 批量导出：一次拉走整批员工信息，敏感度高于单个导出，审计里要能一眼分辨
  'employee.export_batch': '批量导出员工信息',
  // 员工本人在小程序里绑定微信（含换微信号覆盖写）。绑定等于给这个微信开了免密入口，
  // 与「管理员绑定微信」分开记，便于按 actor_type 区分是谁给谁开的口子
  'employee.wx_bind': '员工绑定微信',
  'apply.approve': '通过申请',
  'apply.reject': '驳回申请',
  // 备份：执行与下载都是「数据离开系统边界」，与导出员工信息同级敏感
  'backup.run': '执行备份',
  'backup.download': '下载备份文件',
  // 商业动作：授权与配额直接决定客户能不能继续用、能占多少存储
  'company.license': '修改企业授权',
  'company.storage_quota': '设置企业存储配额',
  // 小程序默认入口决定所有访客第一眼看到谁（全员 / 某个员工）
  'mp_entry.update': '修改小程序入口',
  // 租户自有对象桶：装的是客户的云凭据，改它等于把该企业的文件流向改到另一个桶。
  // 三个动作分开记，因为「改了配置」「验了一下」「切回平台盘」后果完全不同。
  // 第六十八轮复核：三个动作与后端常量已一一对应，P4 不需要再补中文名标签；
  // 新增埋点时 audit.spec.ts 的跨端对账会直接报红（missing / extra 双向断言）
  'storage.config.update': '修改企业自有存储配置',
  'storage.config.check': '企业存储连通性自检',
  'storage.config.reset': '切回平台存储',
  'storage.config.rekey': '租户存储凭据轮换',
  'mp_release.create': '登记小程序版本',
  'mp_release.upload': '后台上传小程序代码',
  'mp_release.submit_audit': '提交小程序审核',
  'mp_release.audit_status': '查询小程序审核状态',
  'mp_release.release': '发布小程序版本',
  'mp_release.undo_audit': '撤回小程序审核',
  // 图片内容安全的处置：覆盖是不可逆的（原图当场丢失），必须留痕
  'media_check.takedown': '下架违规图片',
  // 按保留期清理审计日志（后台定时任务）。删日志本身就是最该被追责的动作，必须留痕
  'audit.purge': '按保留期清理审计日志',
  // 授权临近到期/已到期（后台定时任务）。它不是「谁做了什么」，而是「系统在什么时候
  // 提醒过谁」——这是「提醒过、客户没续」与「根本没提醒就停服了」之间唯一的证据
  'license.expiring': '授权到期提醒',
  // 后台「测试地址」：服务端按管理员填的 URL 发出向 HEAD。只读探测但属 SSRF 入口，
  // 必须留痕（谁测过哪个 host / 被拦下过几次）；审计里只记 host，不带签名 query。
  'url_probe.check': '测试视频地址可达性',
  // 按保留期清理埋点原始数据（后台定时任务）。删统计原始数据不可逆，必须留痕
  'stat.purge': '按保留期清理埋点数据',
  // CDN 缓存刷新失败（覆盖写后让边缘旧缓存失效）。异步 best-effort，失败不阻断主流程，
  // 但「文件改了 CDN 却没刷」会让线上仍是旧图，故失败单独留痕；Detail 只记 url_count，不写错误原文或 URL
  'cdn.refresh': 'CDN 缓存刷新失败',
}

// 审计日志只查不写：记录一旦可改就等于没有审计，因此不提供任何写接口。
export function actionLabel(action: string): string {
  return ACTION_LABELS[action] || action
}
