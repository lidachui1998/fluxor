import type { CustomRule, CustomRulesPayload } from '../store/subscription'

// 模拟后端数据库，维持状态更改
let coreRunning = true
const mockConfigs = {
  'allow-lan': true,
  ipv6: false,
  mode: 'Rule',
  'log-level': 'info',
  'interface-name': 'eth0',
  tun: { enable: false, stack: 'System', device: '' },
  port: 0,
  'socks-port': 0,
  'redir-port': 0,
  'tproxy-port': 0,
  'mixed-port': 7890
}

const mockSubConfig = {
  proxy_port: 7890,
  panel_port: 9090,
  panel_secret: 'secret123',
  rule_group: 'base',
  ui_panel: 'metacubexd',
  meta_backend_url: '',
  tproxy_port: 7893,
  subscriptions: [
    {
      name: 'Sub-Mock-01',
      url: 'https://example.com/subscribe',
      update_interval: 3600,
      health_interval: 300,
      prefix: '',
      info: {
        upload: 1204850123,
        download: 58941094120,
        total: 107374182400,
        expire: Math.floor(Date.now() / 1000) + 864000,
        updatedAt: new Date().toISOString()
      }
    }
  ]
}

const mockProxies: any = {
  GLOBAL: {
    name: 'GLOBAL',
    type: 'Selector',
    now: '节点选择',
    all: ['节点选择', '自动选择', 'DIRECT', 'REJECT']
  },
  '节点选择': {
    name: '节点选择',
    type: 'Selector',
    now: '香港 01 (XUDP)',
    all: ['自动选择', '香港 01 (XUDP)', '日本 01 [IPLc]', 'DIRECT']
  },
  '自动选择': {
    name: '自动选择',
    type: 'URLTest',
    now: '香港 01 (XUDP)',
    all: ['香港 01 (XUDP)', '日本 01 [IPLc]']
  },
  '香港 01 (XUDP)': {
    name: '香港 01 (XUDP)',
    type: 'Shadowsocks',
    udp: true,
    history: [{ time: new Date().toISOString(), delay: 42 }]
  },
  '日本 01 [IPLc]': {
    name: '日本 01 [IPLc]',
    type: 'Vmess',
    udp: true,
    history: [{ time: new Date().toISOString(), delay: 110 }]
  },
  'DIRECT': { name: 'DIRECT', type: 'Direct', history: [] },
  'REJECT': { name: 'REJECT', type: 'Reject', history: [] }
}

const mockRuleProviders = {
  providers: {
    "AdBlock": { name: "AdBlock", type: "Rule", behavior: "classical", ruleCount: 1250, updatedAt: "2026-06-20T10:15:30Z" },
    "GeoIP-CN": { name: "GeoIP-CN", type: "Rule", behavior: "ipcidr", ruleCount: 5400, updatedAt: "2026-06-20T11:00:00Z" }
  }
}

const mockRules = {
  rules: [
    { type: "DomainSuffix", payload: "google.com", proxy: "节点选择" },
    { type: "IPCIDR", payload: "192.168.0.0/16", proxy: "DIRECT" },
    { type: "Match", payload: "", proxy: "GLOBAL" }
  ]
}

let activeMockConns = [
  {
    id: 'c-100',
    metadata: { host: 'github.com', destinationIP: '140.82.113.3', destinationPort: 443, type: 'TLS', network: 'tcp' },
    upload: 15400,
    download: 245000,
    rule: 'DomainKeyword',
    chains: ['GLOBAL', '节点选择', '香港 01 (XUDP)'],
    start: new Date(Date.now() - 60000).toISOString()
  }
]

// TProxy 状态模拟（与后端语义保持一致）
let mockTproxyEnabled = false
let mockTproxyProxyLocal = true
let mockTproxyIPv6 = false
let mockTproxyDstExceptions = ['# 公共 DNS 服务器', '223.5.5.5', '1.12.12.12']
let mockTproxySrcExceptions = ['# Docker 默认网段', '172.17.0.0/16']

