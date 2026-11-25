// 换钥口径的文档守门（纯读磁盘源码，不 import 任何生产模块）。
//
// 为什么要盯文档：上线 checklist / 部署 runbook 是运维当场照着做的东西。
// 第七十一轮曾有一条 `server tenant-storage-rekey`，只重加密租户云凭据、
// 不覆盖业务 PII；第七十五轮它随旧存储模型一起删除（档案凭据改为明文入库）。
//
// 于是现在的口径是**单向**的：业务 PII 没有任何 re-key 通道，换钥即不可逆。
// 三种漂移都不会报编译错、也不会让别的测试变红，只会在某次事故里被人发现：
//   ① 手册里又出现 `tenant-storage-rekey`（运维会去敲一条不存在的命令）；
//   ② 手册把「没有 re-key」写成「有 re-key」（运维以为换钥能救数据）；
//   ③ 手册干脆不提换钥不可逆（运维换钥前没备份，事后无从补救）。
//
// 只盯**当前有效指引**（launch-checklist / deploy-runbook / 设计稿），
// 不碰 commercial-readiness.md 的历史轮次记录——那些是当时的口径，
// 靠文末「历史轮次留档说明」统一覆盖。
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../../..')
const DOCS = {
  checklist: resolve(REPO, 'docs/launch-checklist.md'),
  runbook: resolve(REPO, 'docs/deploy-runbook.md'),
  design: resolve(REPO, 'docs/tenant-storage-design.md'),
}

const read = (p: string): string => readFileSync(p, 'utf8')

// 会直接误导运维的绝对表述（订正前这几句确实写在手册里）。
const STALE = [
  '没有 re-key 机制',
  '本项目没有 re-key',
  'gofmt -l ./... 当前 0 文件',
  '凭据 re-key 命令不覆盖业务表',
]

// 「业务 PII 无 re-key」的各种写法。三份手册至少各命中一种。
const NO_REKEY = /没有 re-key|无 re-key|没有任何 re-key/

describe('换钥口径：当前有效运维文档的现状描述', () => {
  it('量尺自己有读数：三份文档都读到内容，且确实是 PII 相关手册', () => {
    for (const [name, p] of Object.entries(DOCS)) {
      const src = read(p)
      expect(src.length, `${name} 读到的内容过短，守门可能空转`).toBeGreaterThan(1000)
      expect(src, `${name} 不是 PII 手册？`).toContain('PII_ENC_KEY')
    }
  })

  it('每份手册都写明「业务 PII 没有 re-key 通道」', () => {
    for (const [name, p] of Object.entries(DOCS)) {
      expect(NO_REKEY.test(read(p)), `${name} 没写清业务 PII 换钥不可逆`).toBe(true)
    }
  })

  it('已删除的换钥命令不得作为可用命令出现', () => {
    // checklist / runbook 是「照着敲」的手册：出现命令名就是误导。
    for (const name of ['checklist', 'runbook'] as const) {
      expect(read(DOCS[name]), `${name} 还在提已删除的换钥命令`).not.toContain('tenant-storage-rekey')
    }
    // 设计稿允许保留历史记录，但必须同句说明「已删除」，不能只留一个命令名。
    const design = read(DOCS.design)
    if (design.includes('tenant-storage-rekey')) {
      expect(design, '设计稿提到旧命令却没说明已删除').toContain('已随旧模型一并删除')
    }
  })

  it('不再出现订正前的绝对表述（写了就是口径回退）', () => {
    for (const [name, p] of Object.entries(DOCS)) {
      const src = read(p)
      const found = STALE.filter((s) => src.includes(s))
      expect(found, `${name} 又写回了过时口径：${found.join('、')}`).toEqual([])
    }
    // STALE 清单本身要有读数，否则循环退化成空数组恒绿
    expect(STALE.length).toBeGreaterThanOrEqual(4)
  })

  it('设计稿首版「尚未落地」已标注为历史状态', () => {
    expect(read(DOCS.design)).toContain('当时尚未落地')
    expect(read(DOCS.design)).toContain('现状订正')
  })
})

