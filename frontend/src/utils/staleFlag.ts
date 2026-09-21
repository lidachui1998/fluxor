import { ref } from 'vue'

// 「内核运行数据已过期」标记工厂。
//
// 适用场景：某个页面的数据由内核运行态派生（代理组/节点、规则/规则提供商），
// 而改动发生在**另一个页面**（配置页重载 config.yaml、订阅页改自定义规则）。
// 此时无法就地刷新——改动的页面既不知道该页面是否已挂载、也不该替它发请求，
// 只登记一个标记；真正持有数据的页面在 KeepAlive 的 onActivated 里消费它并静默补拉。
//
// consume 采用「读取即清除」语义：标记只生效一次，用户不切到那个页面就永远不产生请求。
// 标记只存在于内存、刻意不持久化——它是「本次会话的待办」而非用户配置，
// 刷新页面即自然清除，绝不会把上一轮会话的标记带进来。
//
// 每个数据域（规则、代理…）各持一份独立的标记：共用一个会让先切过去的页面
// 吃掉另一个页面的待办，导致后者再也刷不到。
export const createStaleFlag = () => {
  const needsRefresh = ref(false)

  const markNeedsRefresh = () => {
    needsRefresh.value = true
  }

  const consumeNeedsRefresh = () => {
    if (!needsRefresh.value) return false
    needsRefresh.value = false
    return true
  }

  return { needsRefresh, markNeedsRefresh, consumeNeedsRefresh }
}
