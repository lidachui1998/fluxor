<script setup lang="ts">
// 自定义规则弹窗（多作用域 + 页签）。
//
// 由订阅页共用两种模式：
//   切换模式 —— endpoint='/subscribe/custom-rules'，作用域=订阅名（单作用域，不渲染页签）；
//   融合模式 —— endpoint='/subscribe/merge-custom-rules'，作用域=规则集档位 base/full（两个页签）。
//
// 两种模式的后端响应体完全同构，因此组件对作用域只做「key → URL 片段」的映射，
// 不区分模式；「哪个作用域当前生效」也只用于打标签，不参与请求构造。
import { ref, reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiFetch } from '../utils/api'
import { CreateOutline, TrashOutline, AddOutline, SyncOutline, ArrowUpOutline, ArrowDownOutline } from '@vicons/ionicons5'
import { useGlobalStore } from '../store/global'
import type { CustomRule, CustomRulesPayload, RuleTypeSpec } from '../store/subscription'

/** 规则列表里「同 position 分组内的上/下边界」预计算结果（§4.9：不在模板里做重计算）。 */
interface MoveLimits {
  canMoveUp: boolean
  canMoveDown: boolean
}

/** 规则表单：一次只写一条规则，添加成功后仅清空取值，保留类型/目标便于连续录入。 */
interface RuleFormState {
  type: string
  payload: string
  target: string
  position: string
  no_resolve: boolean
}

const defaultRuleForm = (): RuleFormState => ({
  type: '',
  payload: '',
  target: '',
  position: 'before',
  no_resolve: false,
})

/**
 * 单个作用域的全部状态。
 *
 * 每个作用域各自持有一份 payload、加载态与**独立的表单状态**：切页签时另一档
 * 已输入的取值必须原样留着（用户可能在两档之间来回对照着填），因此表单不能做成
 * 全局单例再在切换时搬运。
 */
interface ScopeState {
  payload: CustomRulesPayload | null
  loading: boolean
  // 该作用域是否已用首次拿到的 payload 初始化过表单默认值（每个作用域只做一次）
  formInited: boolean
  form: RuleFormState
  editingRuleId: string
  saving: boolean
  movingRuleId: string
  movingDirection: 'up' | 'down' | ''
  deletingRuleId: string
}

const createScopeState = (): ScopeState => ({
  payload: null,
  loading: false,
  formInited: false,
  form: defaultRuleForm(),
  editingRuleId: '',
  saving: false,
  movingRuleId: '',
  movingDirection: '',
  deletingRuleId: '',
})

const props = defineProps<{
  visible: boolean
  // 视图激活态：KeepAlive 停用时必须收起 Teleport 浮层，否则会跨页残留
  isActive: boolean
  // 弹窗标题（父组件已把订阅名/模式名拼进文案）
  title: string
  // 作用域提示文案：只有融合模式需要，切换模式留空即整段不显示。
  // 由父组件给定而不是组件内按 endpoint 判模式：组件对两种模式只做「key → URL 片段」的映射
  hint?: string
  // 作用域接口前缀：'/subscribe/custom-rules' | '/subscribe/merge-custom-rules'
  endpoint: string
  scopes: { key: string, label: string, effective: boolean }[]
}>()

// 关闭时把「发生过后端写操作的作用域 key」交给父组件：由它决定要不要登记
// 「规则列表已过期」（只有当前生效的作用域才会改变运行中的规则集合）
const emit = defineEmits<{
  (e: 'close', mutatedScopeKeys: string[]): void
}>()

// 本次弹窗内发生过后端写操作的作用域 key（仅打开又关闭不算）。
// 单独一份集合而不是挂在 ScopeState 上：作用域状态跨开关保留，写标记只属于「这一轮」。
const mutatedKeys = reactive(new Set<string>())

const { t } = useI18n()
const globalStore = useGlobalStore()

