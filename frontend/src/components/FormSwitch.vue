<script setup lang="ts">
import { computed } from 'vue'

// 使用 Vue 3.4+ defineModel 实现极致简洁的双向绑定
const model = defineModel<boolean>({ default: false })

// size：default 用于页面里的常规开关；sm 用于列表行内（与状态徽标同排时必须让出正文宽度）。
// 两者只有尺寸差异，点击与禁用语义完全一致，因此不另做一个组件。
const props = withDefaults(defineProps<{ disabled?: boolean; disabledHint?: string; size?: 'default' | 'sm' }>(), {
  disabled: false,
  disabledHint: '',
  size: 'default'
})

const emit = defineEmits<{ blocked: [] }>()

// 轨道与滑块尺寸成对变化：滑块 = 轨道高度 − 上下内边距（p-0.5 = 2px×2），
// 否则小尺寸下滑块会被裁切或顶出轨道。
const trackClass = computed(() => (props.size === 'sm' ? 'w-7 h-4' : 'w-10 h-6'))
const knobClass = computed(() => (props.size === 'sm' ? 'w-3 h-3' : 'w-5 h-5'))

// disabled：禁用态不改变值，以降透明 + 禁用光标提示不可操作
// （用于「启用 TProxy 时禁止修改本机流量接管」等场景）。
// disabledHint：置灰原因。挂到按钮 title（悬停可见），并在点击时 emit blocked
// 交由父组件弹提示——与「启用 TProxy 时的绕过设置齿轮」同一套风格。
// 注意：这里刻意不使用原生 disabled 属性，否则浏览器不派发 click，提示无从触发；
// 禁用语义改由 aria-disabled 承担。
const toggle = () => {
  if (props.disabled) {
    emit('blocked')
    return
  }
  model.value = !model.value
}
</script>

<template>
  <button
    type="button"
    :aria-disabled="disabled || undefined"
    :title="disabled ? disabledHint : undefined"
    @click="toggle"
    class="flex items-center rounded-full p-0.5 transition-all outline-none duration-200"
    :class="[
      trackClass,
      model ? 'bg-accent justify-end' : 'bg-slate-200 dark:bg-slate-700 justify-start',
      disabled ? 'opacity-40 cursor-not-allowed' : ''
    ]"
  >
    <span class="rounded-full bg-white shadow-md transition-transform duration-200" :class="knobClass"></span>
  </button>
</template>
