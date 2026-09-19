<script setup lang="ts">
import { ref, onMounted, onActivated, computed, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiFetch } from '../utils/api'
import { MailOutline, EyeOutline, EyeOffOutline, SyncOutline, CreateOutline, TrashOutline, AddOutline, CloseOutline, InformationCircleOutline } from '@vicons/ionicons5'
import { useGlobalStore } from '../store/global'
import { storeToRefs } from 'pinia'
import { useSubscriptionStore, type SubscriptionItem } from '../store/subscription'
import { useRulesStore } from '../store/rules'
import { useProxyStore } from '../store/proxies'
import { useConfigStore } from '../store/config'
import { validateSubscriptionName, filterSubscriptionNameInput, MAX_SUBSCRIPTION_NAME_LENGTH } from '../utils/subscription-name'
import { useViewActive } from '../composables/useViewActive'

const globalStore = useGlobalStore()
const configStore = useConfigStore()
const { coreStatus } = storeToRefs(configStore)
const proxyStore = useProxyStore()
const { providerInfos } = storeToRefs(proxyStore)

const { t } = useI18n()

const showSecret = ref(false)
const showModal = ref(false)
const showUrls = ref<Record<number, boolean>>({})
const modalTitle = ref('')
const isUpdating = ref<Record<number, boolean>>({})
const isApplying = ref(false)
const pendingPhysicalDeletes = ref<string[]>([])
const showBackendUrl = ref(false)
const showHelpModal = ref(false)
// 视图激活态：KeepAlive 停用时必须收起 Teleport 浮层，否则会跨页残留
const isActive = useViewActive()

// 弹窗编辑项
const editingIndex = ref(-1)
const editForm = ref<SubscriptionItem>({
  name: '',
  url: '',
  update_interval: 86400,
  health_interval: 600,
  prefix: ''
})

// 新增选中状态（绑定到 currentConfig.active_subscription）
const activeSub = computed({
    get: () => currentConfig.value.active_subscription || '',
    set: (val: string) => { currentConfig.value.active_subscription = val; }
})

// 名称长度提示。字符集问题由输入框实时过滤拦截，此处只需提示长度。
const nameHint = computed(() =>
  validateSubscriptionName(editForm.value.name) === 'tooLong'
    ? t('subscription.name_too_long')
    : ''
)
// 点击卡片选中
const selectSubscription = (name: string) => {
    if (currentConfig.value.mode === 'switch') {
        activeSub.value = activeSub.value === name ? '' : name; // 点击已选中的可取消选中（单选切换）
    }
}

const rulesStore = useRulesStore()
const subscriptionStore = useSubscriptionStore()
const { currentConfig, savedSubNames } = storeToRefs(subscriptionStore)

// 从后端重新拉取真实配置（force=true）。store 在已加载时会直接 return 旧快照，
// 因此任何「改完后要看到最新状态」的场景都必须走这里，不能用 loadConfig()。
const reloadConfig = async () => {
  try {
    await subscriptionStore.loadConfig(true)
  } catch (e) {
    console.error('加载订阅配置失败', e)
  }
}
// 记录正在轮询的定时器，避免多次触发或卸载泄露
const activePolls = new Map<number, any>()

const clearPoll = (index: number) => {
  const timer = activePolls.get(index)
  if (timer) {
    clearInterval(timer)
    activePolls.delete(index)
  }
}

// 获取订阅显示信息（融合模式优先使用动态数据）
const getSubscriptionDisplayInfo = (sub: SubscriptionItem) => {
  if (currentConfig.value.mode === 'merge') {
    const info = providerInfos.value[sub.name]
    if (info && info.subscriptionInfo) {
      return {
        upload: info.subscriptionInfo.Upload || 0,
        download: info.subscriptionInfo.Download || 0,
        total: info.subscriptionInfo.Total || 0,
        expire: info.subscriptionInfo.Expire || 0,
        updatedAt: info.updatedAt || null,
      }
    }
    return null
  }
  // 切换模式仍使用持久化数据
  return sub.info || null
}