// 作用域状态表：按 key 持有，跨开关弹窗保留，因此重新打开时不会丢掉已输入的内容
const scopeStates = reactive<Record<string, ScopeState>>({})
const activeScopeKey = ref('')

// 每个作用域的表单状态在首次加载到 payload 时才会被写入，这次程序化赋值同样
// 不得触发「改类型即清空取值」的重置，故按 key 记录被程序化改写的类型值。
const programmaticTypes = new Map<string, string>()

const scopeState = (key: string): ScopeState => {
  if (!scopeStates[key]) scopeStates[key] = createScopeState()
  return scopeStates[key]
}

const scopeUrl = (key: string) => `${props.endpoint}/${encodeURIComponent(key)}`

// 首次进入某作用域时选定活动页签：优先「当前生效」的那一档，其次是第一档
const initialScopeKey = () => {
  const effective = props.scopes.find(s => s.effective)
  return effective?.key ?? props.scopes[0]?.key ?? ''
}

const activeScope = computed(() => (activeScopeKey.value ? scopeState(activeScopeKey.value) : null))

// ------- 派生数据：模板只做取值，不做任何计算（§4.9）-------
const activePayload = computed<CustomRulesPayload | null>(() => activeScope.value?.payload ?? null)
const isLoading = computed(() => activeScope.value?.loading ?? false)
const ruleForm = computed<RuleFormState | null>(() => activeScope.value?.form ?? null)
const editingRuleId = computed(() => activeScope.value?.editingRuleId ?? '')
const isSavingRule = computed(() => activeScope.value?.saving ?? false)
const movingRuleId = computed(() => activeScope.value?.movingRuleId ?? '')
const movingDirection = computed(() => activeScope.value?.movingDirection ?? '')
const deletingRuleId = computed(() => activeScope.value?.deletingRuleId ?? '')

// 后端返回的可选项一律经 computed 兜底为空数组，模板因此无需处理 null
const ruleTypes = computed<RuleTypeSpec[]>(() => activePayload.value?.rule_types ?? [])
const ruleBuiltins = computed<string[]>(() => activePayload.value?.builtins ?? [])
const ruleGroups = computed<string[]>(() => activePayload.value?.groups ?? [])
const ruleProviders = computed<string[]>(() => activePayload.value?.providers ?? [])
const ruleList = computed<CustomRule[]>(() => activePayload.value?.rules ?? [])

// 目标下拉的额外选项：历史规则的 target 可能来自已改名的代理组/节点，不在 builtins/groups 里。
// 单独追加一条，否则下拉显示为空并在保存时把用户的目标静默改掉（数据丢失）
const ruleTargetExtra = computed<string[]>(() => {
  const target = ruleForm.value?.target
  if (!target) return []
  return ruleBuiltins.value.includes(target) || ruleGroups.value.includes(target) ? [] : [target]
})

// 当前类型对应的规格：驱动 placeholder 与 no-resolve 选项的显隐
const selectedRuleType = computed<RuleTypeSpec | null>(
  () => ruleTypes.value.find(spec => spec.type === ruleForm.value?.type) ?? null
)
const isRuleSetType = computed(() => ruleForm.value?.type === 'RULE-SET')
// RULE-SET 的取值只能来自该作用域可引用的 rule-providers，为空时无从选择
// （融合模式的 base 档位没有 rule-providers，即走这里禁用表单）
const hasNoRuleProviders = computed(() => isRuleSetType.value && ruleProviders.value.length === 0)
// file_ready=false 时后端无法校验规则，整个表单禁用
const rulesFormDisabled = computed(() => !activePayload.value?.file_ready || hasNoRuleProviders.value)
// 新增与编辑共用同一个提交按钮，校验条件一致
const canSubmitRule = computed(() =>
  !rulesFormDisabled.value
  && !isSavingRule.value
  && (ruleForm.value?.payload.trim() ?? '') !== ''
  && (ruleForm.value?.target ?? '') !== ''
)
// 编辑模式：提交按钮改为「保存」并多出一个「取消编辑」
const isEditingRule = computed(() => editingRuleId.value !== '')
// 上/下移与删除共用一个「有请求在途」的判定，避免并发写同一条列表
const isRuleBusy = computed(() => !!movingRuleId.value || !!deletingRuleId.value)

