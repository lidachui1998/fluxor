<script setup lang="ts">
// 使用 Vue 3.4+ defineModel 实现极致简洁的双向绑定
const model = defineModel<boolean>({ default: false })

// disabled：禁用态不改变值，以降透明 + 禁用光标提示不可操作
// （用于「启用 TProxy 时禁止修改本机流量接管」等场景）。
// disabledHint：置灰原因。挂到按钮 title（悬停可见），并在点击时 emit blocked
// 交由父组件弹提示——与「启用 TProxy 时的绕过设置齿轮」同一套风格。
// 注意：这里刻意不使用原生 disabled 属性，否则浏览器不派发 click，提示无从触发；
// 禁用语义改由 aria-disabled 承担。
const props = withDefaults(defineProps<{ disabled?: boolean; disabledHint?: string }>(), {
  disabled: false,
  disabledHint: ''
})

const emit = defineEmits<{ blocked: [] }>()

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
    class="w-10 h-6 flex items-center rounded-full p-0.5 transition-all outline-none duration-200"
    :class="[
      model ? 'bg-accent justify-end' : 'bg-slate-200 dark:bg-slate-700 justify-start',
      disabled ? 'opacity-40 cursor-not-allowed' : ''
    ]"
  >
    <span class="w-5 h-5 rounded-full bg-white shadow-md transition-transform duration-200"></span>
  </button>
</template>