// 预处理所有订阅的显示信息
const subscriptionsWithDisplay = computed(() => {
  return (currentConfig.value.subscriptions || []).map((sub) => ({
    ...sub,
    displayInfo: getSubscriptionDisplayInfo(sub),
  }))
})

// 手动更新单个订阅
const handleUpdateSub = async (index: number) => {
  const sub = currentConfig.value.subscriptions[index]
  if (!sub) return
  if (isUpdating.value[index]) return

  // 记录初始的更新时间以比对
  const initialTime = sub.info?.updatedAt || null

  isUpdating.value[index] = true
  globalStore.showToast(t('rules.updating'), 'info')
  try {
    const encoded = encodeURIComponent(sub.name)
    const resp = await apiFetch(`/subscribe/update/${encoded}`, { method: 'POST' })
    const result = await resp.json()

    if (!resp.ok) {
      globalStore.showToast(`${t('subscription.operation_failed')}: ${result.message || ''}`, 'error')
      await reloadConfig()
      isUpdating.value[index] = false
      return
    }

    if (result.status === 'processing') {
      // 融合模式：异步更新，前端轮询 2s 间隔，最长 30s
      let retries = 0
      const maxRetries = 15
      clearPoll(index)

      const timer = setInterval(async () => {
        retries++
        try {
          // 必须强制拉取：store 已加载时会直接返回旧快照，轮询将永远看不到 updatedAt 变化
          await subscriptionStore.loadConfig(true)
          const updatedSub = currentConfig.value.subscriptions.find(s => s.name === sub.name)
          if (updatedSub && updatedSub.info?.updatedAt !== initialTime) {
            clearPoll(index)
            isUpdating.value[index] = false
            globalStore.showToast(t('subscription.update_success', { name: sub.name }), 'success')
            rulesStore.fetchRules(true)
            rulesStore.fetchProviders(true)
            proxyStore.fetchProxies(true)
          } else if (retries >= maxRetries) {
            clearPoll(index)
            isUpdating.value[index] = false
            globalStore.showToast(`${t('subscription.operation_failed')}: ${t('proxies.timeout')}`, 'error')
          }
        } catch (pollErr) {
          console.error('轮询订阅配置出错:', pollErr)
        }
      }, 2000)

      activePolls.set(index, timer)
    } else if (result.status === 'ok') {
      // 切换模式：同步更新成功
      globalStore.showToast(result.message || t('subscription.update_success', { name: sub.name }), 'success')
      if (result.info) {
        currentConfig.value.subscriptions[index].info = {
          upload: result.info.upload || 0,
          download: result.info.download || 0,
          total: result.info.total || 0,
          expire: result.info.expire || 0,
          updatedAt: result.info.updatedAt || null,
        }
      }
      isUpdating.value[index] = false
      rulesStore.fetchRules(true)
      rulesStore.fetchProviders(true)
      proxyStore.fetchProxies(true)
    } else {
      globalStore.showToast(`${t('subscription.operation_failed')}: ${result.message || ''}`, 'error')
      isUpdating.value[index] = false
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
    isUpdating.value[index] = false
  }
}
// 打开模态框
const openSubModal = (index: number = -1) => {
  editingIndex.value = index
  if (index >= 0) {
    modalTitle.value = t('subscription.edit_modal_title')
    const sub = currentConfig.value.subscriptions[index]
    editForm.value = {
      name: sub.name || '',
      url: sub.url || '',
      update_interval: sub.update_interval || 86400,
      health_interval: sub.health_interval || 600,
      prefix: sub.prefix || ''
    }
  } else {
    modalTitle.value = t('subscription.add_modal_title')
    editForm.value = {
      name: '',
      url: '',
      update_interval: 86400,
      health_interval: 600,
      prefix: ''
    }
  }
  showModal.value = true
}

const closeSubModal = () => {
  showModal.value = false
}

// 保存至订阅列表
const saveSubToList = () => {
  const { name, url } = editForm.value
  if (!name.trim() || !url.trim()) {
    globalStore.showToast(t('common.name_required'), 'error')
    return
  }
  // 订阅名会用作节点文件名与 provider 键，限制字符集（后端有同样的校验兜底）
  const nameError = validateSubscriptionName(name)
  if (nameError === 'tooLong') {
    globalStore.showToast(t('subscription.name_too_long'), 'error')
    return
  }
  if (nameError !== null) {
    globalStore.showToast(t('subscription.name_invalid'), 'error')
    return
  }
  const isDuplicate = (currentConfig.value.subscriptions || []).some((sub, idx) => {
    return sub.name.trim() === name.trim() && idx !== editingIndex.value
  })
  if (isDuplicate) {
    globalStore.showToast(t('subscription.duplicate_name'), 'error')
    return
  }
  const subData = { ...editForm.value }
  const wasEmpty = (currentConfig.value.subscriptions || []).length === 0
  if (editingIndex.value >= 0) {
    // 重命名时必须同步迁移选中态：active_subscription 以订阅名为键，
    // 若停留在旧名，切换模式下会「看起来没选中」却仍按旧名保存（复制旧文件）。
    const oldName = currentConfig.value.subscriptions[editingIndex.value]?.name
    currentConfig.value.subscriptions[editingIndex.value] = subData
    if (oldName && oldName !== subData.name && currentConfig.value.active_subscription === oldName) {
      currentConfig.value.active_subscription = subData.name
    }
  } else {
    if (!currentConfig.value.subscriptions) {
      currentConfig.value.subscriptions = []
    }
    currentConfig.value.subscriptions.push(subData)
  }
  showModal.value = false

  // 如果是切换模式且原列表为空（即第一个订阅），自动选中该订阅
  if (currentConfig.value.mode === 'switch' && wasEmpty) {
    currentConfig.value.active_subscription = name
  }
  // 从物理删除暂存列表中移除，以防同名冲突
  pendingPhysicalDeletes.value = pendingPhysicalDeletes.value.filter(n => n !== name.trim())
}

// 删除订阅
const handleDeleteSub = async (index: number) => {
  const sub = currentConfig.value.subscriptions[index]
  if (!sub) return

  const result = await globalStore.showConfirm({
    title: t('common.confirm_delete'),
    message: `${t('common.confirm_delete')} ${sub.name}?`,
    type: 'danger',
    checkboxLabel: t('subscription.delete_physical_file'),
    checkboxDefault: true
  })

  if (result.confirmed) {
    if (result.checkboxChecked) {
      if (!pendingPhysicalDeletes.value.includes(sub.name)) {
        pendingPhysicalDeletes.value.push(sub.name)
      }
    }
    // 删除的若是当前选中的订阅，同步清空选中态，避免残留一个指向已删除订阅的旧名
    if (currentConfig.value.active_subscription === sub.name) {
      currentConfig.value.active_subscription = ''
    }
    currentConfig.value.subscriptions.splice(index, 1)
  }
}

// 保存并应用
const saveAndApply = async () => {
  // 切换模式下的选中校验：
  // 有订阅时必须选中其中之一，且选中名必须真实存在——改名/删除后可能残留旧名
  // （后端只判空字符串），若不拦截，后端会按旧名复制旧订阅文件，而前端既不选中
  // 任何卡片、界面又显示新名字，造成「显示与生效不一致」。
  // 无订阅时允许保存（清空选中态），后端会据此生成基础配置。
  const subsForSelection = currentConfig.value.subscriptions || []
  if (currentConfig.value.mode === 'switch') {
    if (subsForSelection.length === 0) {
      currentConfig.value.active_subscription = ''
    } else if (!subsForSelection.some(s => s.name === currentConfig.value.active_subscription)) {
      currentConfig.value.active_subscription = ''
      globalStore.showToast(t('subscription.switch_no_selection'), 'error')
      return
    }
  }
  // 端口必填
  if (!currentConfig.value.proxy_port || !currentConfig.value.panel_port) {
    globalStore.showToast(t('subscription.proxy_port') + ' / ' + t('subscription.panel_port') + ' ' + t('common.required'), 'error')
    return
  }
  
  // 端口范围及冲突校验
  const proxyPort = currentConfig.value.proxy_port
  const panelPort = currentConfig.value.panel_port
  const tproxyPort = currentConfig.value.tproxy_port || 0

  const portsToCheck = [proxyPort, panelPort]
  if (tproxyPort !== 0) {
    portsToCheck.push(tproxyPort)
  }

  for (const p of portsToCheck) {
    if (p < 1025 || p > 65535) {
      globalStore.showToast(t('config.port_invalid_hint'), 'error')
      return
    }
  }

  if (new Set(portsToCheck).size !== portsToCheck.length) {
    globalStore.showToast(t('config.port_duplicate_hint'), 'error')
    return
  }
  // 规则组必填（不能为 'none'）
  if (!currentConfig.value.rule_group || currentConfig.value.rule_group === 'none') {
    globalStore.showToast(t('subscription.rule_group') + ' ' + t('common.required'), 'error')
    return
  }

  isApplying.value = true
  try {
    // 将前端的 subscriptions 转换为后端期望的格式
    const subscriptionsForBackend = currentConfig.value.subscriptions.map(sub => {
      // 解构出前端自定义字段 info 和其余属性
      const { info, ...rest } = sub

      // 如果 info 存在，构造 subscription_info；否则为 undefined（序列化时忽略）
      const subscription_info = info ? {
        upload: info.upload || 0,
        download: info.download || 0,
        total: info.total || 0,
        expire: info.expire || 0,
      } : undefined

      // 提取 updatedAt 作为 updated_at
      const updated_at = info?.updatedAt || undefined

      return {
        ...rest,
        updated_at,
        subscription_info,
      }
    })

    // 构造完整 payload，包含转换后的订阅列表和待物理删除列表
    const payload = {
      ...currentConfig.value,
      subscriptions: subscriptionsForBackend,
      delete_physical: pendingPhysicalDeletes.value,
    }

    const resp = await apiFetch('/subscribe/generate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    })
    const result = await resp.json()

    if (resp.ok && result.status === 'ok') {
      globalStore.showToast(result.message || t('subscription.apply_success'), 'success')
      // 清空待物理删除列表
      pendingPhysicalDeletes.value = []
      // 重新加载配置，保持前后端数据一致（必须 force，否则只会读回本地旧快照）
      await reloadConfig()
      // 刷新规则和代理列表
      rulesStore.fetchRules(true)
      rulesStore.fetchProviders(true)
      proxyStore.fetchProxies(true)
    } else {
      globalStore.showToast(`${t('subscription.operation_failed')}: ${result.message || ''}`, 'error')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    isApplying.value = false
  }
}

// TPROXY 端口输入框清空时自动置 0
const onTproxyPortInput = (event: Event) => {
  const target = event.target as HTMLInputElement
  if (target.value === '') {
    currentConfig.value.tproxy_port = 0
  }
}

// 辅助格式化
const formatGB = (bytes: number) => {
  if (!bytes) return '0.0 GB'
  return (bytes / (1024 * 1024 * 1024)).toFixed(1) + ' GB'
}

const formatUpdateTime = (dateStr: string | null) => {
  if (!dateStr) return null
  const date = new Date(dateStr)
  if (isNaN(date.getTime())) return null
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}

const formatExpire = (expire: number) => {
  if (!expire) return t('subscription.expire_forever')
  return new Date(expire * 1000).toLocaleString()
}

onMounted(() => {
  // 视图重新挂载时静默强制刷新订阅配置，避免长期显示陈旧数据（§4.1）
  subscriptionStore.loadConfig(true)
})

onActivated(() => {
  subscriptionStore.loadConfig(true)
})

onUnmounted(() => {
  // 清理所有未完成的订阅轮询定时器
  activePolls.forEach(timer => clearInterval(timer))
  activePolls.clear()
})
</script>

<template>
  <div class="flex flex-col flex-1 min-h-0 gap-4 h-full">
    <!-- 顶部操作栏 -->
    <div class="glass-medium shadow-none px-6 py-3 md:py-0 rounded-xl border border-slate-200/50 dark:border-slate-800/50 flex flex-wrap gap-4 items-center justify-between transition-all shrink-0 h-auto min-h-[56px] md:h-[56px]">
      <h3 class="text-base font-semibold flex items-center gap-2">
        <MailOutline class="w-5 h-5 text-accent" />
        {{ t('subscription.title') }}
         <button @click="showHelpModal = true" class="flex items-center justify-center text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 transition-all hover:scale-105 active:scale-95 p-0.5 -ml-0.5" :title="t('subscription.help_title')">
           <InformationCircleOutline class="w-4 h-4" />
         </button>
      </h3>
      <button @click="saveAndApply" :disabled="isApplying" class="px-4 py-1.5 bg-accent hover:bg-accent-hover text-white text-xs font-semibold rounded-lg shadow-sm transition-all flex items-center gap-1.5 disabled:opacity-50 disabled:cursor-not-allowed">
        <SyncOutline v-if="isApplying" class="w-3.5 h-3.5 animate-spin" />
        {{ isApplying ? t('subscription.applying_short') : t('subscription.save_and_apply') }}
      </button>
    </div>

    <!-- 内容区域内滚动容器 -->
    <div class="flex-1 min-h-0 overflow-y-auto glass-medium shadow-none p-6 rounded-xl border border-slate-200/50 dark:border-slate-800/50 transition-all pr-4">
      <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div class="flex flex-col gap-2">
          <label class="text-sm font-medium text-slate-600 dark:text-slate-400">{{ t('subscription.proxy_port') }}</label>
          <input type="number" v-model="currentConfig.proxy_port" min="0" max="65535" step="1" class="px-4 py-2.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none" />
        </div>
        <div class="flex flex-col gap-2">
          <label class="text-sm font-medium text-slate-600 dark:text-slate-400">{{ t('subscription.panel_port') }}</label>
          <input type="number" v-model="currentConfig.panel_port" min="0" max="65535" step="1" class="px-4 py-2.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none" />
        </div>
        <div class="flex flex-col gap-2">
          <label class="text-sm font-medium text-slate-600 dark:text-slate-400">{{ t('subscription.tproxy_port') }}</label>
          <input
            type="number"
            v-model.number="currentConfig.tproxy_port"
            min="0" max="65535" step="1" 
            @input="onTproxyPortInput"
            :placeholder="t('config.port_disabled_hint')"
            class="px-4 py-2.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none"
          />
        </div>
        <div class="flex flex-col gap-2">
          <label class="text-sm font-medium text-slate-600 dark:text-slate-400">{{ t('subscription.panel_secret') }}</label>
          <div class="relative flex items-center">
            <input :type="showSecret ? 'text' : 'password'" v-model="currentConfig.panel_secret" class="w-full pl-4 pr-10 py-2.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none" />
            <button @click="showSecret = !showSecret" class="absolute right-3 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200">
              <EyeOutline v-if="showSecret" class="w-5 h-5" />
              <EyeOffOutline v-else class="w-5 h-5" />
            </button>
          </div>
        </div>
        <div class="flex flex-col gap-2">
          <label class="text-sm font-medium text-slate-600 dark:text-slate-400">{{ t('subscription.rule_group') }}</label>
          <select v-model="currentConfig.rule_group" class="px-4 py-2.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none">
            <option value="base">{{ t('subscription.rule_group_base') }}</option>
            <option value="full">{{ t('subscription.rule_group_full') }}</option>
          </select>
        </div>
        <div class="flex flex-col gap-2">
          <label class="text-sm font-medium text-slate-600 dark:text-slate-400">{{ t('subscription.ui_panel') }}</label>
          <select v-model="currentConfig.ui_panel" class="px-4 py-2.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none">
            <option value="metacubexd">MetaCubeXD</option>
            <option value="zashboard">Zashboard</option>
          </select>
        </div>

        <div class="flex flex-col gap-2">
          <label class="text-sm font-medium text-slate-600 dark:text-slate-400">{{ t('subscription.meta_backend_url') }}</label>
          <div class="relative flex items-center">
            <input
              :type="showBackendUrl ? 'text' : 'password'"
              v-model="currentConfig.meta_backend_url"
              :placeholder="t('subscription.meta_backend_url_placeholder')"
              class="w-full pl-4 pr-10 py-2.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none"
            />
            <button
              @click="showBackendUrl = !showBackendUrl"
              class="absolute right-3 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
            >
              <EyeOutline v-if="showBackendUrl" class="w-5 h-5" />
              <EyeOffOutline v-else class="w-5 h-5" />
            </button>
          </div>
        </div>
      </div>

      <div class="flex flex-wrap gap-y-3 gap-x-4 items-center justify-between mt-8 mb-4">
        <h4 class="font-semibold text-base shrink-0 order-1">{{ t('subscription.subscription_list') }}</h4>
        <div class="flex bg-slate-100 dark:bg-slate-800 rounded-lg p-0.5 transition-all shrink-0 order-3 sm:order-2 w-full sm:w-auto sm:ml-auto">
          <button
            @click="currentConfig.mode = 'merge'"
            class="flex-1 sm:flex-none px-4 py-1.5 text-xs font-semibold rounded-md transition-all duration-200"
            :class="currentConfig.mode === 'merge' ? 'bg-accent text-white shadow-sm' : 'text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'"
          >
            {{ t('subscription.mode_merge') }}
          </button>
          <button
            @click="currentConfig.mode = 'switch'"
            class="flex-1 sm:flex-none px-4 py-1.5 text-xs font-semibold rounded-md transition-all duration-200"
            :class="currentConfig.mode === 'switch' ? 'bg-accent text-white shadow-sm' : 'text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'"
          >
            {{ t('subscription.mode_switch') }}
          </button>
        </div>
        <button @click="openSubModal(-1)" class="px-3.5 py-1.5 bg-accent hover:bg-accent-hover text-white text-xs font-semibold rounded-lg shadow-sm transition-all flex items-center gap-1 shrink-0 order-2 sm:order-3">
          <AddOutline class="w-4 h-4" /> {{ t('subscription.add_subscription') }}
        </button>
      </div>

      <div id="subList" class="space-y-4">
        <div v-if="!currentConfig.subscriptions || currentConfig.subscriptions.length === 0" class="text-slate-400 dark:text-slate-600 text-sm py-4 text-center">
          {{ t('subscription.no_subscriptions') }}
        </div>
        <!-- 卡片循环（已修改：支持点击选中和高亮） -->
        <div 
          v-else 
          v-for="(item, idx) in subscriptionsWithDisplay" 
          :key="item.name" 
          @click="selectSubscription(item.name)"
          class="live-card p-4 rounded-xl border border-slate-200/40 dark:border-slate-800/40 bg-slate-50/50 dark:bg-slate-900/30 flex flex-col gap-3 hover:border-slate-300/80 dark:hover:border-slate-700/80 hover:-translate-y-[3px] hover:shadow-md hover:bg-slate-100/80 dark:hover:bg-slate-900/80 transition-all duration-300 relative overflow-hidden cursor-pointer"
          :class="{
            'border-accent ring-2 ring-accent/30': currentConfig.mode === 'switch' && currentConfig.active_subscription === item.name
          }"
        >
          <!-- 正在更新的卡片遮罩层 -->
          <div v-if="isUpdating[idx]" class="absolute inset-0 glass-light rounded-xl z-10 flex items-center justify-center gap-2 animate-[fadeIn_0.15s_ease-out]">
            <div class="w-4 h-4 border-2 border-slate-300 dark:border-slate-700 !border-t-accent rounded-full animate-spin"></div>
            <span class="text-[11px] font-bold text-slate-500 dark:text-slate-400">
              {{ t('rules.updating') }}
            </span>
          </div>
          <div class="flex justify-between items-start gap-4">
            <div class="min-w-0 flex-1">
              <span class="font-semibold text-slate-800 dark:text-slate-100 break-all">{{ item.name }}</span>
              <div class="text-xs text-slate-400 dark:text-slate-500 mt-1 select-all break-all flex items-center gap-1.5">
                <button @click.stop="showUrls[idx] = !showUrls[idx]" class="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 focus:outline-none" :title="showUrls[idx] ? t('subscription.hide_url') : t('subscription.show_url')">
                  <EyeOffOutline v-if="showUrls[idx]" class="w-3.5 h-3.5" />
                  <EyeOutline v-else class="w-3.5 h-3.5" />
                </button>
                <span>{{ showUrls[idx] ? item.url : '••••••••' }}</span>
              </div>
            </div>
            <div class="flex gap-1.5" @click.stop>
              <button v-if="savedSubNames.has(item.name)" @click="handleUpdateSub(idx)" :disabled="isUpdating[idx]" class="p-2 hover:bg-slate-200 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 rounded-lg transition-all" :title="t('rules.update')">
                <SyncOutline class="w-4 h-4 inline-block" :class="{ 'animate-spin': isUpdating[idx] }" />
              </button>
              <button @click="openSubModal(idx)" class="p-2 hover:bg-slate-200 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 rounded-lg transition-all" :title="t('common.edit')">
                <CreateOutline class="w-4 h-4" />
              </button>
              <button @click="handleDeleteSub(idx)" class="p-2 hover:bg-red-500/10 hover:text-red-500 text-slate-500 dark:text-slate-400 rounded-lg transition-all" :title="t('common.delete')">
                <TrashOutline class="w-4 h-4" />
              </button>
            </div>
          </div>

          <!-- 信息展示 -->
          <div v-if="item.displayInfo" class="space-y-2">
            <div class="flex items-center gap-3">
              <div class="flex-1 bg-slate-200 dark:bg-slate-800 h-2 rounded-full overflow-hidden">
                <div class="bg-accent h-full rounded-full transition-all" :style="{ width: Math.min(((item.displayInfo.upload + item.displayInfo.download) / (item.displayInfo.total || 1)) * 100, 100) + '%' }">
                </div>
              </div>
              <span class="text-xs font-semibold text-accent">{{ ((item.displayInfo.upload + item.displayInfo.download) / (item.displayInfo.total || 1) * 100).toFixed(1) }}%</span>
            </div>
            <div class="flex justify-between text-xs text-slate-500 dark:text-slate-400">
              <span>{{ formatGB(item.displayInfo.upload + item.displayInfo.download) }} / {{ formatGB(item.displayInfo.total) }}</span>
              <span>{{ t('subscription.valid_until_label') }}{{ formatExpire(item.displayInfo.expire) }}</span>
            </div>
            <div class="flex justify-between text-[11px] text-slate-400 dark:text-slate-500 mt-1">
              <span>{{ t('subscription.updated_at_label') }}{{ formatUpdateTime(item.displayInfo.updatedAt) || t('common.unknown') }}</span>
            </div>
          </div>
          <div v-else class="text-xs text-slate-400 dark:text-slate-500">
            <template v-if="!savedSubNames.has(item.name)">
              {{ t('subscription.save_to_show_info') }}
            </template>
            <template v-else>
              {{ !coreStatus.running ? `${t('config.core_stopped')}${t('common.list_sep')}${t('subscription.traffic_unavailable')}` : t('subscription.traffic_unavailable') }}
            </template>
          </div>
        </div>
      </div>
    </div>

    <!-- 保存并应用全屏模糊加载浮层 -->
    <Teleport to="body">
      <div v-if="isActive && isApplying" class="fixed inset-0 glass-mask z-[9999] flex flex-col items-center justify-center gap-3 animate-[fadeIn_0.2s_ease-out]">
        <div class="glass-medium border px-6 py-4 rounded-2xl shadow-xl flex items-center gap-3">
          <div class="w-5 h-5 border-2 border-slate-200 dark:border-slate-800 !border-t-accent rounded-full animate-spin"></div>
          <span class="text-xs font-bold text-slate-600 dark:text-slate-300">{{ t('subscription.applying') }}</span>
        </div>
      </div>
    </Teleport>

    <!-- 使用说明弹窗 -->
    <Teleport to="body">
      <div v-if="isActive && showHelpModal" class="fixed inset-0 glass-mask z-[9999] flex items-center justify-center p-4" @click.self="showHelpModal = false">
        <div class="glass-heavy w-full max-w-lg rounded-[20px] shadow-2xl border p-6 flex flex-col gap-4 animate-[zoomIn_0.2s_ease-out]">
          <div class="flex justify-between items-center border-b border-slate-100 dark:border-slate-800 pb-3">
            <h2 class="text-lg font-bold">{{ t('subscription.help_title') }}</h2>
            <button @click="showHelpModal = false" class="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 flex items-center justify-center p-1 rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 transition-all">
              <CloseOutline class="w-5 h-5" />
            </button>
          </div>
          <div class="text-sm text-slate-700 dark:text-slate-300 whitespace-pre-wrap leading-relaxed">
            {{ t('subscription.help_content') }}
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Modal -->
    <Teleport to="body">
      <div v-if="isActive && showModal" class="fixed inset-0 glass-mask z-[9999] flex items-center justify-center p-4">
        <div class="glass-heavy w-full max-w-lg rounded-[20px] shadow-2xl border p-6 flex flex-col gap-4 animate-[zoomIn_0.2s_ease-out]">
          <div class="flex justify-between items-center border-b border-slate-100 dark:border-slate-800 pb-3">
            <h2 class="text-lg font-bold">{{ modalTitle }}</h2>
            <button @click="closeSubModal" class="text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 flex items-center justify-center p-1 rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 transition-all">
              <CloseOutline class="w-5 h-5" />
            </button>
          </div>

          <div class="space-y-4">
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.name') }}</label>
              <input
                type="text"
                :value="editForm.name"
                @input="editForm.name = filterSubscriptionNameInput(($event.target as HTMLInputElement).value)"
                :maxlength="MAX_SUBSCRIPTION_NAME_LENGTH"
                :placeholder="t('subscription.name_placeholder')"
                class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm"
              />
              <p v-if="nameHint" class="text-[11px] text-danger">{{ nameHint }}</p>
            </div>
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.url') }}</label>
              <input type="text" v-model="editForm.url" placeholder="https://example.com/sub" class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm" />
            </div>
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.update_interval') }}</label>
              <input type="number" v-model="editForm.update_interval" placeholder="86400" class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm" />
            </div>
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.health_interval') }}</label>
              <input type="number" v-model="editForm.health_interval" placeholder="300" class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm" />
            </div>
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.prefix') }}</label>
              <input type="text" v-model="editForm.prefix" :placeholder="t('subscription.prefix_placeholder')" class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm" />
            </div>
          </div>

          <p class="text-xs text-slate-400 dark:text-slate-500 leading-normal">{{ t('subscription.modal_hint') }}</p>

          <div class="flex justify-end gap-2.5 pt-4 border-t border-slate-100 dark:border-slate-800">
            <button @click="closeSubModal" class="px-4 py-2 text-sm font-semibold rounded-lg bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 transition-all">
              {{ t('subscription.cancel') }}
            </button>
            <button @click="saveSubToList" class="px-4 py-2 text-sm font-semibold rounded-lg bg-accent hover:bg-accent-hover text-white transition-all shadow-md shadow-accent/15">
              {{ t('subscription.save_to_list') }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style>
@keyframes zoomIn {
  from { opacity: 0; transform: scale(0.95); }
  to { opacity: 1; transform: scale(1); }
}
</style>