// rules 已按生效顺序返回（before 组在前、after 组在后，组内连续），
// 故「同 position 组内的第一条/最后一条」即为该组的上/下边界
const ruleMoveLimits = computed<Record<string, MoveLimits>>(() => {
  const limits: Record<string, MoveLimits> = {}
  const rules = ruleList.value
  rules.forEach((rule) => {
    const samePosition = rules.filter(item => item.position === rule.position)
    limits[rule.id] = {
      canMoveUp: samePosition[0]?.id !== rule.id,
      canMoveDown: samePosition[samePosition.length - 1]?.id !== rule.id,
    }
  })
  return limits
})
const moveLimitsFor = (id: string): MoveLimits => ruleMoveLimits.value[id] ?? { canMoveUp: false, canMoveDown: false }

// 内置目标的展示名走 i18n；订阅/模板自带的代理组名原样展示
const builtinTargetKeys: Record<string, string> = {
  DIRECT: 'subscription.custom_rule_target_direct',
  REJECT: 'subscription.custom_rule_target_reject',
  PASS: 'subscription.custom_rule_target_pass',
}
const ruleTargetLabel = (target: string) => {
  const key = builtinTargetKeys[target]
  return key ? t(key) : target
}

// ------- 加载 -------
// 载入某作用域的规则。响应始终写回发起时捕获的那一档（用户可能在请求在途时切页签）。
const loadScope = async (key: string) => {
  if (!key) return
  const state = scopeState(key)
  state.loading = true
  try {
    const resp = await apiFetch(scopeUrl(key))
    const data = await resp.json()
    if (!resp.ok) {
      globalStore.showToast(`${t('subscription.custom_rule_load_failed')}: ${data.message || ''}`, 'error')
      return
    }
    applyPayload(key, data)
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    state.loading = false
  }
}

// 用响应刷新本地数据；每个作用域仅在首次拿到 payload 时初始化表单默认值
const applyPayload = (key: string, data: CustomRulesPayload) => {
  const state = scopeState(key)
  state.payload = data
  if (state.formInited) return
  state.formInited = true
  const form = state.form
  if (!form.type && data.rule_types?.length) {
    // RULE-SET 只能从下拉里选；类型下拉本身含全部类型，无需特殊处理
    form.type = data.rule_types[0].type
    programmaticTypes.set(key, form.type)
  }
  if (!form.target) {
    form.target = data.builtins?.[0] || data.groups?.[0] || ''
  }
}

const selectScope = (key: string) => {
  if (key === activeScopeKey.value) return
  activeScopeKey.value = key
  // 首次切到该档位（或上一次加载失败留下空 payload）才拉数据：
  // 另一档保持惰性，不产生多余请求，但失败后切回来仍能重试
  if (!scopeState(key).payload) loadScope(key)
}

// 本次「打开」已经选过页签的作用域 key：用来让下面两个 watch 分清
// 「只是父组件重算了 scopes」与「用户真的换了个作用域」，避免同一次打开发两次请求。
let mountedScopeKey = ''

// 弹窗打开期间以 activeScopeKey 驱动加载；每次打开都把活动页签重置为「当前生效」的那一档，
// 并重新拉取一次——规则可能已被其它入口（另一个页签、订阅更新）改动过。
watch(() => props.visible, (visible) => {
  if (!visible) {
    mountedScopeKey = ''
    return
  }
  const key = initialScopeKey()
  activeScopeKey.value = key
  mountedScopeKey = key
  if (key) loadScope(key)
})