// 切换模式订阅的自定义规则（按订阅名隔离，模拟后端的即时持久化）
const mockCustomRulesBySub: Record<string, CustomRule[]> = {
  'Sub-Mock-01': [
    { id: 'mock-rule-01', type: 'DOMAIN-SUFFIX', payload: 'ads.example.com', target: 'REJECT', position: 'after', line: 'DOMAIN-SUFFIX,ads.example.com,REJECT', valid: true },
    { id: 'mock-rule-02', type: 'RULE-SET', payload: 'gone', target: '已改名的组', position: 'after', line: 'RULE-SET,gone,已改名的组', valid: false, reason: '规则集 "gone" 不存在于该订阅的 rule-providers 中' }
  ]
}

// 与后端 configcheck.RuleSpecs() 保持一致的类型白名单及载荷示例
const mockCustomRuleTypes = [
  { type: 'DOMAIN', example: 'example.com', no_resolve: false },
  { type: 'DOMAIN-SUFFIX', example: 'example.com', no_resolve: false },
  { type: 'DOMAIN-KEYWORD', example: 'example', no_resolve: false },
  { type: 'DOMAIN-REGEX', example: '^ads\\..*$', no_resolve: false },
  { type: 'GEOSITE', example: 'github', no_resolve: false },
  { type: 'GEOIP', example: 'CN', no_resolve: true },
  { type: 'IP-CIDR', example: '1.1.1.0/24', no_resolve: true },
  { type: 'IP-CIDR6', example: '2001:db8::/32', no_resolve: true },
  { type: 'IP-SUFFIX', example: '1.1.1.0/24', no_resolve: true },
  { type: 'IP-ASN', example: '13335', no_resolve: true },
  { type: 'SRC-IP-CIDR', example: '192.168.1.0/24', no_resolve: true },
  { type: 'SRC-PORT', example: '443', no_resolve: false },
  { type: 'DST-PORT', example: '443', no_resolve: false },
  { type: 'PROCESS-NAME', example: 'curl', no_resolve: false },
  { type: 'NETWORK', example: 'tcp', no_resolve: false },
  { type: 'RULE-SET', example: '从该订阅的规则集中选择', no_resolve: true }
]

// 融合模式的两档规则集：各自的代理组与规则集清单都不同（与后端 configgen 模板一致），
// 因此两档的规则必须分开存放——同一份列表放在两档下必然有一半指向不存在的目标。
// base ≈ 5 个代理组且没有 rule-providers；full ≈ 38 个代理组 + 31 个规则集（此处取代表样子集）。
const mockMergeBaseGroups = ['🚀 节点选择', '👉 手动选择', '♻️ 自动选择', '🐟 漏网之鱼', '🎯 全球直连']
const mockMergeFullGroups = [
  '🚀 节点选择', '👉 手动选择', '♻️ 自动选择', '📈 网络测试', '🕹️ 游戏平台', '🤖 AI 平台',
  '🎬 Prime Video', '📹 油管视频', '🎵 TikTok', '🇨🇳 国内域名', '🀄️ 国内 IP',
  '🐟 漏网之鱼', '🛑 广告域名', '🎯 全球直连', '🇭🇰 香港节点', '🇯🇵 日本节点',
]
// base 档位没有 rule-providers，RULE-SET 在这里无从选择（前端会禁用表单）
const mockMergeBaseProviders: string[] = []
const mockMergeFullProviders = ['ads', 'cn', 'gfw']

// 融合模式的自定义规则（按档位 base/full 隔离，互不影响）
const mockMergeCustomRules: Record<string, CustomRule[]> = {
  base: [],
  full: [
    { id: 'mock-merge-rule-01', type: 'RULE-SET', payload: 'ads', target: '🛑 广告域名', position: 'before', line: 'RULE-SET,ads,🛑 广告域名', valid: true },
  ],
}

const mockCustomRuleGroups = ['节点选择', '自动选择', '广告拦截']
const mockCustomRuleBuiltins = ['DIRECT', 'REJECT', 'PASS']
const mockCustomRuleProviders = ['ads', 'private']
// 支持 no-resolve 的类型（IP 类与 RULE-SET），用于组装规则行
const mockCustomRuleNoResolveTypes = mockCustomRuleTypes.filter(t => t.no_resolve).map(t => t.type)

// 与后端一致：响应中的 rules 已按生效顺序排列（before 组整体在前、after 组整体在后），前端原样渲染
const orderMockCustomRules = (rules: any[]) => [
  ...rules.filter(r => r.position === 'before'),
  ...rules.filter(r => r.position !== 'before')
]

