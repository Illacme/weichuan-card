import { describe, it, expect } from 'vitest'
import {
  DRIVER_GUIDES, CDN_GUIDES, S3_VARIANTS, SETUP_STEPS, MENU_GUIDES, TAB_GUIDES,
  STORAGE_FIELD_GUIDES, DELIVERY_FIELD_GUIDES, ARCHITECTURE_POINTS, DEFAULT_FACTS,
  STORAGE_GLOSSARY, driverGuide, cdnGuide,
} from './storageGuides'

// 这份表是「用户能自己配通」的底线：少一家地址、少一个字段说明，
// 用户就得去搜索引擎猜。所以按平台逐个守，而不是只看总数。

const officialHosts = [
  'aliyun.com', 'tencent.com', 'qiniu.com', 'cloudflare.com',
  'aws.amazon.com', 'huaweicloud.com', 'min.io',
]

function assertGuide(guide: { key: string; label: string; summary: string; when: string; credential: string; links: { label: string; url: string }[] }, minLinks: number) {
  expect(guide.label, `${guide.key} 缺显示名`).toBeTruthy()
  expect(guide.summary, `${guide.key} 缺一句话说明`).toBeTruthy()
  expect(guide.when, `${guide.key} 缺适用场景`).toBeTruthy()
  expect(guide.credential, `${guide.key} 缺凭据获取说明`).toBeTruthy()
  expect(guide.links.length, `${guide.key} 的官方地址不足`).toBeGreaterThanOrEqual(minLinks)
  for (const link of guide.links) {
    expect(link.url, `${guide.key} / ${link.label} 不是 https 地址`).toMatch(/^https:\/\//)
    expect(officialHosts.some((h) => link.url.includes(h)), `${link.url} 不是已知厂商官方域名`).toBe(true)
  }
}

describe('存储模块的产品化事实表', () => {
  it('每一种存储驱动都有用户可懂的说明；自建 MinIO 以外的都给出官方控制台与凭据地址', () => {
    expect(Object.keys(DRIVER_GUIDES).sort()).toEqual(['local', 'minio', 'oss', 's3'])
    for (const guide of Object.values(DRIVER_GUIDES)) {
      assertGuide(guide, guide.key === 'local' ? 0 : guide.key === 'minio' ? 2 : 3)
    }
    // 本地盘是默认，绝不能塞一个"去某云注册"的链接吓人
    expect(DRIVER_GUIDES.local.links).toHaveLength(0)
    expect(DRIVER_GUIDES.local.summary).toContain('服务器')
  })

  it('每一种 CDN 厂商都有官方地址，关闭状态不要求任何凭据', () => {
    expect(Object.keys(CDN_GUIDES).sort()).toEqual(['aliyun', 'cloudflare', 'cloudfront', 'none', 'qiniu', 'tencent'])
    for (const guide of Object.values(CDN_GUIDES)) {
      assertGuide(guide, guide.key === 'none' ? 0 : 3)
    }
    expect(CDN_GUIDES.none.links).toHaveLength(0)
  })

  it('每家第三方平台的控制台 / 密钥 / 文档入口都点名给出，不只放一个首页链接', () => {
    const links = (g: { links: { label: string; url: string }[] }) => g.links.map((l) => `${l.label} ${l.url}`).join(' ')
    expect(links(DRIVER_GUIDES.oss)).toContain('oss.console.aliyun.com')
    expect(links(DRIVER_GUIDES.oss)).toContain('ram.console.aliyun.com/manage/ak')
    expect(links(DRIVER_GUIDES.s3)).toContain('console.cloud.tencent.com/cos')
    expect(links(DRIVER_GUIDES.s3)).toContain('console.cloud.tencent.com/cam/capi')
    expect(links(DRIVER_GUIDES.s3)).toContain('s3.console.aws.amazon.com')
    expect(links(DRIVER_GUIDES.s3)).toContain('dash.cloudflare.com')
    expect(links(DRIVER_GUIDES.s3)).toContain('portal.qiniu.com/user/key')
    expect(links(DRIVER_GUIDES.s3)).toContain('console.huaweicloud.com/obs')
    expect(links(CDN_GUIDES.cloudflare)).toContain('dash.cloudflare.com/profile/api-tokens')
    expect(links(CDN_GUIDES.cloudflare)).toContain('developers.cloudflare.com/cache/how-to/purge-cache')
    expect(links(CDN_GUIDES.cloudfront)).toContain('console.aws.amazon.com/cloudfront')
    expect(links(CDN_GUIDES.cloudfront)).toContain('private-content-trusted-signers.html')
    expect(links(CDN_GUIDES.qiniu)).toContain('developer.qiniu.com/fusion')
  })

  it('S3 兼容厂商的端点示例覆盖五家，且是不带协议头的主机形式', () => {
    expect(S3_VARIANTS.map((v) => v.label)).toEqual([
      '腾讯云 COS', 'AWS S3', 'Cloudflare R2', '七牛云 Kodo', '华为云 OBS',
    ])
    for (const v of S3_VARIANTS) {
      expect(v.endpoint).not.toContain('https://')
      expect(v.endpoint).toContain('.')
      expect(v.note).toBeTruthy()
    }
  })

  it('存储与分发表单的每一项都有功能说明，密钥类还说明去哪拿', () => {
    for (const key of ['name', 'driver', 'endpoint', 'bucket', 'region', 'baseUrl', 'hostHeader', 'useSsl', 'publicRead', 'accessKey', 'secretKey', 'localDir']) {
      expect(STORAGE_FIELD_GUIDES[key]?.usage, `存储字段 ${key} 缺说明`).toBeTruthy()
    }
    for (const key of ['name', 'provider', 'domain', 'endpoint', 'resourceId', 'accessKey', 'secretKey']) {
      expect(DELIVERY_FIELD_GUIDES[key]?.usage, `分发字段 ${key} 缺说明`).toBeTruthy()
    }
    expect(STORAGE_FIELD_GUIDES.accessKey?.source).toContain('控制台')
    expect(STORAGE_FIELD_GUIDES.endpoint?.source).toContain('控制台')
    expect(DELIVERY_FIELD_GUIDES.resourceId?.source).toContain('Cloudflare')
  })

  it('架构叙事、默认事实、配置流程、术语与菜单说明都在，且流程能跳到真实页签', () => {
    expect(ARCHITECTURE_POINTS).toHaveLength(3)
    expect(DEFAULT_FACTS.join('')).toContain('初始状态：本地磁盘')
    expect(DEFAULT_FACTS.join('')).toContain('不填任何云账号')
    expect(STORAGE_GLOSSARY.length).toBeGreaterThanOrEqual(6)
    const tabs = new Set(['overview', 'storage', 'delivery', 'refresh', 'migration', 'binding', 'platform'])
    expect(SETUP_STEPS).toHaveLength(5)
    for (const step of SETUP_STEPS) {
      expect(tabs.has(step.tab), `流程第 ${step.no} 步指向不存在的页签`).toBe(true)
      expect(step.when).toBeTruthy()
      expect(step.body.length).toBeGreaterThan(20)
    }
    for (const path of ['/platform/storage-profiles', '/platform/backup', '/platform/settings']) {
      expect(MENU_GUIDES[path]?.desc, `菜单 ${path} 缺悬停说明`).toBeTruthy()
    }
    for (const key of tabs) expect(TAB_GUIDES[key]?.desc, `页签 ${key} 缺说明`).toBeTruthy()
    expect(TAB_GUIDES.refresh.title).toBe('CDN 缓存刷新')
  })

  it('未知驱动 / 厂商返回 null，界面不会拿别家的凭据说明冒充', () => {
    expect(driverGuide('unknown')).toBeNull()
    expect(cdnGuide('unknown')).toBeNull()
    expect(driverGuide('local')?.links).toHaveLength(0)
    expect(cdnGuide('none')?.links).toHaveLength(0)
  })
})
