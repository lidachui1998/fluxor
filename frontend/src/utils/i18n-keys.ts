/**
 * 动态 i18n key 的安全映射。
 *
 * 直接拼接（如 `t('config.theme_' + theme)`）有两个隐患：
 *  1. 大小写/拼写一旦与 key 定义不符，vue-i18n 会**原样打印 key 字符串**到界面上；
 *  2. 取值来自 localStorage 等外部输入时，脏数据会直接泄漏为可见文案。
 *
 * 因此对这类"有限枚举"的动态 key，一律走显式白名单映射，并提供兜底项。
 */

export const PROXY_MODES = ['Rule', 'Global', 'Direct'] as const
export type ProxyMode = typeof PROXY_MODES[number]

const MODE_KEYS: Record<string, string> = {
  Rule: 'config.mode_rule',
  Global: 'config.mode_global',
  Direct: 'config.mode_direct',
}

/** 代理模式 → i18n key，未知值兜底为 Rule */
export function modeI18nKey(mode: string | null | undefined): string {
  return MODE_KEYS[String(mode)] ?? MODE_KEYS.Rule
}

export const THEMES = ['light', 'dark', 'purple', 'pink', 'green', 'blue', 'system'] as const
export type ThemeName = typeof THEMES[number]

const THEME_KEYS: Record<string, string> = {
  light: 'config.theme_light',
  dark: 'config.theme_dark',
  purple: 'config.theme_purple',
  pink: 'config.theme_pink',
  green: 'config.theme_green',
  blue: 'config.theme_blue',
  system: 'config.theme_system',
}

/** 是否为受支持的主题名（用于校验 localStorage 中的历史脏值） */
export function isThemeName(value: unknown): value is ThemeName {
  return typeof value === 'string' && (THEMES as readonly string[]).includes(value)
}

/** 主题名 → i18n key，未知值兜底为 system */
export function themeI18nKey(theme: string | null | undefined): string {
  return THEME_KEYS[String(theme)] ?? THEME_KEYS.system
}
