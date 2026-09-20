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

/**
 * 切换模式下挂在某个订阅上的单条自定义规则。
 *
 * 与后端 config.CustomRule 一一对应：存的是结构化字段而非拼好的规则文本，
 * 规则行由后端按内核语法组装（用户输入的逗号/空格无法破坏规则行结构）。
 */
export interface CustomRule {
  id: string
  type: string          // 规则类型，如 'DOMAIN-SUFFIX'
  payload: string       // 规则载荷，如 'example.com'
  target: string        // 该订阅的代理组/代理节点名，或 'DIRECT' | 'REJECT' | 'PASS'
  position: string      // 'before'（默认，插在最前）| 'after'（插在最后一条 MATCH 之前）
  no_resolve?: boolean
  // 以下两个字段仅由后端在响应中返回，请求体不需要
  line: string          // 后端组装好的规则行，用于列表展示
  valid: boolean
  reason?: string       // valid=false 时的原因
}

/** 内核支持的规则类型白名单及其载荷示例（示例用作输入框 placeholder）。 */
export interface RuleTypeSpec {
  type: string          // 'DOMAIN' | 'DOMAIN-SUFFIX' | ... | 'RULE-SET'
  example: string       // 载荷示例
  no_resolve: boolean   // 该类型是否支持 no-resolve 选项
}

/** /subscribe/custom-rules/{name} 的统一响应体：增删查共用。 */
export interface CustomRulesPayload {
  file_ready: boolean     // 订阅原始文件是否已下载（false 时无法添加规则）
  rules: CustomRule[]     // 已按生效顺序返回（before 组在前、after 组在后），前端原样渲染，勿再排序/分组
  groups: string[]        // 该订阅自带的代理组（仅代理组，不含代理节点），作为可选目标
  builtins: string[]      // ['DIRECT','REJECT','PASS']
  providers: string[]     // 该订阅 rule-providers 的键，供 RULE-SET 选择
  rule_types: RuleTypeSpec[]
  status?: 'ok' | 'warning'
  message?: string
}

export interface SubscriptionItem {
  name: string
  url: string
  update_interval: number
  health_interval: number
  prefix: string
  info?: SubscriptionInfo | null
  // 订阅级自定义规则（切换模式）：编辑订阅时必须原样带回，否则保存并应用会丢规则
  custom_rules?: CustomRule[]
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
  // 融合模式按规则集档位存放的自定义规则，由专用接口维护；前端只做原样透传，
  // 但必须带回请求体，否则「保存并应用」会把它们丢掉（后端另有继承兜底）
  merge_custom_rules?: Record<string, CustomRule[]>
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
            // 先铺开后端返回的原始字段：像 merge_custom_rules 这类由专用接口维护、
            // 前端不直接编辑的字段必须原样带回，否则「保存并应用」的请求体里就没有它们
            //（后端虽有继承兜底，前端也不该主动丢字段）
            ...cfg,
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