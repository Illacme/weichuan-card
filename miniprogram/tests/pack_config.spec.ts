// 打包配置守门：**测试代码与文档不得进包**。
//
// 为什么值得单独一条用例：`miniprogram/tests/` 有 470 KB 左右，`scripts/` 还有脚本，
// 它们被打进包不会报错、不会变丑，只会让主包白白胖一圈——而且每次新增一个 spec
// 都在继续长，没人会注意到。主包上限只有 2 MB，这部分占掉的是真金白银的额度。
//
// 这条配置第一次被发现是空的（`packOptions.ignore: []`），也就是说此前的上传
// 一直把 35 个 spec 一起发了出去。删掉 ignore 不会让任何功能坏掉，所以必须钉住。
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')

function packIgnore(): Array<{ type: string; value: string }> {
  const cfg = JSON.parse(readFileSync(resolve(root, 'project.config.json'), 'utf8'))
  return (cfg.packOptions && cfg.packOptions.ignore) || []
}

describe('小程序打包配置', () => {
  it('tests 目录不进包（470 KB 的 spec 不该发给用户）', () => {
    const hit = packIgnore().some(
      (r) => r.type === 'folder' && String(r.value).replace(/\/+$/, '') === 'tests'
    )
    expect(hit).toBe(true)
  })

  it('scripts 目录与 README 不进包', () => {
    const vals = packIgnore().map((r) => r.type + ':' + r.value)
    expect(vals).toContain('folder:scripts')
    expect(vals).toContain('file:README.md')
  })
})
