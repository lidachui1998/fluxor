<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiFetch } from '../utils/api'
import { OptionsOutline, HardwareChipOutline, ShieldCheckmarkOutline, BuildOutline, SearchOutline, SyncOutline, ColorPaletteOutline, SettingsOutline, InformationCircleOutline, DocumentTextOutline, ChevronDownOutline } from '@vicons/ionicons5'
import { useGlobalStore } from '../store/global'
import { storeToRefs } from 'pinia'
import { useConfigStore, type ConfigData } from '../store/config'
import { useOverviewStore, CORE_VERSION_LOADING, CORE_VERSION_UNKNOWN } from '../store/overview'
import FormSwitch from '../components/FormSwitch.vue'
import { useViewActive } from '../composables/useViewActive'

const { t, locale } = useI18n()
const globalStore = useGlobalStore()
const configStore = useConfigStore()
const overviewStore = useOverviewStore()
const { configs, configsLoading, coreStatus } = storeToRefs(configStore)
const { stats } = storeToRefs(overviewStore)
const fetchConfigs = configStore.fetchConfigs

const coreVersion = computed(() => {
  if (stats.value.coreVersion === CORE_VERSION_LOADING) return t('common.loading')
  if (stats.value.coreVersion === CORE_VERSION_UNKNOWN) return ''
  return 'v' + stats.value.coreVersion
})

const onTproxyPortClick = () => {
  if (configStore.tproxyEnabled) {
    globalStore.showToast(t('config.tproxy_port_readonly_warning'), 'warning')
  }
}

export interface CoreStatus {
  running: boolean
  loading: boolean
}

// DNS 查询测试
const dnsQuery = ref({
  name: '',
  type: 'A',
  result: '',
  loading: false
})

const isUpgrading = ref(false)
const isReloading = ref(false)
const isFlushingFakeIP = ref(false)
const isFlushingDNS = ref(false)
const isUpdatingGeo = ref(false)

const interfaces = ref<string[]>([])
const fetchInterfaces = async () => {
  try {
    const resp = await apiFetch('/interfaces')
    if (resp.ok) {
      interfaces.value = await resp.json()
    }
  } catch (e) {
    console.error('获取网卡列表失败:', e)
  }
}

// TUN 高级设置弹窗：TUN 设备名 + 出口网卡
// 实时修改（字段变更即写回后端），无保存按钮，仅关闭。
const showTunAdvancedDialog = ref(false)

// TProxy 绕过列表弹窗
const showTproxyExceptionsDialog = ref(false)

// 视图激活态：KeepAlive 停用时必须收起 Teleport 弹窗，否则会跨页残留
const isActive = useViewActive()

const tproxyDstExceptionsText = ref('')
const tproxySrcExceptionsText = ref('')

// 后端预填的绕过列表（GET /config/tproxy/exceptions 的 defaults 字段）。
// 只在 store.go 维护一份清单，前端不复制内容，「恢复默认」按钮直接用这里的值。
const tproxyDefaults = ref<{ dst: string[], src: string[] } | null>(null)

// 本机流量接管开关（已从弹窗移至开关行下方，故状态提升为页面级）
const tproxyProxyLocal = ref(false)
const tproxyProxyLocalLoaded = ref(false)

// 接管 IPv6 开关（默认关闭）。与「接管本机流量」同为页面级状态：
// 二者都只在 TProxy 开关行下方展示，且启用 TProxy 期间均置灰不可改。
const tproxyIPv6 = ref(false)
const tproxyIPv6Loaded = ref(false)

// 打开 TUN 高级设置弹窗：弹窗内两项均为实时修改，无需预设值，
// 仅确保网卡列表已加载
const openTunAdvancedDialog = async () => {
  if (interfaces.value.length === 0) {
    await fetchInterfaces()
  }
  showTunAdvancedDialog.value = true
}

// 读取本机流量接管开关状态
const fetchTproxyProxyLocal = async () => {
  try {
    const resp = await apiFetch('/config/tproxy/proxy-local')
    if (resp.ok) {
      const data = await resp.json()
      tproxyProxyLocal.value = data.enabled
      tproxyProxyLocalLoaded.value = true
    }
  } catch (e) {
    console.error('获取本机流量接管开关失败:', e)
  }
}

// 读取接管 IPv6 开关状态（默认关闭）
const fetchTproxyIPv6 = async () => {
  try {
    const resp = await apiFetch('/config/tproxy/proxy-ipv6')
    if (resp.ok) {
      const data = await resp.json()
      tproxyIPv6.value = data.enabled
      tproxyIPv6Loaded.value = true
    }
  } catch (e) {
    console.error('获取 IPv6 接管开关失败:', e)
  }
}

// 打开 TProxy 绕过列表弹窗：同时取回绕过列表本体与后端预填内容（供「恢复默认」用）
const openTproxyExceptionsDialog = async () => {
  if (configStore.tproxyEnabled) {
    globalStore.showToast(t('config.tproxy_exceptions_disabled_message'), 'warning')
    return
  }
  try {
    const resp = await apiFetch('/config/tproxy/exceptions')
    if (resp.ok) {
      const data = await resp.json()
      tproxyDstExceptionsText.value = (data.dst || []).join('\n')
      // 直接使用后端数据，不额外填充默认值（老配置里已有用户的列表，不能被默认值覆盖）
      tproxySrcExceptionsText.value = (data.src || []).join('\n')
      tproxyDefaults.value = data.defaults || null
    }
    showTproxyExceptionsDialog.value = true
  } catch (e) {
    globalStore.showToast(t('common.error'), 'error')
  }
}

// 恢复默认：把后端预填内容灌回文本框。刻意不直接落盘——用户可能只想参考默认值，
// 再按自己的网络改几行，因此仍需手动点「保存」。
const restoreTproxyDefaults = () => {
  const defaults = tproxyDefaults.value
  if (!defaults) {
    globalStore.showToast(t('config.tproxy_defaults_unavailable'), 'warning')
    return
  }
  tproxyDstExceptionsText.value = (defaults.dst || []).join('\n')
  tproxySrcExceptionsText.value = (defaults.src || []).join('\n')
  globalStore.showToast(t('config.tproxy_defaults_filled'), 'info')
}

// 仅保存绕过列表（本机流量接管开关已移出该弹窗）
const saveTproxyExceptions = async () => {
  const dstLines = tproxyDstExceptionsText.value.split('\n').map(s => s.trim()).filter(s => s !== '')
  const srcLines = tproxySrcExceptionsText.value.split('\n').map(s => s.trim()).filter(s => s !== '')
  try {
    const resp = await apiFetch('/config/tproxy/exceptions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dst: dstLines, src: srcLines })
    })
    if (resp.ok) {
      globalStore.showToast(t('config.tproxy_exceptions_saved'), 'success')
      showTproxyExceptionsDialog.value = false
    } else {
      globalStore.showToast(t('common.operation_failed'), 'error')
    }
  } catch (e) {
    globalStore.showToast(t('common.error'), 'error')
  }
}