// A1（第八十六轮）把平台连接信息 / CDN 凭据改成保存即生效；第八十八轮三端对齐时
// 又发现 design 文档的「启动链」段与 CDN 表格仍写「启动期构造、改完重启」，
// 是当时漏改的现状描述（不是历史轮次）。这些句子一贴回去，管理员就会明明改了
// 却不去生效、或者反过来每次改凭据都白重启一次。只拦这几句整句，历史轮次保留。
describe('平台实例生效口径：现状描述不回退到「改完重启」', () => {
  const STALE_NOW = [
    '平台实例仍是**启动期构造、运行期不可变**',
    '必须重启才会换进程里的实例',
    '启动期构造刷新器，**改完重启**',
  ]

  it('量尺有读数：design 文档确实含现状锚点，否则下面两条会空转', () => {
    const design = read(DOCS.design)
    expect(design).toContain('按 `platform_primary` 内置档案的 `version`')
    expect(design).toContain('按该档案 version 现建刷新器')
  })

  it('现状区的旧「改完重启」话术没有回贴', () => {
    const design = read(DOCS.design)
    const found = STALE_NOW.filter((s) => design.includes(s))
    expect(found, `design 现状描述又写回了旧口径：${found.join('、')}`).toEqual([])
  })

  it('产品决策：到期只读 = 不能改但能带走数据，且名读实写的预签名仍要拦', () => {
    // 只读拦导出只有合规风险、没有商业收益；导出放行不是漏拦。
    // GET 预签名直传返回上传 URL，拿到就能覆盖对象——不点名它会让「已只读」形同虚设。
    const enforce = readFileSync(resolve(REPO, 'backend/internal/handler/license_enforce.go'), 'utf8')
    expect(enforce).toContain('"/api/admin/upload/presign"')
    expect(enforce).toContain('"/api/employee/upload/presign"')
    expect(enforce).toContain('数据可携带权')

    const consequence = readFileSync(resolve(REPO, 'backend/internal/license/expiry.go'), 'utf8')
    expect(consequence).toContain('导出带走自己的数据')

    for (const name of ['checklist', 'runbook'] as const) {
      const src = read(DOCS[name])
      expect(src, `${name} 丢了「导出放行」的产品决策`).toContain('导出')
      expect(src, `${name} 没写明预签名是名读实写`).toContain('预签名')
    }
  })

  it('授权回退：无行回退全局是有意兼容，不许改成 fail-closed', () => {
    // 存量企业升级后没有 tenant_license 行；改成「查不到就拒绝」会让整批存量
    // 客户上线即事故。前端企业列表也有「未配置授权 = 不受限制」提示。
    const resolver = readFileSync(resolve(REPO, 'backend/internal/license/tenant_db.go'), 'utf8')
    expect(resolver).toContain('上线即事故')
    expect(resolver).toContain('return Current()')

    const companies = readFileSync(resolve(REPO, 'frontend/src/views/Companies.vue'), 'utf8')
    expect(companies).toContain('未配置授权 = 不受席位与到期限制')
  })

  it('旧版兼容盘点：死注释不回贴，真实存量保护不误删', () => {
    // 已发布过 MIT 版本，历史裸键 / 七牛直链 / 未绑定回落 / env 首次播种
    // 都保护真实存量数据，删了会让老用户裂图或丢配置——这些必须留。
    const owner = readFileSync(resolve(REPO, 'backend/internal/model/storage_owner.go'), 'utf8')
    expect(owner, 'storage_owner 又写回「旧表并存」死注释').not.toContain('两套并存的数据源')
    expect(owner).toContain('唯一真值源是 storage_profiles + storage_bindings')

    const design = read(DOCS.design)
    expect(design, '0059 未兑现的「清理旧 key」承诺又写回现状描述').not.toContain('随即清理那批历史')
    expect(design).toContain('并未实现')

    // STORAGE_REF_STRICT 回滚开关已随「线上无存量」删除，代码和当前文档都不许再提。
    const resourceref = readFileSync(resolve(REPO, 'backend/internal/handler/resourceref.go'), 'utf8')
    expect(resourceref).not.toContain('STORAGE_REF_STRICT')
    for (const p of [DOCS.design, resolve(REPO, 'backend/README.md'),
      resolve(REPO, 'docs/tenant-storage-url-boundaries.md')]) {
      expect(read(p), `${p} 又把回滚开关写回当前口径`).not.toContain('STORAGE_REF_STRICT=0')
    }

    const tenantDB = readFileSync(resolve(REPO, 'backend/internal/license/tenant_db.go'), 'utf8')
    expect(tenantDB).toContain('上线即事故') // fail-open 是新部署空表所需，不是旧版兼容

    // 第九十二轮按「线上无存量」重新拍板：扁平 templateId / splitTheme 已删除，
    // 后端 0043 也不再下发该列。不许以「老用户零迁移」为由把它贴回来。
    const theme = readFileSync(resolve(REPO, 'miniprogram/utils/theme.ts'), 'utf8')
    expect(theme).not.toContain('splitTheme')
    expect(theme).not.toContain('templateId?:')
    expect(theme).toContain('线上无存量')
  })

  it('设置项说明也只把重启留给目录类', () => {
    const setting = readFileSync(resolve(REPO, 'backend/internal/setting/setting.go'), 'utf8')
    const ptr = setting.match(/\{Key: "platform_storage_profile_id"[\s\S]*?\},/)?.[0] ?? ''
    expect(ptr, '指针设置项没找到，守门空转').toBeTruthy()
    expect(ptr).toContain('保存即生效、无需重启')
    expect(ptr).not.toContain('保存后重启服务')

    const settingsVue = readFileSync(resolve(REPO, 'frontend/src/views/platform/Settings.vue'), 'utf8')
    expect(settingsVue).not.toContain('改完文件存储也要重启服务才生效')
    expect(settingsVue).not.toContain('改完任一边都要重启服务')
    expect(settingsVue).toContain('保存即生效、无需重启')
  })
})

