import { defineStore } from 'pinia'
import { getPublicSettings } from '@/api/settings'

// 系统设置的「公开子集」缓存：品牌名等展示位从后端取，避免把品牌名写死在前端。
//
// 为什么缓存到 localStorage：每次进入后台都等接口返回会让侧边栏品牌名先闪一下占位。
// 这里用「上次已知值先渲染 + 后台静默刷新」的策略，既即时又最终一致；
// 接口不可用（未登录 / 后端未升级）时回落到内置默认「微传名片」，不阻塞界面。
export const useSettingsStore = defineStore('settings', {
  state: () => ({
    brandName: localStorage.getItem('sys_brand_name') || '',
    // 登录页白标品牌名：仅由带 company 参数的公开接口填充；为空时回落全局品牌
    loginBrandName: '',
    loaded: false,
  }),
  getters: {
    // 品牌名：未配置 / 接口不可用回落内置默认
    displayBrandName: (s): string => s.brandName || '微传名片',
    // 登录页品牌名：优先白标码命中的企业简称，否则回落全局品牌名（再回落内置默认）。
    // 与 displayBrandName 分开：后台侧边栏永远用全局品牌，登录页才用白标专属品牌。
    // 引用另一个 getter 必须用 this（getter 不在 state 上）。
    loginDisplayBrand(): string {
      return this.loginBrandName || this.displayBrandName
    },
  },
  actions: {
    // 拉取公开设置并刷新本地缓存（force=true 用于「系统设置」保存后立刻同步品牌名）。
    // company 为登录页白标码（?company=）；传入时取 login_brand_name 填充 loginBrandName，
    // 未命中回落''→走全局品牌；不传时保持 loginBrandName 不变（不干扰其它页面的全局品牌）。
    async load(company?: string, force = false) {
      if (this.loaded && !force && !company) return
      this.loaded = true
      try {
        const r = await getPublicSettings(company)
        if (r.code === 0 && r.data) {
          this.brandName = r.data.brand_name || ''
          localStorage.setItem('sys_brand_name', this.brandName)
          if (company) {
            this.loginBrandName = r.data.login_brand_name || ''
          }
        }
      } catch {
        // 网络异常 / 后端未升级：保留本地缓存或默认值，不阻塞界面
      }
    },
    // 「系统设置」页保存品牌名后即时更新侧边栏，无需刷新页面
    setBrandName(v: string) {
      this.brandName = v
      localStorage.setItem('sys_brand_name', v)
    },
  },
})