// 作用域集合变化（父组件换作用域后重算 scopes）：仅在「活动页签已不在新集合里」时
// 才校正到新的生效档位——那种情况下 visible 不会变化，需要自己触发一次加载。
watch(() => props.scopes, () => {
  if (!props.visible) return
  if (props.scopes.some(s => s.key === activeScopeKey.value)) return
  const key = initialScopeKey()
  activeScopeKey.value = key
  if (key && key !== mountedScopeKey) {
    mountedScopeKey = key
    loadScope(key)
  }
})

// 切换类型时重置取值与 no-resolve（两者都与类型强相关）。
// flush:'sync' + programmaticTypes：编辑模式回填与首次加载的默认值都是程序化赋值，
// 绝不能触发重置（否则会抹掉正在编辑的取值）；用户手动改类型仍走同一逻辑。
watch(() => ruleForm.value?.type ?? '', (newType) => {
  const key = activeScopeKey.value
  const state = activeScope.value
  if (!key || !state) return
  if (programmaticTypes.get(key) === newType) {
    programmaticTypes.delete(key)
    return
  }
  const form = state.form
  form.payload = ''
  form.no_resolve = false
  // RULE-SET 只能从下拉里选，默认选中第一个规则集，避免出现空选项
  if (newType === 'RULE-SET') {
    form.payload = ruleProviders.value[0] || ''
  }
}, { flush: 'sync' })

// ------- 写操作：一律打在「当前活动作用域」的 URL 上 -------
// 进入编辑模式：把该条规则回填到表单，并记住 id 供 PUT 就地替换（列表位置不变）
const startEditRule = (rule: CustomRule) => {
  const state = activeScope.value
  if (!state) return
  programmaticTypes.set(activeScopeKey.value, rule.type)
  state.editingRuleId = rule.id
  state.form.type = rule.type
  state.form.payload = rule.payload
  state.form.target = rule.target
  // 位置取值以 before 为默认（与后端 NormalizeRulePosition 一致），避免历史空值显示成「末尾」
  state.form.position = rule.position === 'after' ? 'after' : 'before'
  state.form.no_resolve = !!rule.no_resolve
}

// 用户主动「取消编辑」：回到新增的默认态（插入位置也回到默认的「最前」）
const cancelEditRule = () => {
  const state = activeScope.value
  if (!state) return
  state.editingRuleId = ''
  state.form.payload = ''
  state.form.position = 'before'
  state.form.no_resolve = false
}

// 提交成功后的表单复位：退出编辑态（保存/取消按钮随之消失），并清空取值。
//
// 新增与修改共用，保证两条路径收敛到同一个「可以继续录入」的状态：
// 保留类型/目标/位置便于连续录入，只清取值——否则刚提交的内容留在表单里，
// 会让人误以为仍处于编辑中。RULE-SET 的取值必须来自下拉，故回落到第一个规则集。
const resetFormAfterSubmit = (state: ScopeState) => {
  state.editingRuleId = ''
  state.form.payload = isRuleSetType.value ? (ruleProviders.value[0] || '') : ''
}

// status=warning 表示规则已保存但运行配置未同步，需如实告知（优先于成功提示）
const notifyMutation = (data: CustomRulesPayload, fallback: string) => {
  if (data.status === 'warning' && data.message) {
    globalStore.showToast(data.message, 'warning')
  } else {
    globalStore.showToast(fallback, 'success')
  }
}