// 第九十六轮：上线判据文档的事实漂移守门。
// checklist / branch-protection 是运维当场照着做的东西，写错数字或过期动作
// 会让运维做多余重启、少勾 required check、或漏同步迁移目录——三者都不报编译错。
// commercial-readiness.md 的历史轮次原文按惯例保留，不进本守门。
describe('上线判据文档：数字与生效口径不回退', () => {
  const RUNTIME_DOCS = {
    checklist: resolve(REPO, 'docs/launch-checklist.md'),
    branchProtection: resolve(REPO, 'docs/branch-protection.md'),
    readme: resolve(REPO, 'README.md'),
    handover: resolve(REPO, '部署与联调.md'),
  }

  it('量尺有读数：四份文档都读到内容，且确实是运维判据文档', () => {
    for (const [name, p] of Object.entries(RUNTIME_DOCS)) {
      const src = read(p)
      expect(src.length, `${name} 没读到内容，守门空转`).toBeGreaterThan(200)
      expect(src).toContain('CI')
    }
  })

  it('CI job 数对齐真实 workflow：四份文档都写 15，不许回退到 12', () => {
    const ci = read(resolve(REPO, '.github/workflows/ci.yml'))
    const jobCount = (ci.match(/^ {2}[a-z0-9-]+:\n {4}name:/gm) ?? []).length
    expect(jobCount, 'CI job 数变了，守门数字需要重新核对').toBe(15)
    for (const [name, p] of Object.entries(RUNTIME_DOCS)) {
      const src = read(p)
      expect(src, `${name} 仍写旧 job 数`).not.toMatch(/12 个 job|12 个.*job/)
      expect(src, `${name} 未写当前 job 数`).toContain('15 个')
    }
  })

  it('CDN 凭据生效口径：checklist 不许写「自检通过后重启」', () => {
    const checklist = read(RUNTIME_DOCS.checklist)
    const row = checklist.split('\n').find((l) => l.includes('CDN 刷新凭据')) ?? ''
    expect(row, 'CDN 凭据行没找到，守门空转').toBeTruthy()
    expect(row).toContain('保存即生效、无需重启')
    expect(row).not.toContain('自检通过后重启')
  })

  it('迁移目录同步口径：checklist 不许再写「整个 migrations」', () => {
    const checklist = read(RUNTIME_DOCS.checklist)
    expect(checklist).not.toContain('同步**整个** `backend/migrations/`')
    expect(checklist).toContain('--exclude=history/')
    expect(checklist).toContain('自动切 `history/`')
  })

  it('三档判据的重启说法与「保存即生效」不冲突', () => {
    const readme = read(RUNTIME_DOCS.readme)
    expect(readme).not.toContain('改回来重启即可')
    expect(readme).toContain('改回来即刻生效')
  })

  it('branch-protection 表格 job 数与 CI 对齐，且含三处新补的门禁', () => {
    const doc = read(RUNTIME_DOCS.branchProtection)
    const tableRows = (doc.match(/^\| `[^`]+` \|/gm) ?? []).length
    expect(tableRows, '表格 job 行数与 CI 不一致').toBe(15)
    expect(doc).toContain('`审计动作 ↔ 中文名对账`')
    expect(doc).toContain('`OpenAPI 契约对账`')
    expect(doc).toContain('`小程序启动标题同步`')
    expect(doc).toContain('51 条断言')
  })
})
