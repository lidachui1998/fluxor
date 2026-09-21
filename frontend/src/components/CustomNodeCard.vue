<script setup lang="ts">
// 自定义模式下的单个节点卡片（纯展示 + 两个动作事件）。
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { CreateOutline, EyeOutline, EyeOffOutline, TrashOutline } from '@vicons/ionicons5'
import type { CustomNode } from '../store/subscription'

const props = defineProps<{
  node: CustomNode
  /** 协议展示名（由父组件按协议表解析，卡片不访问 store） */
  protocolName: string
}>()

defineEmits<{
  (e: 'edit'): void
  (e: 'delete'): void
}>()

const { t } = useI18n()

// 地址/网络名称默认以密文展示（订阅卡片链接的同一处理方式），点小眼睛才展开
const addressVisible = ref(false)

// 节点摘要：优先「服务器:端口」，组网类协议（无 server）回落到各自的标识字段
const summary = computed(() => {
  const cfg = props.node.config || {}
  const server = typeof cfg.server === 'string' ? cfg.server : ''
  const port = cfg.port
  if (server && port) return `${server}:${port}`
  if (server) return server
  for (const key of ['network', 'network-name', 'hostname', 'uri']) {
    const value = cfg[key]
    if (typeof value === 'string' && value) return value
  }
  return t('common.unknown')
})
</script>

<template>
  <div class="live-card p-4 rounded-xl border border-slate-200/40 dark:border-slate-800/40 bg-slate-50/50 dark:bg-slate-900/30 flex flex-col gap-2 hover:border-slate-300/80 dark:hover:border-slate-700/80 hover:-translate-y-[3px] hover:shadow-md hover:bg-slate-100/80 dark:hover:bg-slate-900/80 transition-all duration-300 relative overflow-hidden">
    <div class="flex justify-between items-start gap-3">
      <span class="min-w-0 font-semibold text-slate-800 dark:text-slate-100 break-all">{{ node.name }}</span>
      <div class="flex gap-1.5 shrink-0">
        <button @click="$emit('edit')" class="p-2 hover:bg-slate-200 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 rounded-lg transition-all" :title="t('common.edit')">
          <CreateOutline class="w-4 h-4" />
        </button>
        <button @click="$emit('delete')" class="p-2 hover:bg-red-500/10 hover:text-red-500 text-slate-500 dark:text-slate-400 rounded-lg transition-all" :title="t('common.delete')">
          <TrashOutline class="w-4 h-4" />
        </button>
      </div>
    </div>
    <div class="flex items-center gap-2 flex-wrap">
      <span class="px-2 py-0.5 rounded-md text-[11px] font-semibold bg-accent/10 text-accent">{{ protocolName }}</span>
      <button
        type="button"
        @click="addressVisible = !addressVisible"
        class="shrink-0 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 transition-all focus:outline-none"
        :title="addressVisible ? t('common.hide') : t('common.show')"
      >
        <EyeOutline v-if="addressVisible" class="w-3.5 h-3.5" />
        <EyeOffOutline v-else class="w-3.5 h-3.5" />
      </button>
      <span class="text-xs text-slate-400 dark:text-slate-500 break-all min-w-0" :class="{ 'select-all': addressVisible }">{{ addressVisible ? summary : '••••••••' }}</span>
    </div>
  </div>
</template>