// 新增（POST）或保存修改（PUT）：后端即时持久化，弹窗无需整体保存按钮
const handleSubmitRule = async () => {
  const key = activeScopeKey.value
  const state = activeScope.value
  if (!key || !state || !canSubmitRule.value) return
  const editingId = state.editingRuleId
  state.saving = true
  try {
    const resp = await apiFetch(scopeUrl(key), {
      method: editingId ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        // 修改必须带 id：后端据此就地替换，保持该规则在列表中的位置
        ...(editingId ? { id: editingId } : {}),
        type: state.form.type,
        payload: state.form.payload.trim(),
        target: state.form.target,
        position: state.form.position,
        no_resolve: state.form.no_resolve,
      })
    })
    const data = await resp.json()
    if (!resp.ok) {
      // 校验失败（如与已有规则行重复）时原样展示后端提示，并保持编辑态便于修正
      globalStore.showToast(data.message || t('subscription.operation_failed'), 'error')
      return
    }
    applyPayload(key, data)
    resetFormAfterSubmit(state)
    mutatedKeys.add(key)
    // 修改成功用「规则已更新」，新增成功用「规则已添加」
    notifyMutation(data, editingId ? t('subscription.custom_rule_updated') : t('subscription.custom_rule_added'))
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    state.saving = false
  }
}

// 上/下移：与同插入位置分组内的相邻规则交换。成功静默替换列表（用户会连续点按，不弹提示）
const handleMoveRule = async (rule: CustomRule, direction: 'up' | 'down') => {
  const key = activeScopeKey.value
  const state = activeScope.value
  if (!key || !state || state.movingRuleId) return
  state.movingRuleId = rule.id
  state.movingDirection = direction
  try {
    const resp = await apiFetch(scopeUrl(key), {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: rule.id, direction })
    })
    const data = await resp.json()
    if (!resp.ok) {
      // 已是该分组首/末条时后端回 400，原样展示后端提示
      globalStore.showToast(data.message || t('subscription.operation_failed'), 'error')
      return
    }
    applyPayload(key, data)
    mutatedKeys.add(key)
    // status=warning 表示顺序已保存但运行配置未同步，需如实告知
    if (data.status === 'warning' && data.message) {
      globalStore.showToast(data.message, 'warning')
    }
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    state.movingRuleId = ''
    state.movingDirection = ''
  }
}

const handleDeleteRule = async (rule: CustomRule) => {
  const key = activeScopeKey.value
  const state = activeScope.value
  if (!key || !state || state.deletingRuleId) return
  const confirmed = await globalStore.showConfirm({
    title: t('common.confirm_delete'),
    message: t('subscription.custom_rule_delete_confirm'),
    type: 'danger',
  })
  if (!confirmed) return

  state.deletingRuleId = rule.id
  try {
    const resp = await apiFetch(`${scopeUrl(key)}?id=${encodeURIComponent(rule.id)}`, { method: 'DELETE' })
    const data = await resp.json()
    if (!resp.ok) {
      globalStore.showToast(data.message || t('subscription.operation_failed'), 'error')
      return
    }
    applyPayload(key, data)
    mutatedKeys.add(key)
    // 被删掉的正是当前编辑的那条：退出编辑态，否则之后的保存会打到一个不存在的 id 上
    if (state.editingRuleId === rule.id) cancelEditRule()
    notifyMutation(data, t('subscription.custom_rule_deleted'))
  } catch (e) {
    globalStore.showToast(`${t('common.error')}: ${(e as Error).message}`, 'error')
  } finally {
    state.deletingRuleId = ''
  }
}

// 关闭：把「改动过的作用域 key」交给父组件。两条关闭路径（页脚关闭 / 遮罩点击）
// 都收敛到这里，不能有分支绕过去——漏掉任何一个都会导致规则页继续显示旧列表。
const closeDialog = () => {
  const closedKeys = props.scopes.filter(scope => mutatedKeys.has(scope.key)).map(scope => scope.key)
  // 清掉已上报的标记：同一个实例会被再次打开，不能重复上报
  mutatedKeys.clear()
  emit('close', closedKeys)
}

// 父组件在「改完未关弹窗就切走」时调用：取走待上报的作用域并清空。
//
// 与 closeDialog 的区别只是「由谁上报」。切页时父组件等不到 close（用户可能再也不回来关），
// 但这次改动已经落盘，丢弃它会让规则页一直显示旧列表；而多登记一次的代价只是一个布尔标记。
// 失败代价不对称，因此选择上报而不是丢弃。
const takeMutatedScopes = () => {
  const keys = props.scopes.filter(scope => mutatedKeys.has(scope.key)).map(scope => scope.key)
  mutatedKeys.clear()
  return keys
}