// 递增序号：id 是列表 key 也是编辑/排序的定位依据，同一毫秒内连续写入不能重复
let mockRuleSeq = 0
const nextMockRuleId = () => `mock-rule-${Date.now()}-${++mockRuleSeq}`

// 由请求体组装一条规则：规则行由结构化字段拼装，no-resolve 仅对支持的类型生效，position 缺省为最前
const buildMockCustomRule = (body: any, id: string) => {
  const type = String(body.type || '').toUpperCase()
  const payload = String(body.payload || '').trim()
  const target = String(body.target || '').trim()
  const noResolve = !!body.no_resolve && mockCustomRuleNoResolveTypes.includes(type)
  return {
    id,
    type,
    payload,
    target,
    position: body.position === 'after' ? 'after' : 'before',
    no_resolve: noResolve,
    line: [type, payload, target].join(',') + (noResolve ? ',no-resolve' : ''),
    valid: true
  }
}

// 快捷响应封装（模块级：合并规则的处理函数同样需要）
const reply = (data: any, status = 200) => new Response(JSON.stringify(data), { status })

// 组装自定义规则接口的统一响应体（切换/融合两种模式同构，仅 groups/providers 不同；
// status/message 仅写操作时携带）
const buildMockCustomRulesPayload = (
  rules: CustomRule[],
  groups: string[],
  providers: string[],
  status?: 'ok' | 'warning',
  message?: string,
): CustomRulesPayload => ({
  file_ready: true,
  rules: orderMockCustomRules(rules),
  groups,
  builtins: mockCustomRuleBuiltins,
  providers,
  rule_types: mockCustomRuleTypes,
  status,
  message
})

// 处理 /subscribe/custom-rules 与 /subscribe/merge-custom-rules 的共用逻辑：
// 两者的方法语义完全一致，只有「作用域 → 规则数组/groups/providers」的映射不同。
//
// 返回 null 表示当前方法不是本模块写的（调用方继续向下匹配），
// rawQuery 需传入含查询串的原始 path——DELETE 的 id 只能从这里取。
const handleMockCustomRulesRequest = (
  rules: CustomRule[],
  groups: string[],
  providers: string[],
  rawPath: string,
  method: string,
  bodyRaw: string | undefined,
): Response | null => {
  if (method === 'POST') {
    const body = JSON.parse(bodyRaw || '{}')
    rules.push(buildMockCustomRule(body, nextMockRuleId()))
    return reply(buildMockCustomRulesPayload(rules, groups, providers, 'ok'))
  }

  if (method === 'PUT') {
    const body = JSON.parse(bodyRaw || '{}')
    const idx = rules.findIndex(r => r.id === body.id)
    if (idx < 0) return reply({ status: 'error', message: '规则不存在: ' + body.id }, 404)
    // 就地替换：索引与 id 都保持原样，列表位置不变（与后端 PUT 语义一致）
    rules[idx] = buildMockCustomRule(body, rules[idx].id)
    return reply(buildMockCustomRulesPayload(rules, groups, providers, 'ok'))
  }

  if (method === 'PATCH') {
    const body = JSON.parse(bodyRaw || '{}')
    const direction = body.direction === 'up' ? 'up' : 'down'
    // 交换在生效顺序上进行，且只与同 position 分组内的相邻规则交换
    const ordered = orderMockCustomRules(rules)
    const idx = ordered.findIndex(r => r.id === body.id)
    if (idx < 0) return reply({ status: 'error', message: '规则不存在: ' + body.id }, 404)
    const swapIdx = direction === 'up' ? idx - 1 : idx + 1
    const neighbour = ordered[swapIdx]
    // 边界（已是该分组首/末条）回 400，而不是假装成功
    if (!neighbour || neighbour.position !== ordered[idx].position) {
      return reply({ status: 'error', message: direction === 'up' ? '规则已在该分组的最前，无法继续移动' : '规则已在该分组的最后，无法继续移动' }, 400)
    }
    const moved = ordered[idx]
    ordered[idx] = neighbour
    ordered[swapIdx] = moved
    // 交换结果按生效顺序写回原数组
    ordered.forEach((r, i) => { rules[i] = r })
    return reply(buildMockCustomRulesPayload(rules, groups, providers))
  }

  if (method === 'DELETE') {
    // 原始 path 的查询串已随 cleanPath 一起剥离，id 需从这里取
    const id = decodeURIComponent(new URLSearchParams(rawPath.split('?')[1] || '').get('id') || '')
    const idx = rules.findIndex(r => r.id === id)
    if (idx < 0) return reply({ status: 'error', message: '规则不存在: ' + id }, 404)
    rules.splice(idx, 1)
    return reply(buildMockCustomRulesPayload(rules, groups, providers, 'ok'))
  }

  if (method === 'GET') return reply(buildMockCustomRulesPayload(rules, groups, providers))

  return null
}

