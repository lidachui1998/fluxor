import { defineStore } from 'pinia'
import { ref } from 'vue'
import { apiFetch } from '../utils/api'

export interface RuleItem {
  type: string
  payload: string
  proxy: string
  enabled: boolean
  index?: number
}

export interface ProviderItem {
  name: string
  type: string
  ruleCount: number
  updatedAt: string
}

export const useRulesStore = defineStore('rules', () => {
  const rules = ref<RuleItem[]>([])
  const providers = ref<ProviderItem[]>([])
  const isLoadingRules = ref(false)
  const isLoadingProviders = ref(false)

  // 获取所有规则，支持静默刷新。返回是否成功，交由调用方决定如何提示。
  const fetchRules = async (silent = false) => {
    if (!silent) isLoadingRules.value = true
    try {
      const resp = await apiFetch('/rules')
      if (!resp.ok) {
        console.error('获取规则失败：HTTP', resp.status)
        return false
      }
      const data = await resp.json()
      const list = data.rules || []
      list.forEach((r: any, idx: number) => {
        // 内核 /rules 已下发权威 index；仅在缺失时才回退到数组下标。
        // 直接写死 idx 会在前端做过任何过滤/重排后与内核错位，
        // 导致 /rules/disable 关掉错误的规则。
        if (typeof r.index !== 'number') r.index = idx
        r.enabled = !(r.extra?.disabled === true)
      })
      rules.value = list
      return true
    } catch (e) {
      // 保留上一份快照，避免网络抖动清空列表
      console.error('获取规则失败', e)
      return false
    } finally {
      if (!silent) isLoadingRules.value = false
    }
  }

  // 获取规则提供商。返回是否成功，交由调用方决定如何提示。
  const fetchProviders = async (silent = false) => {
    if (!silent) isLoadingProviders.value = true
    try {
      const resp = await apiFetch('/providers/rules')
      if (!resp.ok) {
        console.error('获取规则提供商失败：HTTP', resp.status)
        return false
      }
      const data = await resp.json()
      const providersObj = data.providers || {}
      providers.value = Object.keys(providersObj).map(name => ({
        name,
        ...providersObj[name]
      })) as ProviderItem[]
      return true
    } catch (e) {
      console.error('获取规则提供商失败', e)
      return false
    } finally {
      if (!silent) isLoadingProviders.value = false
    }
  }

  return {
    rules,
    providers,
    isLoadingRules,
    isLoadingProviders,
    fetchRules,
    fetchProviders
  }
})
