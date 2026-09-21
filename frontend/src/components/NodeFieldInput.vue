<script setup lang="ts">
// 单个协议字段的输入控件：把「字段类型 → 控件形态」的映射收敛在一处，
// 弹窗只负责遍历字段表，各分区与嵌套块共用同一个控件（避免多处模板漂移）。
//
// 取值类型由后端声明（kind）决定：bool → boolean、int → number、
// list / map → 分隔文本、group → 子字段对象、其余为字符串。前端无法静态收窄成
// 联合类型，故用 any 承载（真正的类型校验与归一化在后端完成，前端只保证控件形态正确）。
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { EyeOutline, EyeOffOutline } from '@vicons/ionicons5'
import FormSwitch from './FormSwitch.vue'
import type { NodeFieldSpec } from '../store/subscription'

const props = defineProps<{
  field: NodeFieldSpec
  /** 所属协议（用于选择协议专属的字段文案，如 vmess 的 network 是「传输方式」） */
  protocolType: string
}>()

const value = defineModel<any>({ default: '' })

const { t, te } = useI18n()
const secretVisible = ref(false)

// 展示名：优先协议专属文案（subscription.node_field.<协议>.<键>），其次是通用文案
// （subscription.node_field.<键>），都没有时回落到后端下发的英文 label。
const label = computed(() => {
  const scoped = `subscription.node_field.${props.protocolType}.${props.field.key}`
  if (te(scoped)) return t(scoped)
  const shared = `subscription.node_field.${props.field.key}`
  return te(shared) ? t(shared) : props.field.label
})

// 键值对字段在界面上按「键: 值」每行一条编辑；后端同样接受该文本形态
const pairsPlaceholder = computed(() => t('subscription.node_pairs_placeholder'))

// 嵌套块：子字段以对象承载，子项的 v-model 直接写回对象属性
const nestedValue = (child: NodeFieldSpec) => {
  if (!value.value || typeof value.value !== 'object') value.value = {}
  return value.value[child.key]
}

const setNestedValue = (child: NodeFieldSpec, childValue: any) => {
  if (!value.value || typeof value.value !== 'object') value.value = {}
  value.value[child.key] = childValue
}
</script>

<template>
  <!-- 嵌套块：整块占满一行，标题即块名（如 WebSocket / REALITY / ECH） -->
  <div
    v-if="field.kind === 'group'"
    class="sm:col-span-2 rounded-xl border border-slate-200/70 dark:border-slate-700/60 bg-slate-50/60 dark:bg-slate-800/30 p-3 flex flex-col gap-3"
  >
    <div class="text-xs font-bold text-slate-600 dark:text-slate-300 flex items-center gap-1.5">
      {{ label }}
      <span class="font-mono text-[10px] font-normal text-slate-400 dark:text-slate-500">{{ field.key }}</span>
    </div>
    <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
      <NodeFieldInput
        v-for="child in field.children || []"
        :key="child.key"
        :field="child"
        :protocol-type="protocolType"
        :model-value="nestedValue(child)"
        @update:model-value="setNestedValue(child, $event)"
      />
    </div>
  </div>

  <div v-else class="flex flex-col gap-1.5" :class="{ 'sm:col-span-2': field.kind === 'text' || field.kind === 'map' }">
    <label class="text-xs font-semibold text-slate-600 dark:text-slate-400">
      {{ label }}<span v-if="field.required" class="text-danger"> *</span>
    </label>

    <FormSwitch
      v-if="field.kind === 'bool'"
      :model-value="value === true"
      @update:model-value="value = $event"
    />

    <select
      v-else-if="field.kind === 'select'"
      v-model="value"
      class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm"
    >
      <!-- 非必填且未预设默认值时允许留空：留空表示用内核自身的默认值 -->
      <option v-if="!field.required && !field.default" value="">{{ t('subscription.node_field_default_option') }}</option>
      <option v-for="option in field.options || []" :key="option" :value="option">{{ option }}</option>
    </select>

    <textarea
      v-else-if="field.kind === 'text'"
      v-model="value"
      rows="4"
      spellcheck="false"
      class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-xs font-mono"
    ></textarea>

    <textarea
      v-else-if="field.kind === 'map'"
      v-model="value"
      rows="3"
      spellcheck="false"
      :placeholder="pairsPlaceholder"
      class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-xs font-mono"
    ></textarea>

    <input
      v-else-if="field.kind === 'int'"
      type="number"
      step="1"
      v-model.number="value"
      class="px-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm"
    />

    <div v-else class="relative flex items-center">
      <input
        :type="field.secret && !secretVisible ? 'password' : 'text'"
        v-model="value"
        :placeholder="field.kind === 'list' ? t('subscription.node_list_placeholder') : ''"
        class="w-full pl-3.5 py-2 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 focus:ring-2 focus:ring-accent outline-none text-sm"
        :class="field.secret ? 'pr-10' : 'pr-3.5'"
      />
      <button
        v-if="field.secret"
        type="button"
        @click="secretVisible = !secretVisible"
        class="absolute right-3 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
      >
        <EyeOutline v-if="secretVisible" class="w-4 h-4" />
        <EyeOffOutline v-else class="w-4 h-4" />
      </button>
    </div>
  </div>
</template>
