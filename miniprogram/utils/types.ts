// 与后端 JSON 字段对齐的 TS 类型定义

export interface Employee {
  staffId: number
  name: string
  title: string
  avatar: string
  phone: string
  /** 座机（本人办公电话）；为空时名片「座机」行回退公司总机 company.tel */
  landline: string
  email: string
  status: string
}

export interface CompanyInfo {
  fullName: string
  address: string
  tel: string
  logo: string
}

// videoSource 五档：none（封面占位）/ channels（视频号）/ upload（云存储上传）/
// url（外部直链）/ tencent（腾讯视频插件，需部署侧开 MP_TENCENT_VIDEO 才可选）。
// videoUrl 由 upload、url 两档共用，并作为 channels / tencent 开启 videoFallbackUrl 时的兜底源。
export interface CompanyProfile {
  introText: string
  videoSource: string
  videoChannelId: string
  // feedId（同主体，可自动播放）与 feedToken（跨主体，强制不能自动播放）二选一，feedId 优先
  videoFeedId: string
  videoFeedToken: string
  videoVid: string
  videoUrl: string
  videoFallbackUrl: boolean
  cover: string
}

export interface CardResp {
  employee?: Employee
  company?: CompanyInfo
  profile?: CompanyProfile
}

export interface Category {
  id: number
  name: string
  icon: string
  sort: number
}

export interface FeaturedProject {
  id: number
  name: string
  cover: string
  categoryId: number
}

export interface AchievementsResp {
  categories?: Category[]
  featured?: FeaturedProject[]
}

export interface ProjectListItem {
  id: number
  name: string
  cover: string
  summary: string
  categoryId: number
}

export interface ProjectDetail {
  id: number
  name: string
  summary: string
  detail: string // 富文本，含 <wx-video channel-id="..."> 块
  cover: string
  album: string[]
  categoryId: number
}

export interface ServiceListItem {
  id: number
  title: string
}

export interface ServiceDetail {
  id: number
  title: string
  content: string // 富文本，含视频号块
  updatedAt?: string
}

export interface HomeLayoutResp {
  order?: string[]
}