// 统一修改配置
const patchConfig = async (payload: Partial<ConfigData>) => {
  try {
    const resp = await apiFetch('/configs', {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    })
    if (resp.ok) {
      // 静默同步：仅就地更新开关/端口/选项状态，不触发整卡遮罩与重渲染
      fetchConfigs(false, true)
    } else {
      globalStore.showToast(t('common.operation_failed'), 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  }
}

const toggleAllowLan = () => {
  patchConfig({ 'allow-lan': configs.value['allow-lan'] })
}

const toggleIPv6 = () => {
  patchConfig({ ipv6: configs.value.ipv6 })
}

const changeMode = () => {
  patchConfig({ mode: configs.value.mode })
}

const changeLogLevel = () => {
  patchConfig({ 'log-level': configs.value['log-level'] })
}

const saveInterface = () => {
  patchConfig({ 'interface-name': configs.value['interface-name'] })
}

const savePorts = async (e?: Event) => {
  if (e && e.type === 'keyup' && e.target instanceof HTMLElement) {
    e.target.blur()
    return
  }

  const port = configs.value.port || 0
  const socksPort = configs.value['socks-port'] || 0
  const redirPort = configs.value['redir-port'] || 0
  const tproxyPort = configs.value['tproxy-port'] || 0
  const mixedPort = configs.value['mixed-port'] || 0

  const ports = [port, socksPort, redirPort, tproxyPort, mixedPort]

  for (const p of ports) {
    if (p !== 0 && (p < 1025 || p > 65535)) {
      globalStore.showToast(t('config.port_invalid_hint'), 'error')
      fetchConfigs(false, true)
      return
    }
  }

  const activePorts = ports.filter(p => p !== 0)
  if (new Set(activePorts).size !== activePorts.length) {
    globalStore.showToast(t('config.port_duplicate_hint'), 'error')
    fetchConfigs(false, true)
    return
  }

  // 仅 PATCH 内核运行时配置，不触碰订阅配置的持久化和内存状态
  await patchConfig({
    port,
    'socks-port': socksPort,
    'redir-port': redirPort,
    'tproxy-port': tproxyPort,
    'mixed-port': mixedPort
  })
}

const saveTun = async (e?: Event) => {
  if (e && e.type === 'keyup' && e.target instanceof HTMLElement) {
    e.target.blur()
    return
  }
  const isTunEnabled = configs.value.tun.enable
  // 互斥：开启 TUN 时自动关闭 TProxy。
  //
  // 必须先关 TProxy 再开 TUN，且必须调用后端：后端仍会把 TProxy 判定为启用，
  // 只改本地开关会让「TProxy 绕过列表弹窗禁用」「端口只读」等逻辑基于错误状态
  // 工作（此前正是如此）。先关后开也避免两者同时生效的窗口。
  if (isTunEnabled && configStore.tproxyEnabled) {
    await disableTproxyOnBackend()
  }
  await patchConfig({ tun: configs.value.tun })
}

// disableTproxyOnBackend 通知后端关闭 TProxy，并同步本地开关。
// 后端会把开关状态落盘，因此这里以服务端返回值为准。
const disableTproxyOnBackend = async () => {
  try {
    const resp = await apiFetch('/config/tproxy', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enable: false })
    })
    if (!resp.ok) {
      globalStore.showToast(t('common.operation_failed'), 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    // 无论成败都以后端实际状态为准，避免本地与后端不一致
    await configStore.refreshTproxyState()
  }
}

const toggleTProxy = async (enable: boolean) => {
  if (enable) {
    const port = configs.value['tproxy-port'] || 0
    if (port === 0) {
      globalStore.showToast(t('config.tproxy_port_zero_warning'), 'warning')
      // 回退开关状态
      configStore.tproxyEnabled = false
      return
    }
    // 互斥：如果 TUN 开启，关闭 TUN
    if (configs.value.tun.enable) {
      configs.value.tun.enable = false
      await patchConfig({ tun: configs.value.tun })
    }
  }

  // 调用后端更新状态（不涉及端口）
  //
  // 后端在规则安装失败或端口为 0 时会拒绝请求并回滚开关状态，
  // 因此这里必须检查响应，把失败原因反馈给用户，并统一以服务端状态为准，
  // 避免出现「界面显示已启用、实际规则未生效」的静默错配。
  try {
    const resp = await apiFetch('/config/tproxy', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enable })
    })
    if (!resp.ok) {
      let msg = t('common.operation_failed')
      try {
        const data = await resp.json()
        if (data.message) msg = data.message
      } catch (_) {}
      globalStore.showToast(msg, 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    // 刷新状态（以服务端返回值为准，失败时即完成回滚）
    await configStore.refreshTproxyState()
  }
}

