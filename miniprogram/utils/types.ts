// 小程序端消费的接口信封类型（与后端 JSON 对齐）。
//
// 为什么单独成文件：小程序 tsconfig 是 `strict: false`（没有 wx typings，裸 tsc 有
// 大量基线错误），类型错误**不阻塞构建**，只在 IDE 与 code review 时起作用。
// 代价是它很容易悄悄腐烂：本文件上一版整份 12 个类型**零引用**，且字段早已过时
// （`Employee` 缺 `phone2`、`CompanyInfo` 缺 `mpEntryMode`/`mpEntryStaffId`、
// `HomeLayoutResp` 缺 `bgImage`/`bgMask`），描述的是一份不存在的契约。
//
// 现在只保留首页 `loadAll` **真正消费**的四个信封，并被
// `pages/index/index.ts` 的 `request<CardResp>` 等调用点引用；
// `miniprogram/tests/types_contract.spec.ts` 会把这些字段与
// `backend/internal/model/models.go` 的 json tag 逐项比对，写错字段名（例如把
// `phone2` 写成 `phoneTwo`）会直接让测试变红。
//
// 完整接口契约仍以 `docs/openapi.yaml` 为准；这里只放「端上真的在用」的那部分，
// 不做全量镜像（镜像必然再次腐烂）。
//
// **四个接口的字段都在响应根级，不在 data 里**：
//   /api/card         → employee / company / profile
//   /api/achievements → categories / featured
//   /api/home-layout  → order / bgImage / bgMask
// 误读成 `card.data.*` 会让名片页恒走 notFound——这个 bug 已经真实发生过一次。
import type { ApiResult } from './request'

/** 不带 data 的通用信封（根级字段接口用），与 request.ts 的 ApiResult 同源 */
type Envelope = Omit<ApiResult<unknown>, 'data'>

/** /api/card 根级 employee（model.Employee 全量序列化，PII 已由后端解密为明文） */
export interface Employee {
  staffId: number
  companyId: number
  title: string
  avatar: string
  /** 座机（办公号）；为空时名片「座机」行回退企业总机 company.tel */
  landline: string
  openid: string
  status: string
  createdAt: string
  name: string
  /** 手机（主号，同时是登录账号） */
  phone: string
  /** 手机 2（选填）：名片「手机」行下方并列展示，不参与登录 */
  phone2: string
  email: string
  /** 微信昵称（登录时授权采集） */
  nickname: string
}

/** /api/card 根级 company（model.CompanyInfo） */
export interface CompanyInfo {
  id: number
  companyId: number
  fullName: string
  /** 企业简称（小程序顶栏居中展示） */
  shortName: string
  address: string
  tel: string
  logo: string
  /** 裸启动落地：wall（名片墙，缺省）/ staff（直接进指定员工名片） */
  mpEntryMode: string
  mpEntryStaffId: number
}

/**
 * /api/card 根级 profile（model.CompanyProfile）。
 *
 * videoSource 五档：none（封面占位）/ channels（视频号）/ upload（云存储上传）/
 * url（外部直链）/ tencent（腾讯视频插件，需部署侧开 MP_TENCENT_VIDEO 才可选）。
 * videoUrl 由 upload、url 两档共用，并作为 channels / tencent 开启 videoFallbackUrl
 * 时的兜底源；videoBackupUrl 是自托管 MP4 档的备用地址（主源失败时切换）。
 */
export interface CompanyProfile {
  id: number
  companyId: number
  introText: string
  videoSource: string
  videoChannelId: string
  /** feedId（同主体，可自动播放）与 feedToken（跨主体，强制不能自动播放）二选一 */
  videoFeedId: string
  videoFeedToken: string
  videoVid: string
  videoUrl: string
  videoFallbackUrl: boolean
  /** 自托管 MP4（upload / url）档的备用地址，空串=没配 */
  videoBackupUrl: string
  /** 进页面即自动播放；默认 false（腾讯视频插件默认 true，由字段显式压住） */
  videoAutoplay: boolean
  cover: string
}

/** /api/card 整个响应信封 */
export type CardResp = Envelope & {
  employee?: Employee
  company?: CompanyInfo
  profile?: CompanyProfile
}

/** /api/achievements 的 categories 元素（model.AchievementCategory） */
export interface Category {
  id: number
  companyId: number
  name: string
  icon: string
  sort: number
  isLocked: boolean
}

/**
 * 首页精选案例（/api/achievements 的 featured 元素）。
 * 后端用 projectBriefCols 裁剪成 5 列（不含 detail / album），别在这里补字段。
 */
export interface FeaturedProject {
  id: number
  categoryId: number
  name: string
  summary: string
  cover: string
}

/** /api/achievements 整个响应信封 */
export type AchievementsResp = Envelope & {
  categories?: Category[]
  featured?: FeaturedProject[]
}

/**
 * 首页服务卡（/api/services 的 data 元素）。
 * 后端用 serviceBriefCols 裁剪成 4 列（不含 content 富文本），列表页不需要正文。
 */
export interface ServiceBrief {
  id: number
  title: string
  icon: string
  sort: number
}

/** /api/services：标准 data 数组信封 */
export type ServicesResp = ApiResult<ServiceBrief[]>

/** /api/home-layout 整个响应信封（order / bgImage / bgMask 均在根级） */
export type HomeLayoutResp = Envelope & {
  /** 首页区块顺序，缺省 ['achievement', 'service'] */
  order?: string[]
  bgImage?: string
  /** 背景遮罩浓度 0..100 */
  bgMask?: number
}