// 模拟 HTTP API
export function handleMockFetch(path: string, options: RequestInit = {}): Response {
  const method = (options.method || 'GET').toUpperCase()
  const cleanPath = path.split('?')[0].replace(/\/$/, '')

  if (cleanPath.endsWith('/core/status')) return reply({ running: coreRunning })
  // 后端编译期注入的版本号（dev 模式下用固定值模拟）
  if (cleanPath.endsWith('/app-version')) return reply({ version: '1.0.0' })
  if (cleanPath.endsWith('/check-update')) return reply({ hasUpdate: false, current: '1.0.0' })
  if (cleanPath.endsWith('/core/start')) { coreRunning = true; return reply({ status: 'ok' }) }
  if (cleanPath.endsWith('/core/stop')) { coreRunning = false; return reply({ status: 'ok' }) }
  if (cleanPath.endsWith('/restart') || cleanPath.endsWith('/core/restart')) return reply({ status: 'ok' })
  if (cleanPath.endsWith('/version')) return reply({ version: 'v1.18.8-meta' })

  // TProxy：/config/tproxy/exceptions 与 /config/tproxy/proxy-local 路径更长，
  // 必须先匹配，否则会被 /config/tproxy 的前缀判断吞掉
  if (cleanPath.endsWith('/config/tproxy/exceptions')) {
    if (method === 'POST') {
      const body = JSON.parse(options.body as string || '{}')
      mockTproxyDstExceptions = body.dst || []
      mockTproxySrcExceptions = body.src || []
    }
    return reply({ dst: mockTproxyDstExceptions, src: mockTproxySrcExceptions })
  }
  if (cleanPath.endsWith('/config/tproxy/proxy-local')) {
    if (method === 'POST') {
      const body = JSON.parse(options.body as string || '{}')
      mockTproxyProxyLocal = !!body.enabled
    }
    return reply({ enabled: mockTproxyProxyLocal })
  }
  if (cleanPath.endsWith('/config/tproxy/proxy-ipv6')) {
    if (method === 'POST') {
      const body = JSON.parse(options.body as string || '{}')
      mockTproxyIPv6 = !!body.enabled
    }
    return reply({ enabled: mockTproxyIPv6 })
  }
  if (cleanPath.endsWith('/config/tproxy')) {
    if (method === 'POST') {
      const body = JSON.parse(options.body as string || '{}')
      const enable = !!body.enable
      // 与后端一致：端口为 0 时拒绝启用（后端返回 400）
      if (enable && (mockConfigs['tproxy-port'] || 0) === 0) {
        return reply({ status: 'error', message: 'TProxy 端口为 0，请先配置端口' }, 400)
      }
      mockTproxyEnabled = enable
    }
    return reply({ enabled: mockTproxyEnabled })
  }

  if (cleanPath.endsWith('/configs')) {
    if (method === 'PATCH' || method === 'PUT') {
      Object.assign(mockConfigs, JSON.parse(options.body as string || '{}'))
      return reply({ status: 'ok' })
    }
    return reply(mockConfigs)
  }

  if (cleanPath.endsWith('/subscribe/config')) {
    if (method === 'POST') {
      Object.assign(mockSubConfig, JSON.parse(options.body as string || '{}'))
      return reply({ status: 'ok' })
    }
    return reply(mockSubConfig)
  }
  if (cleanPath.endsWith('/subscribe/generate')) {
    // 与后端一致：保存订阅配置并落库，使随后的 /subscribe/config 能读到最新订阅列表
    if (method === 'POST') {
      const payload = JSON.parse(options.body as string || '{}')
      // delete_physical 是临时字段，后端不会持久化
      delete payload.delete_physical
      Object.assign(mockSubConfig, payload)
    }
    return reply({ status: 'ok' })
  }

  if (cleanPath.endsWith('/proxies')) return reply({ proxies: mockProxies })
  if (cleanPath.includes('/proxies/') && cleanPath.endsWith('/delay')) {
    const parts = cleanPath.split('/')
    const proxyName = decodeURIComponent(parts[parts.length - 2])
    const delay = Math.floor(30 + Math.random() * 200)
    if (mockProxies[proxyName]) {
      mockProxies[proxyName].history = [{ time: new Date().toISOString(), delay }]
    }
    return reply({ delay })
  }
  if (cleanPath.includes('/proxies/') && method === 'PUT') {
    const parts = cleanPath.split('/')
    const groupName = decodeURIComponent(parts[parts.length - 1])
    const body = JSON.parse(options.body as string || '{}')
    if (mockProxies[groupName]) {
      mockProxies[groupName].now = body.name
      return reply({ status: 'ok' })
    }
  }

  if (cleanPath.endsWith('/providers/rules')) return reply(mockRuleProviders)
  if (cleanPath.endsWith('/rules')) return reply(mockRules)
  if (cleanPath.includes('/cache/')) return reply({ status: 'ok' })

  if (cleanPath.endsWith('/dns/query')) {
    return reply({ Status: 0, Answer: [{ data: '192.168.1.100' }] })
  }
  if (cleanPath.endsWith('/interfaces')) return reply(['eth0', 'wlan0', 'meta'])

  // IP 信息模拟
  if (cleanPath.endsWith('/ipinfo/local/v4')) {
    return reply({ ip: '116.228.111.222', country: '中国', region: '上海', isp: '电信' })
  }
  if (cleanPath.endsWith('/ipinfo/local/v6')) return reply({ ip: '240e:3b3:3000:200::100' })
  if (cleanPath.endsWith('/ipinfo/proxy/v4')) {
    return reply({ ip: '104.244.42.1', country: '美国', region: '加利福尼亚', isp: 'Twitter Inc.' })
  }
  if (cleanPath.endsWith('/ipinfo/proxy/v6')) return reply({ ip: '2606:4700:3030::ac43:8ad7' })

  // 延迟测试模拟
  if (cleanPath.includes('/delaytest/')) {
    return reply({ delay: Math.floor(40 + Math.random() * 100) })
  }

  // 融合模式自定义规则（/subscribe/merge-custom-rules/{base|full}）：方法语义与切换模式一致，
  // 区别只在于作用域是规则集档位、两档各有自己的代理组与规则集清单（互不影响）。
  // 该前缀必须在切换模式那条更短的前缀之前匹配（两者字符串不同，此处仅作可读性排序）。
  if (cleanPath.includes('/subscribe/merge-custom-rules/')) {
    const ruleGroup = decodeURIComponent(cleanPath.split('/subscribe/merge-custom-rules/')[1] || '')
    if (ruleGroup !== 'base' && ruleGroup !== 'full') {
      return reply({ status: 'error', message: '未知规则集: ' + ruleGroup }, 400)
    }
    if (!mockMergeCustomRules[ruleGroup]) mockMergeCustomRules[ruleGroup] = []
    const groups = ruleGroup === 'full' ? mockMergeFullGroups : mockMergeBaseGroups
    const providers = ruleGroup === 'full' ? mockMergeFullProviders : mockMergeBaseProviders
    const resp = handleMockCustomRulesRequest(mockMergeCustomRules[ruleGroup], groups, providers, path, method, options.body as string | undefined)
    if (resp) return resp
  }

  // 订阅自定义规则（切换模式）：GET 查询 / POST 新增 / PUT 修改 / PATCH 排序 / DELETE 删除（?id=）
  // 规则按订阅名隔离，写操作立即改内存数组，与后端「即时持久化」语义一致
  if (cleanPath.includes('/subscribe/custom-rules/')) {
    const subName = decodeURIComponent(cleanPath.split('/subscribe/custom-rules/')[1] || '')
    if (!mockCustomRulesBySub[subName]) mockCustomRulesBySub[subName] = []
    const resp = handleMockCustomRulesRequest(
      mockCustomRulesBySub[subName],
      mockCustomRuleGroups,
      mockCustomRuleProviders,
      path,
      method,
      options.body as string | undefined,
    )
    if (resp) return resp
  }

  return reply({ error: 'Not Found' }, 404)
}