// 内核进程管理
const handleStartCore = async () => {
  coreStatus.value.loading = true
  try {
    const resp = await apiFetch('/core/start', { method: 'POST' })
    const data = await resp.json()
    if (resp.ok && data.status === 'ok') {
      globalStore.showToast(t('config.core_start_success'), 'success')
      configStore.refreshCoreStatus()
      setTimeout(() => {
        fetchConfigs(true)
        overviewStore.fetchVersionAndStatus()
      }, 1500)
    } else {
      globalStore.showToast(t('config.core_start_failed') + ': ' + (data.message || ''), 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    coreStatus.value.loading = false
  }
}

const handleStopCore = async () => {
  const ok = await globalStore.showConfirm({
    message: t('config.confirm_stop_core'),
    type: 'danger'
  })
  if (!ok) return
  coreStatus.value.loading = true
  try {
    const resp = await apiFetch('/core/stop', { method: 'POST' })
    const data = await resp.json()
    if (resp.ok && data.status === 'ok') {
      globalStore.showToast(t('config.core_stopped_success'), 'success')
      configStore.refreshCoreStatus()
      configsLoading.value = true
      configs.value = {
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
      }
    } else {
      globalStore.showToast(t('config.core_stop_failed') + ': ' + (data.message || ''), 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    coreStatus.value.loading = false
  }
}

const handleRestartCore = async () => {
  const ok = await globalStore.showConfirm({
    message: t('config.confirm_restart'),
    type: 'warning'
  })
  if (!ok) return
  coreStatus.value.loading = true
  try {
    const resp = await apiFetch('/restart', { method: 'POST' })
    if (resp.ok) {
      globalStore.showToast(t('config.restart_sent'), 'success')
      setTimeout(() => {
        fetchConfigs(true)
        overviewStore.fetchVersionAndStatus()
      }, 1500)
    } else {
      globalStore.showToast(t('config.restart_failed'), 'error')
      coreStatus.value.loading = false
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
    coreStatus.value.loading = false
  }
}

const handleReloadConfig = async () => {
  isReloading.value = true
  try {
    const resp = await apiFetch('/configs', { method: 'PUT' })
    if (resp.ok) {
      globalStore.showToast(t('config.reload_success'), 'success')
      // 重载属于配置同步，同样静默刷新，避免卡片标题闪烁
      fetchConfigs(false, true)
    } else {
      globalStore.showToast(t('config.reload_failed'), 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    isReloading.value = false
  }
}

const handleFlushFakeIP = async () => {
  isFlushingFakeIP.value = true
  try {
    const resp = await apiFetch('/cache/fakeip/flush', { method: 'POST' })
    if (resp.ok) globalStore.showToast(t('config.flush_fakeip_success'), 'success')
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    isFlushingFakeIP.value = false
  }
}

const handleFlushDNS = async () => {
  isFlushingDNS.value = true
  try {
    const resp = await apiFetch('/cache/dns/flush', { method: 'POST' })
    if (resp.ok) globalStore.showToast(t('config.flush_dns_success'), 'success')
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    isFlushingDNS.value = false
  }
}

const handleUpdateGeo = async () => {
  isUpdatingGeo.value = true
  try {
    let resp = await apiFetch('/providers/geo', { method: 'POST' }).catch(() => null)
    if (!resp || !resp.ok) {
      resp = await apiFetch('/configs/geo', { method: 'POST' })
    }
    if (resp.ok) {
      globalStore.showToast(t('config.update_geo_sent'), 'success')
    } else {
      globalStore.showToast(t('config.update_geo_failed'), 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    isUpdatingGeo.value = false
  }
}

// 新增响应式状态
const showUpgradeMenu = ref(false)
const upgradeMenuRef = ref<HTMLElement | null>(null)

// 切换下拉菜单
const toggleUpgradeMenu = () => {
  showUpgradeMenu.value = !showUpgradeMenu.value
}

// 升级核心（支持通道参数）
const handleUpgradeCore = async (channel?: string) => {
  if (isUpgrading.value) return
  isUpgrading.value = true
  try {
    let url = '/upgrade'
    if (channel) url += `?channel=${channel}`
    const resp = await apiFetch(url, { method: 'POST' })
    if (resp.ok) {
      // **** 关键修复：强制重置版本号为 loading 哨兵，使下次请求重新获取 ****
      overviewStore.stats.coreVersion = CORE_VERSION_LOADING
      globalStore.showToast(t('config.upgrade_success'), 'success')
      // 延迟刷新，等待内核完成更新
      setTimeout(async () => {
        let attempts = 0
        const maxAttempts = 10 // 增加尝试次数
        while (attempts < maxAttempts) {
          try {
            await overviewStore.fetchVersionAndStatus()
            // 检查是否成功获取到有效版本（非 loading 哨兵或 unknown 哨兵）
            if (overviewStore.stats.coreVersion && 
                overviewStore.stats.coreVersion !== CORE_VERSION_LOADING && 
                overviewStore.stats.coreVersion !== CORE_VERSION_UNKNOWN) {
              break
            }
          } catch (_) {}
          attempts++
          await new Promise(resolve => setTimeout(resolve, 1000))
        }
        fetchConfigs(true)
      }, 2000) // 初始延迟 2 秒
    } else {
      // 错误处理（保持不变）
      let errorMsg = ''
      try {
        const data = await resp.json()
        if (data.message) errorMsg = data.message
      } catch (_) {
        try {
          errorMsg = await resp.text()
        } catch (_) {}
      }
      if (errorMsg.includes('already using latest version')) {
        const channelName = channel === 'alpha' ? t('config.upgrade_alpha') : (channel === 'release' ? t('config.upgrade_stable') : '')
        if (channelName) {
          globalStore.showToast(t('config.upgrade_already_latest_channel', { channel: channelName }), 'warning')
        } else {
          globalStore.showToast(t('config.upgrade_already_latest'), 'warning')
        }
      } else {
        globalStore.showToast(`${t('config.upgrade_failed')}${errorMsg ? ': ' + errorMsg : ''}`, 'error')
      }
    }
  } catch (e) {
    globalStore.showToast(t('config.upgrade_network_error'), 'error')
  } finally {
    isUpgrading.value = false
    showUpgradeMenu.value = false
  }
}

// Alpha 通道升级
const handleUpgradeAlpha = () => {
  handleUpgradeCore('alpha')
}

// 稳定版通道升级
const handleUpgradeStable = () => {
  handleUpgradeCore('release')
}

// 点击菜单外部自动关闭
const handleClickOutside = (event: MouseEvent) => {
  if (upgradeMenuRef.value && !upgradeMenuRef.value.contains(event.target as Node)) {
    showUpgradeMenu.value = false
  }
}

// 监听菜单显示状态，添加/移除全局点击监听
watch(showUpgradeMenu, (val) => {
  if (val) {
    document.addEventListener('click', handleClickOutside)
  } else {
    document.removeEventListener('click', handleClickOutside)
  }
})

const handleDNSQuery = async (e?: Event) => {
  if (!dnsQuery.value.name.trim()) return

  // 主动释放焦点，在移动端自动收起虚拟键盘，以便用户看清下方的解析结果
  if (e && e.target instanceof HTMLElement) {
    e.target.blur()
  }

  dnsQuery.value.loading = true
  dnsQuery.value.result = ''
  try {
    const query = `name=${encodeURIComponent(dnsQuery.value.name)}&type=${dnsQuery.value.type}`
    const resp = await apiFetch(`/dns/query?${query}`)
    if (resp.ok) {
      const data = await resp.json()
      if (data.Status === 0 && data.Answer && data.Answer.length > 0) {
        dnsQuery.value.result = data.Answer.map((a: any) => a.data).join('\n')
      } else {
        dnsQuery.value.result = JSON.stringify(data, null, 2)
      }
    } else {
      dnsQuery.value.result = t('config.dns_query_failed')
    }
  } catch (e) {
    dnsQuery.value.result = `${t('config.dns_query_failed')}: ${(e as Error).message}`
  } finally {
    dnsQuery.value.loading = false
  }
}

// 处理本机流量接管开关。
//
// 语义（与用户约定一致）：
//   - 开启：先二次确认，确认后才写入后端持久化文件；
//   - 关闭：直接写入后端持久化，不确认。
// 后端成功写入后即以服务端返回值为准回写本地状态，避免界面与磁盘不一致。
const handleProxyLocalToggle = async (newVal: boolean) => {
  if (newVal) {
    const confirmed = await globalStore.showConfirm({
      title: t('common.warning'),
      message: t('config.tproxy_proxy_local_warning'),
      type: 'warning'
    })
    if (!confirmed) return // 取消则保持原值
  }
  await persistProxyLocal(newVal)
}

// TProxy 开启时该开关被锁定（切换会重建正被 TProxy 持有的 nft 规则）。
// 此时点击不改变值，只说明原因——与「绕过设置齿轮」同一套交互。
const onProxyLocalBlocked = () => {
  globalStore.showToast(t('config.tproxy_proxy_local_readonly_warning'), 'warning')
}

// persistProxyLocal 写入本机流量接管开关并同步本地状态
const persistProxyLocal = async (enabled: boolean) => {
  try {
    const resp = await apiFetch('/config/tproxy/proxy-local', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled })
    })
    if (!resp.ok) {
      globalStore.showToast(t('common.operation_failed'), 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    await fetchTproxyProxyLocal() // 以服务端状态为准
  }
}

// 处理接管 IPv6 开关。语义与「接管本机流量」一致：
//   - 开启：先二次确认（额外说明「节点无 IPv6 出口会导致原本直连可达的 IPv6 目标失败」），
//     确认后才写入后端持久化文件；
//   - 关闭：直接写入后端持久化，不确认。
// 后端写入成功后以服务端返回值为准回写本地状态。
const handleTproxyIPv6Toggle = async (newVal: boolean) => {
  if (newVal) {
    const confirmed = await globalStore.showConfirm({
      title: t('common.warning'),
      message: t('config.tproxy_ipv6_warning'),
      type: 'warning'
    })
    if (!confirmed) return // 取消则保持原值
  }
  await persistTproxyIPv6(newVal)
}

// TProxy 开启时该开关被锁定（切换会重建正被 TProxy 持有的 nft 规则）。
// 此时点击不改变值，只说明原因——与「绕过设置齿轮」同一套交互。
const onTproxyIPv6Blocked = () => {
  globalStore.showToast(t('config.tproxy_ipv6_readonly_warning'), 'warning')
}

// persistTproxyIPv6 写入 IPv6 接管开关并同步本地状态
const persistTproxyIPv6 = async (enabled: boolean) => {
  try {
    const resp = await apiFetch('/config/tproxy/proxy-ipv6', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled })
    })
    if (!resp.ok) {
      // 后端在「TProxy 已启用且重新应用规则失败」时会带上原因，需如实透出
      let msg = t('common.operation_failed')
      try {
        const data = await resp.json()
        if (data.message) msg = data.message
      } catch (_) {}
      globalStore.showToast(msg, 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    await fetchTproxyIPv6() // 以服务端状态为准
  }
}

// 处理 TUN 开关切换（开启时弹窗确认）
const handleTunToggle = async (newVal: boolean) => {
  if (newVal) {
    // 尝试开启 -> 弹窗警告
    const confirmed = await globalStore.showConfirm({
      title: t('common.warning'),
      message: t('config.tun_enable_warning'),
      type: 'warning'
    })
    if (confirmed) {
      configs.value.tun.enable = true
      saveTun() // 内部处理互斥并 patch 后端
    }
    // 取消则保持原值（false）
  } else {
    // 关闭直接生效
    configs.value.tun.enable = false
    saveTun()
  }
}

const changeLang = () => {
  localStorage.setItem('lang', locale.value)
}

onMounted(async () => {
  fetchInterfaces()
  fetchTproxyProxyLocal()
  fetchTproxyIPv6()
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
})

</script>

<template>
  <div class="flex flex-col flex-1 min-h-0 gap-4 h-full">
    <!-- 顶部操作栏 -->
    <div class="glass-medium shadow-none px-6 py-3 md:py-0 rounded-xl border border-slate-200/50 dark:border-slate-800/50 flex flex-wrap gap-4 items-center justify-between transition-all shrink-0 h-auto min-h-[56px] md:h-[56px]">
      <h3 class="text-base font-semibold flex items-center gap-2">
        <SettingsOutline class="w-5 h-5 text-accent" />
        {{ t('nav.config') }}
      </h3>

      <!-- 文档 + 关于 按钮组（紧挨） -->
        <div class="flex items-center gap-1">
          <a href="https://ttq.fjb.dpdns.org" target="_blank" rel="noopener noreferrer"
            class="flex items-center justify-center text-xs font-semibold rounded-xl bg-slate-50 hover:bg-slate-100 dark:bg-slate-800/40 dark:hover:bg-slate-800/80 transition-all text-slate-600 dark:text-slate-300 hover:scale-105 active:scale-95 border border-slate-200/50 dark:border-slate-800/30 px-3 py-1.5 gap-1.5 group"
            :title="t('nav.docs')">
            <DocumentTextOutline class="w-4 h-4 shrink-0 transition-transform duration-300 group-hover:scale-110" />
            <span>{{ t('nav.docs') }}</span>
          </a>
    
          <button @click="globalStore.showAbout = true"
            class="flex items-center justify-center text-xs font-semibold rounded-xl bg-slate-50 hover:bg-slate-100 dark:bg-slate-800/40 dark:hover:bg-slate-800/80 transition-all text-slate-600 dark:text-slate-300 hover:scale-105 active:scale-95 border border-slate-200/50 dark:border-slate-800/30 px-3 py-1.5 gap-1.5 group"
            :title="t('about.title')">
            <InformationCircleOutline class="w-4 h-4 shrink-0 transition-transform duration-300 group-hover:rotate-12" />
            <span>{{ t('about.title') }}</span>
          </button>
        </div>
    </div>

    <!-- 核心状态加载中的优雅 Loading 占位 -->
    <div v-if="coreStatus.loading" class="flex-1 flex flex-col items-center justify-center gap-3 select-none">
      <div class="w-7 h-7 border-2 border-slate-200 dark:border-slate-800 !border-t-accent rounded-full animate-spin"></div>
      <span class="text-xs font-bold text-slate-400 dark:text-slate-500 tracking-wider">{{ t('config.loading_system_params') }}</span>
    </div>

    <!-- 加载完成后的内滚动内容区 (已升级为统一大内容卡片) -->
    <div v-else class="flex-1 min-h-0 overflow-y-auto glass-medium shadow-none rounded-xl border border-slate-200/50 dark:border-slate-800/50 p-6">
      <div class="grid grid-cols-1 gap-6 items-start w-full animate-[fadeIn_0.25s_ease-out]"
        :class="[
          coreStatus.running 
            ? 'md:grid-cols-2 lg:grid-cols-3' 
            : 'md:grid-cols-2 max-w-4xl mx-auto'
        ]">
        <!-- 1. 配置参数面板区（常规参数，内核启动时显示） -->
        <div v-if="coreStatus.running"
          class="live-card bg-slate-50/50 dark:bg-slate-900/30 p-6 rounded-xl border border-slate-200/40 dark:border-slate-800/40 hover:border-slate-300/80 dark:hover:border-slate-700/80 hover:-translate-y-[3px] hover:shadow-md hover:bg-slate-100/80 dark:hover:bg-slate-900/80 duration-300 space-y-5 h-full transition-all flex flex-col relative">
          <!-- 同步配置遮罩屏 -->
          <div v-if="configsLoading"
            class="absolute inset-0 glass-light z-30 flex flex-col items-center justify-center rounded-2xl gap-2 select-none border shadow-sm transition-all duration-300">
            <div class="w-5 h-5 border-2 border-slate-200 dark:border-slate-700 !border-t-accent rounded-full animate-spin"></div>
            <span class="text-xs font-bold text-slate-500 dark:text-slate-400 tracking-wider">{{ t('config.syncing_configs') }}</span>
          </div>

          <h4 class="font-bold text-sm border-b border-slate-100 dark:border-slate-800 pb-3 flex items-center gap-2">
            <OptionsOutline class="w-4 h-4 text-accent" />
            {{ t('config.general_settings') }}
          </h4>

          <div class="flex items-center justify-between">
            <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{ t('config.allow_lan') }}</label>
            <FormSwitch v-model="configs['allow-lan']" @update:model-value="toggleAllowLan" />
          </div>

          <div class="flex items-center justify-between">
            <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{ t('config.ipv6_toggle') }}</label>
            <FormSwitch v-model="configs.ipv6" @update:model-value="toggleIPv6" />
          </div>

          <div class="flex flex-col gap-1.5">
            <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{ t('config.mode') }}</label>
            <select v-model="configs.mode" @change="changeMode"
              class="px-3 py-2 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full">
              <option value="Rule">{{ t('config.mode_rule') }}</option>
              <option value="Global">{{ t('config.mode_global') }}</option>
              <option value="Direct">{{ t('config.mode_direct') }}</option>
            </select>
          </div>

          <div class="flex flex-col gap-1.5">
            <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{ t('config.log_level') }}</label>
            <select v-model="configs['log-level']" @change="changeLogLevel"
              class="px-3 py-2 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full">
              <option value="silent">Silent</option>
              <option value="info">Info</option>
              <option value="warning">Warning</option>
              <option value="error">Error</option>
              <option value="debug">Debug</option>
            </select>
          </div>
        </div>

        <!-- 2. 端口设置（内核启动时显示） -->
        <div v-if="coreStatus.running"
          class="live-card bg-slate-50/50 dark:bg-slate-900/30 p-6 rounded-xl border border-slate-200/40 dark:border-slate-800/40 hover:border-slate-300/80 dark:hover:border-slate-700/80 hover:-translate-y-[3px] hover:shadow-md hover:bg-slate-100/80 dark:hover:bg-slate-900/80 duration-300 space-y-5 h-full transition-all flex flex-col relative">
          <!-- 同步配置遮罩屏 -->
          <div v-if="configsLoading"
            class="absolute inset-0 glass-light z-30 flex flex-col items-center justify-center rounded-2xl gap-2 select-none border shadow-sm transition-all duration-300">
            <div class="w-5 h-5 border-2 border-slate-200 dark:border-slate-700 !border-t-accent rounded-full animate-spin"></div>
            <span class="text-xs font-bold text-slate-500 dark:text-slate-400 tracking-wider">{{ t('config.syncing_configs') }}</span>
          </div>

          <h4 class="font-bold text-sm border-b border-slate-100 dark:border-slate-800 pb-3 flex items-center gap-2">
            <HardwareChipOutline class="w-4 h-4 text-accent" />
            {{ t('config.port_settings') }}
          </h4>

          <div class="grid grid-cols-2 gap-4">
            <div class="flex flex-col gap-1">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('config.mixed_port') }}</label>
              <input type="number" v-model.number="configs['mixed-port']" min="0" max="65535" step="1" @blur="savePorts" @keyup.enter="savePorts" :placeholder="t('config.port_disabled_hint')"
                class="px-3 py-1.5 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full" />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('config.http_port') }}</label>
              <input type="number" v-model.number="configs.port" min="0" max="65535" step="1" @blur="savePorts" @keyup.enter="savePorts" :placeholder="t('config.port_disabled_hint')"
                class="px-3 py-1.5 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full" />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('config.socks_port') }}</label>
              <input type="number" v-model.number="configs['socks-port']" min="0" max="65535" step="1" @blur="savePorts" @keyup.enter="savePorts" :placeholder="t('config.port_disabled_hint')"
                class="px-3 py-1.5 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full" />
            </div>
            <div class="flex flex-col gap-1">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('config.redir_port') }}</label>
              <input type="number" v-model.number="configs['redir-port']" min="0" max="65535" step="1" @blur="savePorts" @keyup.enter="savePorts" :placeholder="t('config.port_disabled_hint')"
                class="px-3 py-1.5 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full" />
            </div>
            <div class="flex flex-col gap-1 col-span-2">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('config.tproxy_port') }}</label>
              <input type="number" v-model.number="configs['tproxy-port']" 
                min="0" max="65535" step="1"
                @blur="savePorts" @keyup.enter="savePorts" 
                :placeholder="t('config.port_disabled_hint')"
                :readonly="configStore.tproxyEnabled"
                @click="onTproxyPortClick"
                class="px-3 py-1.5 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full"
                :class="configStore.tproxyEnabled ? 'cursor-not-allowed opacity-60' : ''" />
            </div>
          </div>
        </div>

        <!-- 3. TUN与网卡设置（内核启动时显示） -->
        <div v-if="coreStatus.running"
          class="live-card bg-slate-50/50 dark:bg-slate-900/30 p-6 rounded-xl border border-slate-200/40 dark:border-slate-800/40 hover:border-slate-300/80 dark:hover:border-slate-700/80 hover:-translate-y-[3px] hover:shadow-md hover:bg-slate-100/80 dark:hover:bg-slate-900/80 duration-300 space-y-5 h-full transition-all flex flex-col relative">
          <!-- 同步配置遮罩屏 -->
          <div v-if="configsLoading"
            class="absolute inset-0 glass-light z-30 flex flex-col items-center justify-center rounded-2xl gap-2 select-none border shadow-sm transition-all duration-300">
            <div class="w-5 h-5 border-2 border-slate-200 dark:border-slate-700 !border-t-accent rounded-full animate-spin"></div>
            <span class="text-xs font-bold text-slate-500 dark:text-slate-400 tracking-wider">{{ t('config.syncing_configs') }}</span>
          </div>

          <h4 class="font-bold text-sm border-b border-slate-100 dark:border-slate-800 pb-3 flex items-center gap-2">
            <ShieldCheckmarkOutline class="w-4 h-4 text-accent" />
            {{ t('config.tun_settings') }}
          </h4>

          <!-- TUN 模式。齿轮按钮与「启用 TProxy」一致：点击打开高级设置弹窗，
               弹窗内为 TUN 设备名与出口网卡（实时修改）。 -->
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{ t('config.tun_enable') }}</label>
              <button
                @click="openTunAdvancedDialog"
                class="p-1 text-slate-400 hover:text-accent rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 transition-all"
                :title="t('config.tun_advanced_title')"
              >
                <SettingsOutline class="w-4 h-4" />
              </button>
            </div>
            <FormSwitch :model-value="configs.tun.enable" @update:model-value="handleTunToggle"/>
          </div>
          <div class="flex flex-col gap-1">
            <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('config.tun_stack') }}</label>
            <select v-model="configs.tun.stack" @change="saveTun"
              class="px-3 py-1.5 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full">
              <option value="gVisor">gVisor</option>
              <option value="System">System</option>
              <option value="Mixed">Mixed</option>
              <option value="Mips">Mips</option>
            </select>
          </div>

          <!-- 分割线 -->
          <div class="h-px bg-slate-200 dark:bg-slate-700 my-1.5"></div>

          <!-- TProxy 透明代理 -->
          <div class="flex items-center justify-between">
            <div class="flex items-center gap-2">
              <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">
                {{ t('config.tproxy_enable') }}
              </label>
              <button
                @click="openTproxyExceptionsDialog"
                class="p-1 text-slate-400 hover:text-accent rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 transition-all"
                :class="configStore.tproxyEnabled ? 'opacity-40 cursor-not-allowed' : 'hover:text-accent'"
                :title="configStore.tproxyEnabled ? t('config.tproxy_exceptions_disabled_message') : t('config.tproxy_exceptions_title')"
              >
                <SettingsOutline class="w-4 h-4" />
              </button>
            </div>
            <div v-if="!configStore.tproxyStateLoaded" class="w-7 h-4 rounded-full bg-slate-200 dark:bg-slate-700 animate-pulse"></div>
            <FormSwitch v-else v-model="configStore.tproxyEnabled" @update:model-value="toggleTProxy" />
          </div>

          <!-- 接管本机流量（由弹窗移出）。
               启用 TProxy 时禁止修改：此时切换会重建 nft 规则，而 TProxy 开关
               正持有规则，故禁用——置灰原因经 disabled-hint 外露（悬停 title +
               点击 toast），避免开关无声不响应。 -->
          <div class="flex items-center justify-between">
            <label
              class="text-xs font-semibold"
              :class="configStore.tproxyEnabled ? 'text-slate-400 dark:text-slate-500' : 'text-slate-700 dark:text-slate-300'"
            >
              {{ t('config.tproxy_proxy_local_label') }}
            </label>
            <div v-if="!tproxyProxyLocalLoaded" class="w-7 h-4 rounded-full bg-slate-200 dark:bg-slate-700 animate-pulse"></div>
            <FormSwitch
              v-else
              :model-value="tproxyProxyLocal"
              :disabled="configStore.tproxyEnabled"
              :disabled-hint="t('config.tproxy_proxy_local_readonly_warning')"
              @update:model-value="handleProxyLocalToggle"
              @blocked="onProxyLocalBlocked"
            />
          </div>

          <!-- 接管 IPv6 流量（默认关闭）。
               与「接管本机流量」同一套交互：开启先二次确认（见
               handleTproxyIPv6Toggle），TProxy 启用期间置灰并在悬停/点击时说明原因。
               该开关只决定 IPv6 家族（nft ip6 表 + `ip -6` 策略路由）是否随
               TProxy 一起下发，不影响 IPv4 侧的任何行为。 -->
          <div class="flex items-center justify-between">
            <label
              class="text-xs font-semibold cursor-help"
              :class="configStore.tproxyEnabled ? 'text-slate-400 dark:text-slate-500' : 'text-slate-700 dark:text-slate-300'"
              :title="t('config.tproxy_ipv6_hint')"
            >
              {{ t('config.tproxy_ipv6_label') }}
            </label>
            <div v-if="!tproxyIPv6Loaded" class="w-7 h-4 rounded-full bg-slate-200 dark:bg-slate-700 animate-pulse"></div>
            <FormSwitch
              v-else
              :model-value="tproxyIPv6"
              :disabled="configStore.tproxyEnabled"
              :disabled-hint="t('config.tproxy_ipv6_readonly_warning')"
              @update:model-value="handleTproxyIPv6Toggle"
              @blocked="onTproxyIPv6Blocked"
            />
          </div>
        </div>

        <!-- 4. 运维控制（始终显示） -->
        <div
          class="live-card bg-slate-50/50 dark:bg-slate-900/30 p-6 rounded-xl border border-slate-200/40 dark:border-slate-800/40 hover:border-slate-300/80 dark:hover:border-slate-700/80 hover:-translate-y-[3px] hover:shadow-md hover:bg-slate-100/80 dark:hover:bg-slate-900/80 duration-300 space-y-5 h-full transition-all flex flex-col relative">
          <div class="border-b border-slate-100 dark:border-slate-800 pb-4">
            <h4 class="font-bold text-sm flex items-center gap-2">
              <BuildOutline class="w-4 h-4 text-accent" />
              {{ t('config.advanced_maintenance') }}
            </h4>
          </div>

          <div class="space-y-4 flex-1 flex flex-col justify-between">
            <!-- 内核状态 -->
            <div class="flex items-center justify-between px-3.5 py-2.5 bg-slate-50 dark:bg-slate-900/40 rounded-xl border border-slate-100 dark:border-slate-800/80">
              <span class="text-xs font-semibold text-slate-500 dark:text-slate-400">{{ t('config.core_status') }}</span>
              <div class="flex items-center gap-2.5 text-xs">
                <span class="w-2 h-2 rounded-full flex shrink-0"
                  :class="coreStatus.loading ? 'bg-slate-400 animate-pulse' : (coreStatus.running ? 'bg-success' : 'bg-red-500')"></span>
                <span class="font-bold text-slate-700 dark:text-slate-200">
                  {{ coreStatus.loading ? t('config.core_checking') : (coreStatus.running ? t('config.core_running') : t('config.core_stopped')) }}
                </span>
                <span v-if="coreStatus.running && stats.coreVersion !== CORE_VERSION_UNKNOWN && stats.coreVersion !== CORE_VERSION_LOADING"
                  class="px-1.5 py-0.5 font-mono text-[10px] bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 rounded">
                  {{ coreVersion }}
                </span>
              </div>
            </div>

            <!-- 内核核心控制 -->
            <div class="grid gap-3 w-full"
              :class="coreStatus.running ? 'grid-cols-3' : 'grid-cols-2'">
              <button v-if="!coreStatus.running" @click="handleStartCore" :disabled="coreStatus.loading"
                class="py-2 bg-success hover:bg-success-hover text-white text-xs font-semibold rounded-xl shadow-md shadow-success/15 hover:shadow-success/25 transition-all flex items-center justify-center gap-1.5 w-full">
                <SyncOutline v-if="coreStatus.loading" class="w-3.5 h-3.5 animate-spin inline-block" />
                {{ coreStatus.loading ? t('config.core_starting') : t('config.start_core') }}
              </button>
              <template v-else>
                <button @click="handleStopCore" :disabled="coreStatus.loading"
                  class="py-2 bg-red-500 hover:bg-red-600 text-white text-xs font-semibold rounded-xl shadow-md shadow-red-500/15 hover:shadow-red-500/25 transition-all flex items-center justify-center gap-1.5 w-full">
                  <SyncOutline v-if="coreStatus.loading" class="w-3.5 h-3.5 animate-spin inline-block" />
                  {{ coreStatus.loading ? t('config.core_stopping') : t('config.stop_core') }}
                </button>
                <button @click="handleRestartCore" :disabled="coreStatus.loading"
                  class="py-2 bg-amber-500 hover:bg-amber-600 text-white text-xs font-semibold rounded-xl shadow-md shadow-amber-500/15 hover:shadow-amber-500/25 transition-all flex items-center justify-center w-full">
                  {{ t('config.restart') }}
                </button>
              </template>
              <!-- 拆分更新按钮 -->
              <div ref="upgradeMenuRef" class="relative flex w-full">
                <button
                  @click="handleUpgradeCore()"
                  :disabled="isUpgrading || !coreStatus.running"
                  class="flex-1 py-2 bg-accent hover:bg-accent-hover text-white text-xs font-semibold rounded-l-xl shadow-md shadow-accent/15 hover:shadow-accent/25 transition-all flex items-center justify-center disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {{ isUpgrading ? t('config.upgrading_core') : t('config.upgrade_core') }}
                </button>
                <button
                  @click="toggleUpgradeMenu"
                  :disabled="isUpgrading || !coreStatus.running"
                  class="py-2 px-2 bg-accent hover:bg-accent-hover text-white rounded-r-xl shadow-md shadow-accent/15 hover:shadow-accent/25 transition-all disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center border-l border-white/20"
                >
                  <ChevronDownOutline class="h-4 w-4" />
                </button>
                <!-- 下拉菜单 -->
                <div
                  v-if="showUpgradeMenu"
                  class="absolute top-full left-0 mt-1 w-full bg-white dark:bg-slate-800 rounded-xl shadow-lg border border-slate-200 dark:border-slate-700 py-1 z-50"
                >
                  <button
                    @click="handleUpgradeStable"
                    :disabled="isUpgrading || !coreStatus.running"
                    class="w-full text-left px-4 py-2 text-xs font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-700 transition-colors"
                  >
                    {{ t('config.upgrade_stable') }}
                  </button>
                  <button
                    @click="handleUpgradeAlpha"
                    :disabled="isUpgrading || !coreStatus.running"
                    class="w-full text-left px-4 py-2 text-xs font-medium text-slate-700 dark:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-700 transition-colors"
                  >
                    {{ t('config.upgrade_alpha') }}
                  </button>
                </div>
              </div>
            </div>
   
            <!-- 分割线 -->
            <div class="h-px bg-slate-200 dark:bg-slate-700 my-1.5"></div>
   
            <!-- 常规运维 -->
            <div class="grid grid-cols-2 gap-3">
              <button @click="handleReloadConfig" :disabled="!coreStatus.running || isReloading"
                class="px-4 py-2.5 bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 text-xs font-semibold rounded-xl text-slate-700 dark:text-slate-200 transition-all border border-slate-200/20 disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-1.5">
                <div v-if="isReloading" class="w-3 h-3 border border-slate-300 dark:border-slate-600 !border-t-accent rounded-full animate-spin"></div>
                {{ isReloading ? t('config.reloading') : t('config.reload') }}
              </button>
              <button @click="handleFlushFakeIP" :disabled="!coreStatus.running || isFlushingFakeIP"
                class="px-4 py-2.5 bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 text-xs font-semibold rounded-xl text-slate-700 dark:text-slate-200 transition-all border border-slate-200/20 disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-1.5">
                <div v-if="isFlushingFakeIP" class="w-3 h-3 border border-slate-300 dark:border-slate-600 !border-t-accent rounded-full animate-spin"></div>
                {{ isFlushingFakeIP ? t('config.flushing') : t('config.flush_fakeip') }}
              </button>
              <button @click="handleFlushDNS" :disabled="!coreStatus.running || isFlushingDNS"
                class="px-4 py-2.5 bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 text-xs font-semibold rounded-xl text-slate-700 dark:text-slate-200 transition-all border border-slate-200/20 disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-1.5">
                <div v-if="isFlushingDNS" class="w-3 h-3 border border-slate-300 dark:border-slate-600 !border-t-accent rounded-full animate-spin"></div>
                {{ isFlushingDNS ? t('config.flushing') : t('config.flush_dns') }}
              </button>
              <button @click="handleUpdateGeo" :disabled="!coreStatus.running || isUpdatingGeo"
                class="px-4 py-2.5 bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 text-xs font-semibold rounded-xl text-slate-700 dark:text-slate-200 transition-all border border-slate-200/20 disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-1.5">
                <div v-if="isUpdatingGeo" class="w-3 h-3 border border-slate-300 dark:border-slate-600 !border-t-accent rounded-full animate-spin"></div>
                {{ isUpdatingGeo ? t('config.upgrading_core') : t('config.update_geo') }}
              </button>
            </div>
          </div>
        </div>

        <!-- 5. 界面设置（始终显示） -->
        <div
          class="live-card bg-slate-50/50 dark:bg-slate-900/30 p-6 rounded-xl border border-slate-200/40 dark:border-slate-800/40 hover:border-slate-300/80 dark:hover:border-slate-700/80 hover:-translate-y-[3px] hover:shadow-md hover:bg-slate-100/80 dark:hover:bg-slate-900/80 duration-300 space-y-5 h-full transition-all flex flex-col relative">
          <h4 class="font-bold text-sm border-b border-slate-100 dark:border-slate-800 pb-3 flex items-center gap-2">
            <ColorPaletteOutline class="w-4 h-4 text-accent" />
            {{ t('config.interface_settings') }}
          </h4>

          <div class="space-y-4 flex-1">
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{ t('config.language') }}</label>
              <select v-model="locale" @change="changeLang"
                class="px-3 py-2 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full">
                <option value="zh">{{ t('config.lang_zh') }}</option>
                <option value="en">{{ t('config.lang_en') }}</option>
              </select>
            </div>

            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{ t('config.theme') }}</label>
              <select v-model="globalStore.theme"
                class="px-3 py-2 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full">
                <option value="light">{{ t('config.theme_light') }}</option>
                <option value="dark">{{ t('config.theme_dark') }}</option>
                <option value="purple">{{ t('config.theme_purple') }}</option>
                <option value="pink">{{ t('config.theme_pink') }}</option>
                <option value="green">{{ t('config.theme_green') }}</option>
                <option value="blue">{{ t('config.theme_blue') }}</option>
                <option value="system">{{ t('config.theme_system') }}</option>
              </select>
            </div>

            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-semibold text-slate-700 dark:text-slate-300">{{ t('config.start_page') }}</label>
              <select v-model="globalStore.startPage"
                class="px-3 py-2 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none w-full">
                <option value="last">{{ t('config.start_page_last') }}</option>
                <option value="overview">{{ t('nav.overview') }}</option>
                <option value="proxies">{{ t('nav.proxies') }}</option>
                <option value="subscription">{{ t('nav.subscription') }}</option>
                <option value="rules">{{ t('nav.rules') }}</option>
                <option value="connections">{{ t('nav.connections') }}</option>
                <option value="logs">{{ t('nav.logs') }}</option>
                <option value="config">{{ t('nav.config') }}</option>
              </select>
            </div>
          </div>
        </div>

        <!-- 6. DNS 查询（内核启动时显示） -->
        <div v-if="coreStatus.running"
          class="live-card bg-slate-50/50 dark:bg-slate-900/30 p-6 rounded-xl border border-slate-200/40 dark:border-slate-800/40 hover:border-slate-300/80 dark:hover:border-slate-700/80 hover:-translate-y-[3px] hover:shadow-md hover:bg-slate-100/80 dark:hover:bg-slate-900/80 duration-300 space-y-4 h-full transition-all flex flex-col relative">
          <h4 class="font-bold text-sm border-b border-slate-100 dark:border-slate-800 pb-3 flex items-center gap-2">
            <SearchOutline class="w-4 h-4 text-accent" />
            {{ t('config.dns_query') }}
          </h4>

          <div class="flex-1 flex flex-col justify-between gap-4">
            <div class="flex flex-col gap-2">
              <input type="text" v-model="dnsQuery.name" :placeholder="t('config.dns_placeholder')"
                @keyup.enter="handleDNSQuery"
                class="w-full px-4 py-2 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none" />
              <div class="flex gap-2">
                <select v-model="dnsQuery.type"
                  class="flex-1 px-3 py-2 text-xs rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 focus:ring-2 focus:ring-accent outline-none">
                  <option value="A">A</option>
                  <option value="AAAA">AAAA</option>
                  <option value="MX">MX</option>
                  <option value="TXT">TXT</option>
                </select>
                <button @click="handleDNSQuery" :disabled="dnsQuery.loading"
                  class="flex-[2] py-2 bg-accent hover:bg-accent-hover text-white text-xs font-semibold rounded-lg shadow-sm transition-all flex items-center justify-center gap-1">
                  {{ dnsQuery.loading ? t('config.dns_querying') : t('config.dns_query_btn') }}
                </button>
              </div>
            </div>

            <pre
              class="p-4 bg-slate-50 dark:bg-slate-900/50 font-mono text-xs rounded-xl overflow-y-auto whitespace-pre-wrap break-all h-28 border border-slate-200 dark:border-slate-800 transition-all flex-1"
              :class="dnsQuery.result ? 'text-emerald-700 dark:text-emerald-400' : 'text-slate-400 dark:text-slate-500 italic flex items-center justify-center select-none'">{{ dnsQuery.result || t('config.dns_result_default') }}</pre>
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- ====== TProxy 绕过列表弹窗 ====== -->
  <Teleport to="body">
    <div v-if="isActive && showTproxyExceptionsDialog" class="fixed inset-0 glass-mask z-[9999] flex items-center justify-center p-4" @click.self="showTproxyExceptionsDialog = false">
      <!-- 限制弹窗最大高度为视口 90%，flex 列布局 -->
      <div class="glass-heavy w-full max-w-lg max-h-[90vh] rounded-[20px] shadow-2xl border p-6 flex flex-col gap-4 animate-[zoomIn_0.15s_ease-out]">
        <!-- 标题 + 恢复默认，固定不滚动 -->
        <div class="flex items-center justify-between gap-3 flex-shrink-0">
          <h4 class="text-lg font-bold">{{ t('config.tproxy_exceptions_title') }}</h4>
          <button
            @click="restoreTproxyDefaults"
            class="px-2.5 py-1 text-xs font-semibold rounded-lg text-accent hover:bg-accent/10 border border-accent/30 transition-all shrink-0"
            :title="t('config.tproxy_restore_defaults_hint')"
          >
            {{ t('config.tproxy_restore_defaults') }}
          </button>
        </div>

        <!-- 可滚动内容区域：占据剩余空间，超出时滚动 -->
        <div class="flex-1 min-h-0 overflow-y-auto">
          <div class="space-y-4">
            <!-- 目的绕过 -->
            <div>
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">
                {{ t('config.tproxy_dst_exceptions_label') }}
              </label>
              <p class="text-[11px] text-slate-400 dark:text-slate-500 mt-0.5">{{ t('config.tproxy_dst_exceptions_hint') }}</p>
              <textarea
                v-model="tproxyDstExceptionsText"
                rows="6"
                class="w-full p-3 text-sm font-mono rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none resize-y min-h-[100px]"
                :placeholder="t('config.tproxy_dst_exceptions_placeholder')"
              ></textarea>
            </div>

            <!-- 源绕过 -->
            <div class="pt-2 border-t border-slate-100 dark:border-slate-800/60">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">
                {{ t('config.tproxy_src_exceptions_label') }}
              </label>
              <p class="text-[11px] text-slate-400 dark:text-slate-500 mt-0.5">{{ t('config.tproxy_src_exceptions_hint') }}</p>
              <textarea
                v-model="tproxySrcExceptionsText"
                rows="6"
                class="w-full p-3 text-sm font-mono rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none resize-y min-h-[100px]"
                :placeholder="t('config.tproxy_src_exceptions_placeholder')"
              ></textarea>
            </div>

          </div>
        </div>

        <!-- 底部按钮，固定不滚动 -->
        <div class="flex justify-end gap-2.5 pt-3 border-t border-slate-100 dark:border-slate-800/60 flex-shrink-0">
          <button @click="showTproxyExceptionsDialog = false" class="px-4 py-2 text-sm font-semibold rounded-xl bg-white border border-slate-200 hover:bg-slate-50 dark:bg-slate-800 dark:border-slate-700 dark:hover:bg-slate-700/60 text-slate-600 dark:text-slate-300 transition-all">
            {{ t('common.cancel') }}
          </button>
          <button @click="saveTproxyExceptions" class="px-4 py-2 text-sm font-semibold rounded-xl bg-accent hover:bg-accent-hover text-white transition-all shadow-md shadow-accent/15">
            {{ t('common.save') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>

  <!-- ====== TUN 高级设置弹窗（实时修改，仅关闭） ====== -->
  <Teleport to="body">
    <div v-if="isActive && showTunAdvancedDialog" class="fixed inset-0 glass-mask z-[9999] flex items-center justify-center p-4" @click.self="showTunAdvancedDialog = false">
      <div class="glass-heavy w-full max-w-lg max-h-[90vh] rounded-[20px] shadow-2xl border p-6 flex flex-col gap-4 animate-[zoomIn_0.15s_ease-out]">
        <div class="flex-shrink-0">
          <h4 class="text-lg font-bold">{{ t('config.tun_advanced_title') }}</h4>
        </div>

        <div class="flex-1 min-h-0 overflow-y-auto">
          <div class="space-y-4">
            <!-- TUN 设备名（失焦/回车即时写回） -->
            <div class="flex flex-col gap-1">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">
                {{ t('config.tun_device') }}
              </label>
              <input
                type="text"
                v-model="configs.tun.device"
                @blur="saveTun"
                @keyup.enter="saveTun"
                :placeholder="t('config.interface_name_placeholder')"
                class="px-3 py-2 text-sm rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none w-full"
              />
            </div>

            <!-- 出口网卡（选择即写回） -->
            <div class="flex flex-col gap-1 pt-2 border-t border-slate-100 dark:border-slate-800/60">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">
                {{ t('config.interface_name') }}
              </label>
              <select
                v-model="configs['interface-name']"
                @change="saveInterface"
                class="px-3 py-2 text-sm rounded-xl border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none w-full"
              >
                <option value="">{{ t('config.interface_name_auto') }}</option>
                <option v-for="iface in interfaces" :key="iface" :value="iface">{{ iface }}</option>
              </select>
              <p class="text-[11px] text-slate-400 dark:text-slate-500 mt-0.5">
                {{ t('config.tun_advanced_interface_hint') }}
              </p>
            </div>
          </div>
        </div>

        <!-- 仅关闭按钮：修改实时生效，无需保存 -->
        <div class="flex justify-end pt-3 border-t border-slate-100 dark:border-slate-800/60 flex-shrink-0">
          <button @click="showTunAdvancedDialog = false" class="px-4 py-2 text-sm font-semibold rounded-xl bg-accent hover:bg-accent-hover text-white transition-all shadow-md shadow-accent/15">
            {{ t('common.close') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
