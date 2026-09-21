<script setup lang="ts">
// 配置标题：中文界面下在中文标题后补一行英文小字。
//
// 内核配置项都以英文为准（wiki 与内核文档只给英文键名），中文界面下光看「路径」「请求头」
// 很难把它与 `path` / `headers` 对上，因此在纯中文标题旁附上英文名。
//
// 三种情况不追加：英文界面（标题本身就是英文）、两处文案相同（WebSocket / gRPC / REALITY
// 这类专有名词）、标题里本来就已经带着英文（「TLS 配置」「ECH 配置」「V2Ray HTTP Upgrade
// 快速打开」）——后者的英文名已在标题里露过面，再补一遍只会变成一长串重复。
//
// 仅「添加 / 编辑节点」弹窗使用：其它页面（订阅列表、规则、配置页）不需要这种对照。
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  /** 当前语言下的标题 */
  text: string
  /** 英文标题（后端下发的字段 label 或 en 文案）；缺省或与 text 相同则不显示 */
  english?: string
}>()

const { locale } = useI18n()

const showEnglish = computed(() => {
  const english = (props.english || '').trim()
  const text = props.text.trim()
  if (!english || english === text) return false
  if (!String(locale.value).startsWith('zh')) return false
  // 只给纯中文标题补英文：标题里已有拉丁字母时，英文名多半已在其中
  return !/[A-Za-z]/.test(text)
})
</script>

<template>
  <span>{{ text }}<span v-if="showEnglish" class="ml-1 text-[10px] font-normal text-slate-400 dark:text-slate-500">{{ english }}</span></span>
</template>