// 模拟推送的局部状态
let lastUp = 100000
let lastDown = 500000
let lastConnId = 100

// 模拟 WebSocket
export class MockWebSocket {
  url: string
  readyState: number = 0
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null
  private intervalId: any = null

  constructor(url: string, path: string) {
    this.url = url
    setTimeout(() => {
      this.readyState = 1
      if (this.onopen) this.onopen()
      this.startPushData(path)
    }, 50)
  }

  private startPushData(path: string) {
    this.intervalId = setInterval(() => {
      if (this.readyState !== 1) return
      let dataStr = ''

      if (path.includes('/traffic')) {
        lastUp = Math.max(5000, Math.floor(lastUp * (1 + (Math.random() - 0.48) * 0.2)))
        lastDown = Math.max(10000, Math.floor(lastDown * (1 + (Math.random() - 0.48) * 0.2)))
        dataStr = JSON.stringify({ up: lastUp, down: lastDown })
      } else if (path.includes('/memory')) {
        dataStr = JSON.stringify({ inuse: Math.floor(40000000 + Math.random() * 5000000) })
      } else if (path.includes('/logs')) {
        const logs = [
          '{"type":"info","payload":"[Proxy] switch Selector to Hong Kong 01"}',
          '{"type":"debug","payload":"[TCP] dial google.com:443 direct"}',
          '{"type":"info","payload":"[DNS] query baidu.com from 119.29.29.29"}'
        ]
        dataStr = logs[Math.floor(Math.random() * logs.length)]
      } else if (path.includes('/connections')) {
        // 增加流量
        activeMockConns.forEach(c => {
          c.upload += Math.floor(Math.random() * 2000)
          c.download += Math.floor(Math.random() * 10000)
        })
        // 概率关闭连接
        if (activeMockConns.length > 1 && Math.random() < 0.1) {
          activeMockConns.pop()
        }
        // 概率新连接
        if (activeMockConns.length < 4 && Math.random() < 0.15) {
          lastConnId++
          activeMockConns.push({
            id: `c-${lastConnId}`,
            metadata: { host: 'youtube.com', destinationIP: '172.217.160.78', destinationPort: 443, type: 'TLS', network: 'tcp' },
            upload: 100,
            download: 1000,
            rule: 'Match',
            chains: ['GLOBAL', '节点选择', '日本 01 [IPLc]'],
            start: new Date().toISOString()
          })
        }
        dataStr = JSON.stringify({ connections: activeMockConns })
      }

      if (dataStr && this.onmessage) {
        this.onmessage(new MessageEvent('message', { data: dataStr }))
      }
    }, 1000)
  }

