import { ref, onActivated, onDeactivated } from 'vue'

/**
 * 视图激活态（配合 <KeepAlive> 使用）。
 *
 * 视图被 KeepAlive 缓存：切走时组件实例并不卸载，只触发 onDeactivated。
 * 而 <Teleport to="body"> 的内容渲染在 body 下，完全不受组件停用影响——
 * 若不加以约束，上一个页面的弹窗会继续浮在新页面上，它持有的 body 滚动锁
 * （overflow-hidden）也会一并残留，导致新页面无法滚动。
 *
 * 因此**所有 Teleport 弹窗的可见性都必须与本状态相与**：
 *   <Teleport to="body"><div v-if="isActive && showDialog">
 * 这样弹窗状态仍随 KeepAlive 保留（切回时原样恢复），但绝不会跨页显现。
 */
export function useViewActive() {
  const isActive = ref(true)
  onActivated(() => { isActive.value = true })
  onDeactivated(() => { isActive.value = false })
  return isActive
}
