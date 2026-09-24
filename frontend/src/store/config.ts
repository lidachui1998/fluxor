// stores/config.ts
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { apiFetch } from '../utils/api'
import { createStaleFlag } from '../utils/staleFlag'

export interface TunConfig {
  enable: boolean
  stack: string
  device: string
}

export interface ConfigData {
  'allow-lan': boolean
  ipv6: boolean
  mode: string
  'log-level': string
  'interface-name': string
  tun: TunConfig
  port: number
  'socks-port': number
  'redir-port': number
  'tproxy-port': number
  'mixed-port': number
}

export const useConfigStore = defineStore('config', () => {
  // ---------- 内核配置 ----------
  const configs = ref<ConfigData>({
    'allow-lan': false,
    ipv6: false,
    mode: 'Rule',
    'log-level': 'silent',
    'interface-name': '',
    tun: { enable: false, stack: 'System', device: '' },
    port: 0,
    'socks-port': 0,
    'redir-port': 0,
    'tproxy-port': 0,
    'mixed-port': 0
  })

  const configsLoading = ref(true)
  const configsLoaded = ref(false)          // 标记是否已加载过
  let configsFetchPromise: Promise<void> | null = null

  // ---------- TProxy 状态 ----------
  const tproxyEnabled = ref<boolean>(false)
  const tproxyStateLoaded = ref(false)      // 标记 TProxy 状态是否已加载
  let tproxyFetchPromise: Promise<boolean> | null = null

  // 就地同步配置字段：只更新发生变化的键，保留 configs 对象与 tun 对象的引用，
  // 使 Vue 仅重渲染受影响的绑定，而非整张卡片
  const patchConfigsInPlace = (next: ConfigData) => {
    const cur = configs.value
    const setIfChanged = <K extends keyof ConfigData>(key: K, value: ConfigData[K]) => {
      if (cur[key] !== value) cur[key] = value
    }
    setIfChanged('allow-lan', next['allow-lan'])
    setIfChanged('ipv6', next.ipv6)
    setIfChanged('mode', next.mode)
    setIfChanged('log-level', next['log-level'])
    setIfChanged('interface-name', next['interface-name'])
    setIfChanged('port', next.port)
    setIfChanged('socks-port', next['socks-port'])
    setIfChanged('redir-port', next['redir-port'])
    setIfChanged('tproxy-port', next['tproxy-port'])
    setIfChanged('mixed-port', next['mixed-port'])
    // tun 为嵌套对象，逐字段就地同步以保留其引用
    const curTun = cur.tun
    const nextTun = next.tun
    if (curTun.enable !== nextTun.enable) curTun.enable = nextTun.enable
    if (curTun.stack !== nextTun.stack) curTun.stack = nextTun.stack
    if (curTun.device !== nextTun.device) curTun.device = nextTun.device
  }

  // ---------- 获取内核配置（缓存 + 防并发） ----------
  // forceLoading: 已加载过也强制重新拉取
  // silent:       静默刷新，不触发 configsLoading（表单保存后同步状态用）
  const fetchConfigs = async (forceLoading = false, silent = false) => {
    if (configsLoaded.value && !forceLoading && !silent) {
      return
    }
    if (configsFetchPromise) {
      return configsFetchPromise
    }

    const hasData = configs.value.port !== 0 || configs.value['mixed-port'] !== 0
    if (!silent && (forceLoading || !hasData)) {
      configsLoading.value = true
    }

    configsFetchPromise = (async () => {
      try {
        const resp = await apiFetch('/configs')
        if (resp.ok) {
          const data = await resp.json()
          const rawMode = data.mode || 'Rule'
          let normalizedMode = 'Rule'
          if (typeof rawMode === 'string') {
            const m = rawMode.toLowerCase()
            if (m === 'global') normalizedMode = 'Global'
            else if (m === 'direct') normalizedMode = 'Direct'
          }

          const tunData = data.tun || {}
          let normalizedStack = 'System'
          if (tunData.stack) {
            const s = tunData.stack.toLowerCase()
            if (s === 'gvisor') normalizedStack = 'gVisor'
            else if (s === 'mixed') normalizedStack = 'Mixed'
            else if (s === 'mips') normalizedStack = 'Mips'
          }

          const next = {
            'allow-lan': data['allow-lan'] || false,
            ipv6: data.ipv6 || false,
            mode: normalizedMode,
            'log-level': data['log-level'] || 'silent',
            'interface-name': data['interface-name'] || '',
            tun: {
              enable: tunData.enable || false,
              stack: normalizedStack,
              device: tunData.device || ''
            },
            port: data.port || 0,
            'socks-port': data['socks-port'] || 0,
            'redir-port': data['redir-port'] || 0,
            'tproxy-port': data['tproxy-port'] || 0,
            'mixed-port': data['mixed-port'] || 0
          }
          if (silent) {
            // 静默刷新：仅就地同步各字段状态，不替换整个对象，
            // 避免卡片整体重渲染导致标题闪烁
            patchConfigsInPlace(next)
          } else {
            configs.value = next
          }
          configsLoaded.value = true
        }
      } catch (e) {
        console.warn('获取内核详细配置失败，可能内核未运行', e)
      } finally {
        configsLoading.value = false
        configsFetchPromise = null
      }
    })()

    return configsFetchPromise
  }

  // ---------- 「内核常规配置已过期」标记 ----------
  //
  // /configs 的取值不只在配置页被改：订阅中心的「保存并应用」会重写 config.yaml 的
  // mixed-port / tproxy-port / secret / external-controller 并重载内核；代理页切换
  // mode 也会改内核配置。而配置页只在挂载时取过一次 /configs，改动方既不知道该页是否
  // 已挂载、也不该替它发请求，于是切回来看到的仍是旧值（实测：订阅中心改 tproxy 端口
  // 后，配置页那一栏不刷新，而 nft 规则其实已经按新端口重建了）。
  //
  // 与代理/规则两页同一套做法：改动方只登记待办，持有数据的页面在 KeepAlive 的
  // onActivated 里消费它并静默补拉（读取即清除，用户不切过去就不产生请求）。
  //
  // 这份标记由**三个页面共用**（配置页 / 代理页 / 概览页都会读 configs），与
  // utils/staleFlag.ts 里「每个数据域各持一份」的说法并不矛盾：那一条针对的是
  // 「各页各自持有不同数据」的情形，共用会让先切过去的页面把别人的待办吃掉。
  // 这里三个页面读的是**同一个 store 状态**，任何一方补拉一次，另外两页看到的
  // 也就都是新值了——因此一份标记即可，谁先激活谁刷新。
  const { markNeedsRefresh: markCoreConfigStale, consumeNeedsRefresh: consumeCoreConfigStale } = createStaleFlag()

  // ---------- 获取 TProxy 状态（缓存 + 防并发） ----------
  const fetchTproxyState = async (retries = 2) => {
    if (tproxyStateLoaded.value) {
      return true
    }
    if (tproxyFetchPromise) {
      return tproxyFetchPromise
    }

    tproxyFetchPromise = (async () => {
      for (let attempt = 0; attempt <= retries; attempt++) {
        try {
          const resp = await apiFetch('/config/tproxy')
          if (resp.ok) {
            const data = await resp.json()
            tproxyEnabled.value = data.enabled
            tproxyStateLoaded.value = true
            return true
          }
        } catch (e) {
          console.warn(`获取 TProxy 状态失败 (尝试 ${attempt + 1}/${retries + 1})`, e)
          if (attempt < retries) {
            await new Promise(r => setTimeout(r, 300 * (attempt + 1)))
          }
        }
      }
      return false
    })()

    return tproxyFetchPromise
  }

  // ---------- 强制刷新 TProxy 状态（用于操作后） ----------
  const refreshTproxyState = async () => {
    tproxyStateLoaded.value = false
    tproxyFetchPromise = null
    return fetchTproxyState()
  }

  // ---------- 内核运行状态 ----------
  const coreStatus = ref({
    running: false,
    loading: true
  })

  const fetchCoreStatus = async () => {
    try {
      const resp = await apiFetch('/core/status')
      if (resp.ok) {
        const data = await resp.json()
        coreStatus.value.running = data.running
      }
    } catch (e) {
      console.error('获取内核状态失败', e)
    } finally {
      coreStatus.value.loading = false
    }
  }

  const refreshCoreStatus = async () => {
    await fetchCoreStatus()
  }

  return {
    configs,
    configsLoading,
    configsLoaded,
    fetchConfigs,
    markCoreConfigStale,
    consumeCoreConfigStale,
    tproxyEnabled,
    tproxyStateLoaded,
    fetchTproxyState,
    refreshTproxyState,
    coreStatus,
    refreshCoreStatus,
  }
})