  close() {
    this.readyState = 3
    if (this.intervalId) {
      clearInterval(this.intervalId)
      this.intervalId = null
    }
    setTimeout(() => {
      if (this.onclose) this.onclose()
    }, 10)
  }
}

// 模拟后端的内核状态 SSE（/core/events）。
//
// 与真实实现保持同样的语义：连接后先推一次当前快照，之后仅在状态变化时推送。
// 这里通过轮询本地 coreRunning 变量来模拟「后端主动推送」。
export function createMockSse(
  // 与真实 sseConnect 保持同一签名；mock 仅模拟 /core/events 单一事件源，
  // 故路径本身不参与逻辑，用下划线前缀显式标记为有意未使用。
  _path: string,
  onEvent: (data: any) => void,
  handlers: { onOpen?: () => void; onError?: (ev: Event) => void } = {}
) {
  let lastSent: boolean | null = null
  let closed = false
  let pollTimer: any = null

  const openTimer = setTimeout(() => {
    if (closed) return
    if (handlers.onOpen) handlers.onOpen()
    // 首次快照
    lastSent = coreRunning
    onEvent({ running: coreRunning, ts: Date.now() })

    // 每 500ms 检查一次，状态变化时推送（模拟服务端主动推送）
    pollTimer = setInterval(() => {
      if (closed) return
      if (coreRunning !== lastSent) {
        lastSent = coreRunning
        onEvent({ running: coreRunning, ts: Date.now() })
      }
    }, 500)
  }, 50)

  return {
    close: () => {
      closed = true
      clearTimeout(openTimer)
      if (pollTimer) clearInterval(pollTimer)
    }
  }
}
