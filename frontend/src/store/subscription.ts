// stores/subscription.ts
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { apiFetch } from '../utils/api'

export interface SubscriptionInfo {
  upload: number
  download: number
  total: number
  expire: number
  updatedAt: string | null
}

export interface SubscriptionItem {
  name: string
  url: string
  update_interval: number
  health_interval: number
  prefix: string
  info?: SubscriptionInfo | null
}

export interface SubscriptionConfigData {
  proxy_port: number
  panel_port: number
  panel_secret: string
  rule_group: string
  ui_panel: string
  meta_backend_url: string
  mode: string
  active_subscription: string
  subscriptions: SubscriptionItem[]
  tproxy_port: number
}

export const useSubscriptionStore = defineStore('subscription', () => {
  // 订阅配置参数
  const currentConfig = ref<SubscriptionConfigData>({
    proxy_port: 7890,
    panel_port: 9090,
    panel_secret: '',
    rule_group: 'base',
    ui_panel: 'metacubexd',
    meta_backend_url: '',
    mode: 'merge',
    active_subscription: '', 
    subscriptions: [],
    tproxy_port: 7898
  })

  // 已保存应用的订阅名称白名单
  const savedSubNames = ref<Set<string>>(new Set())

  const isConfigLoaded = ref(false)  // 新增：标记是否已加载
  let loadPromise: Promise<void> | null = null  // 新增：防止并发重复请求

  // 获取订阅中心配置（带缓存）
  //
  // 注意：force=false 且已加载过时是**空操作**（直接 return），不会发起请求。
  // 需要拿到最新数据的调用方（保存后刷新、更新轮询等）必须 force=true
  // 或改用 refreshConfig()，否则前端会一直停留在本地旧快照。
  const loadConfig = async (force = false) => {
    // 如果已加载且非强制刷新，直接返回
    if (isConfigLoaded.value && !force) {
      return
    }
    // 如果正在加载中，复用同一个 Promise
    if (loadPromise) {
      return loadPromise
    }

    loadPromise = (async () => {
      try {
        const resp = await apiFetch('/subscribe/config')
        if (resp.ok) {
          const cfg = await resp.json()
          const subs = cfg.subscriptions || []
          savedSubNames.value = new Set(subs.map((s: any) => s.name))
          currentConfig.value = {
            proxy_port: cfg.proxy_port || 7890,
            panel_port: cfg.panel_port || 9090,
            panel_secret: cfg.panel_secret || '',
            rule_group: cfg.rule_group || 'base',
            ui_panel: cfg.ui_panel || 'metacubexd',
            meta_backend_url: cfg.meta_backend_url || '',
            mode: cfg.mode || 'merge',
            active_subscription: cfg.active_subscription || '',
            tproxy_port: cfg.tproxy_port ?? 7898,
            subscriptions: subs.map((s: any) => {
              const info = s.subscription_info ? {
                upload: s.subscription_info.upload || 0,
                download: s.subscription_info.download || 0,
                total: s.subscription_info.total || 0,
                expire: s.subscription_info.expire || 0,
                updatedAt: s.updated_at || null,
              } : null
              return { ...s, info }
            })
          }
          isConfigLoaded.value = true
        }
      } catch (e) {
        console.error('加载订阅配置失败', e)
        throw e
      } finally {
        loadPromise = null
      }
    })()

    return loadPromise
  }

  // 强制刷新配置（用于订阅更新、保存等操作后）
  const refreshConfig = async () => {
    isConfigLoaded.value = false
    await loadConfig(true)
  }

  return {
    currentConfig,
    savedSubNames,
    loadConfig,
    isConfigLoaded,
    refreshConfig,
  }
})