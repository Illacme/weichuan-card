// 换钥口径的文档守门（纯读磁盘源码，不 import 任何生产模块）。
//
// 为什么要盯文档：上线 checklist / 部署 runbook 是运维当场照着做的东西。
// 里头一句「本项目没有 re-key 机制」在第七十一轮之前是对的，但现在有了
// server tenant-storage-rekey——只看前半句会以为「换钥能救业务 PII」，
// 而那条命令实际只重加密 tenant_storage_configs 的云凭据。
// 反过来，若手册把「没有任何 re-key」当成现状，运维又会漏掉凭据换钥能力。
// 这类漂移不会报编译错、也不会让测试变红，只会在某次事故里被人发现。
//
// 只盯**当前有效指引**（launch-checklist / deploy-runbook / 设计稿顶部状态），
// 不碰 commercial-readiness.md 的历史轮次记录——那些是当时的口径，
// 靠文末「现状订正」章节统一覆盖。
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
  '（无 re-key）',
  'gofmt -l ./... 当前 0 文件',
]

describe('换钥口径：当前有效运维文档的现状描述', () => {
  it('量尺自己有读数：三份文档都读到内容，且确实是 PII 相关手册', () => {
    for (const [name, p] of Object.entries(DOCS)) {
      const src = read(p)
      expect(src.length, `${name} 读到的内容过短，守门可能空转`).toBeGreaterThan(1000)
      expect(src, `${name} 不是 PII 手册？`).toContain('PII_ENC_KEY')
    }
  })

  it('每份手册都点名 re-key 命令，并写明「不覆盖业务 PII」的边界', () => {
    for (const [name, p] of Object.entries(DOCS)) {
      const src = read(p)
      expect(src, `${name} 没提到换钥命令`).toContain('tenant-storage-rekey')
      const hasBoundary = /不覆盖(业务 PII|业务表|`employees`)|业务 PII 无 re-key|业务 PII 的时间约束不变/.test(src)
      expect(hasBoundary, `${name} 没写清「凭据 re-key 不覆盖业务 PII」`).toBe(true)
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