defineExpose({ takeMutatedScopes })
</script>

<template>
  <Teleport to="body">
    <div v-if="isActive && visible" class="fixed inset-0 glass-mask z-[9999] flex items-center justify-center p-4" @click.self="closeDialog">
      <div class="glass-heavy w-full max-w-lg max-h-[85vh] rounded-[20px] shadow-2xl border p-6 flex flex-col gap-4 animate-[zoomIn_0.2s_ease-out]">
        <div class="flex items-center border-b border-slate-100 dark:border-slate-800 pb-3 shrink-0">
          <h2 class="text-lg font-bold break-all">{{ title }}</h2>
        </div>

        <!-- 作用域页签：仅多作用域（融合模式 base/full）时出现，单作用域不与标题重复 -->
        <div v-if="scopes.length > 1" class="flex bg-slate-100 dark:bg-slate-800 rounded-lg p-0.5 transition-all shrink-0">
          <button
            v-for="scope in scopes"
            :key="scope.key"
            type="button"
            @click="selectScope(scope.key)"
            class="flex-1 px-3 py-1.5 text-xs font-semibold rounded-md transition-all duration-200 flex items-center justify-center gap-1.5"
            :class="scope.key === activeScopeKey ? 'bg-accent text-white shadow-sm' : 'text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'"
          >
            <span class="truncate">{{ scope.label }}</span>
            <span
              v-if="scope.effective"
              class="px-1.5 py-0.5 rounded text-[10px] font-semibold shrink-0"
              :class="scope.key === activeScopeKey ? 'bg-white/20 text-white' : 'bg-accent/10 text-accent'"
            >
              {{ t('subscription.custom_rule_effective_badge') }}
            </span>
          </button>
        </div>

        <div class="flex-1 min-h-0 overflow-y-auto flex flex-col gap-4 pr-1">
          <!-- 作用域提示：只有融合模式需要（切换模式的规则本来就只作用于该订阅，无需说明），
               文案由父组件传入；组件内不判模式 -->
          <p v-if="props.hint" class="text-xs text-slate-400 dark:text-slate-500 leading-normal">{{ props.hint }}</p>

          <div v-if="isLoading" class="flex items-center justify-center gap-2 py-8 text-xs text-slate-500 dark:text-slate-400">
            <div class="w-4 h-4 border-2 border-slate-300 dark:border-slate-700 !border-t-accent rounded-full animate-spin"></div>
            {{ t('common.loading') }}
          </div>

          <template v-else-if="activePayload && ruleForm">
            <p v-if="!activePayload.file_ready" class="text-xs text-warning leading-normal py-1">{{ t('subscription.custom_rule_not_ready') }}</p>
            <p v-else-if="hasNoRuleProviders" class="text-xs text-warning leading-normal py-1">{{ t('subscription.custom_rule_no_providers') }}</p>

            <fieldset :disabled="rulesFormDisabled" class="min-w-0 space-y-3" :class="{ 'opacity-60': rulesFormDisabled }">
              <div class="flex flex-col gap-1.5">
                <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.custom_rule_type') }}</label>
                <select v-model="ruleForm.type" class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm">
                  <option v-for="spec in ruleTypes" :key="spec.type" :value="spec.type">{{ spec.type }}</option>
                </select>
              </div>

              <div class="flex flex-col gap-1.5">
                <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.custom_rule_payload') }}</label>
                <select v-if="isRuleSetType" v-model="ruleForm.payload" class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm">
                  <option v-for="provider in ruleProviders" :key="provider" :value="provider">{{ provider }}</option>
                </select>
                <input v-else type="text" v-model="ruleForm.payload" :placeholder="selectedRuleType ? selectedRuleType.example : ''" class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm" />
              </div>

              <div class="flex flex-col gap-1.5">
                <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.custom_rule_target') }}</label>
                <select v-model="ruleForm.target" class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm">
                  <optgroup :label="t('subscription.custom_rule_builtin_label')">
                    <option v-for="builtin in ruleBuiltins" :key="builtin" :value="builtin">{{ ruleTargetLabel(builtin) }}</option>
                  </optgroup>
                  <optgroup v-if="ruleGroups.length || ruleTargetExtra.length" :label="t('subscription.custom_rule_groups_label')">
                    <option v-for="group in ruleGroups" :key="group" :value="group">{{ group }}</option>
                    <!-- 历史规则的目标已不在可选列表（如引用了改名的代理组）：原样列出，避免下拉空选导致保存时被改写 -->
                    <option v-for="extra in ruleTargetExtra" :key="extra" :value="extra">{{ extra }}</option>
                  </optgroup>
                </select>
              </div>

              <div class="flex flex-col gap-1.5">
                <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">{{ t('subscription.custom_rule_position') }}</label>
                <div class="flex bg-slate-100 dark:bg-slate-800 rounded-lg p-0.5 transition-all">
                  <button
                    type="button"
                    @click="ruleForm.position = 'after'"
                    class="flex-1 px-3 py-1.5 text-xs font-semibold rounded-md transition-all duration-200"
                    :class="ruleForm.position === 'after' ? 'bg-accent text-white shadow-sm' : 'text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'"
                  >
                    {{ t('subscription.custom_rule_position_after') }}
                  </button>
                  <button
                    type="button"
                    @click="ruleForm.position = 'before'"
                    class="flex-1 px-3 py-1.5 text-xs font-semibold rounded-md transition-all duration-200"
                    :class="ruleForm.position === 'before' ? 'bg-accent text-white shadow-sm' : 'text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'"
                  >
                    {{ t('subscription.custom_rule_position_before') }}
                  </button>
                </div>
              </div>

              <label v-if="selectedRuleType && selectedRuleType.no_resolve" class="flex items-center gap-2 text-xs text-slate-600 dark:text-slate-400 select-none cursor-pointer">
                <input type="checkbox" v-model="ruleForm.no_resolve" class="w-3.5 h-3.5 rounded accent-accent" />
                {{ t('subscription.custom_rule_no_resolve') }}
              </label>

              <div class="flex items-center gap-2">
                <button
                  type="button"
                  @click="handleSubmitRule"
                  :disabled="!canSubmitRule"
                  class="flex-1 px-4 py-2 text-xs font-semibold rounded-lg bg-accent hover:bg-accent-hover text-white shadow-sm transition-all flex items-center justify-center gap-1.5 disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  <SyncOutline v-if="isSavingRule" class="w-3.5 h-3.5 animate-spin" />
                  <CreateOutline v-else-if="isEditingRule" class="w-4 h-4" />
                  <AddOutline v-else class="w-4 h-4" />
                  {{ isEditingRule ? t('subscription.custom_rule_save') : t('subscription.custom_rule_add') }}
                </button>
                <button
                  v-if="isEditingRule"
                  type="button"
                  @click="cancelEditRule"
                  class="px-4 py-2 text-xs font-semibold rounded-lg bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 transition-all"
                >
                  {{ t('subscription.custom_rule_cancel_edit') }}
                </button>
              </div>
            </fieldset>

            <div class="flex flex-col gap-2 border-t border-slate-100 dark:border-slate-800 pt-3">
              <div v-if="ruleList.length === 0" class="text-xs text-slate-400 dark:text-slate-600 py-3 text-center">
                {{ t('subscription.custom_rule_empty') }}
              </div>
              <div
                v-for="rule in ruleList"
                :key="rule.id"
                class="flex items-start gap-2 p-2.5 rounded-lg border bg-slate-50/50 dark:bg-slate-900/30"
                :class="editingRuleId === rule.id ? 'border-accent' : 'border-slate-200/60 dark:border-slate-800/60'"
              >
                <div class="min-w-0 flex-1 flex flex-col gap-1.5">
                  <code class="font-mono text-[11px] break-all text-slate-700 dark:text-slate-200">{{ rule.line }}</code>
                  <div class="flex flex-wrap items-center gap-1.5">
                    <span class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-slate-500/10 text-slate-500 dark:text-slate-400">
                      {{ rule.position === 'before' ? t('subscription.custom_rule_before_badge') : t('subscription.custom_rule_after_badge') }}
                    </span>
                    <span v-if="editingRuleId === rule.id" class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-accent/10 text-accent">
                      {{ t('subscription.custom_rule_editing') }}
                    </span>
                    <span v-if="!rule.valid" class="px-1.5 py-0.5 rounded text-[10px] font-semibold bg-red-500/10 text-red-500">
                      {{ t('subscription.custom_rule_invalid_badge') }}
                    </span>
                    <span v-if="!rule.valid && rule.reason" class="text-[10px] text-red-500 break-all">{{ rule.reason }}</span>
                  </div>
                </div>
                <button
                  @click="startEditRule(rule)"
                  class="p-2 hover:bg-slate-200 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 rounded-lg transition-all shrink-0"
                  :title="t('subscription.custom_rule_edit')"
                >
                  <CreateOutline class="w-4 h-4" />
                </button>
                <button
                  @click="handleMoveRule(rule, 'up')"
                  :disabled="isRuleBusy || !moveLimitsFor(rule.id).canMoveUp"
                  class="p-2 hover:bg-slate-200 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 rounded-lg transition-all disabled:opacity-40 disabled:cursor-not-allowed shrink-0"
                  :title="t('subscription.custom_rule_move_up')"
                >
                  <SyncOutline v-if="movingRuleId === rule.id && movingDirection === 'up'" class="w-4 h-4 animate-spin" />
                  <ArrowUpOutline v-else class="w-4 h-4" />
                </button>
                <button
                  @click="handleMoveRule(rule, 'down')"
                  :disabled="isRuleBusy || !moveLimitsFor(rule.id).canMoveDown"
                  class="p-2 hover:bg-slate-200 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 rounded-lg transition-all disabled:opacity-40 disabled:cursor-not-allowed shrink-0"
                  :title="t('subscription.custom_rule_move_down')"
                >
                  <SyncOutline v-if="movingRuleId === rule.id && movingDirection === 'down'" class="w-4 h-4 animate-spin" />
                  <ArrowDownOutline v-else class="w-4 h-4" />
                </button>
                <button
                  @click="handleDeleteRule(rule)"
                  :disabled="isRuleBusy"
                  class="p-2 hover:bg-red-500/10 hover:text-red-500 text-slate-500 dark:text-slate-400 rounded-lg transition-all shrink-0 disabled:opacity-50 disabled:cursor-not-allowed"
                  :title="t('common.delete')"
                >
                  <SyncOutline v-if="deletingRuleId === rule.id" class="w-4 h-4 animate-spin" />
                  <TrashOutline v-else class="w-4 h-4" />
                </button>
              </div>
            </div>
          </template>

          <p v-else class="text-xs text-danger leading-normal py-1">{{ t('subscription.custom_rule_load_failed') }}</p>
        </div>

        <div class="flex justify-end pt-3 border-t border-slate-100 dark:border-slate-800 shrink-0">
          <button @click="closeDialog" class="px-4 py-2 text-sm font-semibold rounded-lg bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-700 transition-all">
            {{ t('common.close') }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style>
@keyframes zoomIn {
  from { opacity: 0; transform: scale(0.95); }
  to { opacity: 1; transform: scale(1); }
}
</style>
