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
let mockTproxyDstExceptions = ['# 公共 DNS 服务器', '223.5.5.5', '1.12.12.12']
let mockTproxySrcExceptions = ['# Docker 默认网段', '172.17.0.0/16']

// 模拟 HTTP API
export function handleMockFetch(path: string, options: RequestInit = {}): Response {
  const method = (options.method || 'GET').toUpperCase()
  const cleanPath = path.split('?')[0].replace(/\/$/, '')

  // 快捷响应封装
  const reply = (data: any, status = 200) => new Response(JSON.stringify(data), { status })

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
