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
