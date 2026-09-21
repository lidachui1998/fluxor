# 持久化配置 (fluxor.json)

Fluxor 的设计核心之一是不引入复杂的数据库系统。所有的全局参数、机场订阅链接及拉取元数据，都持久化存储在一个 JSON 文件中。

---

## 配置文件路径

默认保存在 `/var/apps/Fluxor/var/fluxor.json`（真实落点为 `/vol1/@appdata/Fluxor/fluxor.json`）。

| 事项 | 说明 |
|------|------|
| 写入时机 | 点击网页上的「保存并应用」、修改订阅、切换 TProxy 开关、增删订阅自定义规则等操作时，由程序即时写回 |
| 写入方式 | 「读—改—写」，只更新自己负责的字段，不整文件覆写 |
| 建议 | **面板运行期间不要手动编辑该文件**，避免覆盖掉正在写入的内容 |

> 该文件同时保存**订阅配置**与 **TProxy 状态**（`tproxy_*` 字段）。面板内部对它的读写共用同一把文件锁，因此不存在「保存订阅时把 TProxy 配置清空」的问题；但外部手动编辑无法参与这把锁，故仍建议先停止面板再改。

---

## 配置字段格式参考

以下是 `fluxor.json` 的完整结构示例，用以帮助您理解底层参数的流动和映射：

```json
{
  "proxy_port": 7890,
  "tproxy_port": 7898,
  "panel_port": 9090,
  "panel_secret": "your-panel-controller-secret",
  "rule_group": "base",
  "ui_panel": "metacubexd",
  "meta_backend_url": "",
  "mode": "merge",
  "active_subscription": "",
  "merge_custom_rules": {
    "base": [
      {
        "id": "9f1c0b7a2d3e4f58",
        "type": "DOMAIN-SUFFIX",
        "payload": "ads.example.com",
        "target": "🎯 全球直连",
        "position": "before"
      }
    ],
    "full": [
      {
        "id": "1a2b3c4d5e6f7788",
        "type": "RULE-SET",
        "payload": "ads",
        "target": "🛑 广告域名",
        "position": "before"
      }
    ]
  },
  "tproxy_enabled": false,
  "tproxy_dst_exceptions": [],
  "tproxy_src_exceptions": [],
  "tproxy_proxy_local": false,
  "subscriptions": [
    {
      "name": "my_airport",
      "url": "https://example.com/sub/link",
      "update_interval": 86400,
      "health_interval": 300,
      "prefix": "香港",
      "custom_rules": [
        {
          "id": "a345c008a8f85685",
          "type": "DOMAIN-SUFFIX",
          "payload": "ads.example.com",
          "target": "REJECT",
          "position": "after"
        },
        {
          "id": "602a1b0615fb591a",
          "type": "IP-CIDR",
          "payload": "1.1.1.0/24",
          "target": "🚀 节点选择",
          "position": "before",
          "no_resolve": true
        }
      ],
      "updated_at": "2026-07-02T15:00:00Z",
      "subscription_info": {
        "upload": 10737418240,
        "download": 53687091200,
        "total": 536870912000,
        "expire": 1782979200
      }
    }
  ]
}
```

---

## 核心字段详解

### 全局参数

* **`proxy_port`**：混合富强端口（Mixed Port），该端口同时支持 HTTP 和 SOCKS5 富强协议，默认 `7890`。取值为 `0` 表示禁用。
* **`tproxy_port`**：透明富强网关端口（TProxy Port），专门供 nftables 防火墙重定向劫持流量使用，默认 `7898`。
* **`panel_port`**：外部控制器监听端口，外置面板（如 MetaCubeXD）会通过该端口发送 HTTP API/WS 请求，默认 `9090`。
* **`panel_secret`**：保护内核 **TCP 外部控制端口**（`panel_port`）的密钥，外置面板（MetaCubeXD / Zashboard）或其它客户端直连该端口时需要提供。密钥不会下发给浏览器；Fluxor 面板自身与内核之间的通信走 UNIX Socket，内核对 unix 来源默认信任、不校验密钥，因此该链路不携带认证头。
* **`rule_group`**：当前选用的规则分流集模板名称，取值为 `base`（标准）或 `full`（详细）。
* **`ui_panel`**：关联的外部控制面板界面类型，取值为 `metacubexd` 或 `zashboard`。
* **`meta_backend_url`**：外部面板回连面板后端所用的地址，留空表示使用默认推导值。
* **`mode`**：订阅加载方式，取值为 `merge`（融合模式）或 `switch`（切换模式）。详见 [订阅工作模式](./subscription-modes)。
* **`active_subscription`**：当处于切换模式（`switch`）时，当前正在生效并激活的订阅名。
* **`merge_custom_rules`**：**融合模式**的自定义规则，**按规则集档位分开存放**（`base` 标准 / `full` 详细）。两个档位的代理组与规则集完全不同，因此各存一份、各自生效——只有当前 `rule_group` 那一份会写进 `config.yaml`，另一份保持惰性。字段结构与订阅级 `custom_rules` 完全一致（见下方「订阅数组」），由「订阅配置」页的自定义规则弹窗按档位页面维护。

### TProxy 状态字段

这几个字段由 TProxy 功能单独维护，通常不需要手工编辑：

* **`tproxy_enabled`**：透明富强开关的持久化值。**注意**：nftables 规则不跨重启存活，因此面板每次启动都会把该值**归零**并清理残留规则，确保「面板显示的状态」与「系统实际生效的规则」一致。
* **`tproxy_dst_exceptions`** / **`tproxy_src_exceptions`**：目的 / 源绕过列表（字段名沿用历史命名，界面统一叫「绕过」）。
* **`tproxy_proxy_local`**：是否接管面板主机自身的出站流量（界面上的「接管本机流量」开关）。

> 旧版本曾使用 `tproxy_exceptions` 单一字段，现在会自动迁移到分离的源/目的两个字段。

### 订阅数组

* **`name`**：订阅名，作为节点的来源标识与文件名依据。
* **`url`**：机场订阅链接（`http(s)://`）。
* **`update_interval`**：以秒为单位的自动静默更新间隔（`0` 表示不自动更新）。
* **`health_interval`**：以秒为单位的后台健康测速频率。
* **`prefix`**：**节点名称前缀**（不是过滤正则）。填写后生成的节点名会自动带上该前缀，对应内核 `proxy-provider` 的 `override.additional-prefix`，用于多订阅时区分节点来源。
* **`custom_rules`**：**切换模式**下该订阅的自定义规则列表（融合模式不使用，改用顶层的 `merge_custom_rules`）。每条规则由结构化字段组成，写入 `config.yaml` 时由面板组装成内核规则行：

  | 字段 | 说明 |
  |------|------|
  | `id` | 面板生成的稳定标识，用于修改 / 排序 / 删除单条规则 |
  | `type` | 规则类型（如 `DOMAIN-SUFFIX`、`RULE-SET`），取值受面板白名单限制 |
  | `payload` | 规则取值（如 `ads.example.com`、`1.1.1.0/24`、规则集名称） |
  | `target` | 命中目标：该订阅自带的代理组，或 `DIRECT` / `REJECT` / `PASS` |
  | `position` | `before`（默认，插到规则最前，优先级高于订阅自带规则）或 `after`（插在最后一条 `MATCH` 之前） |
  | `no_resolve` | 仅 IP 类与 `RULE-SET` 规则使用，跳过域名解析 |

  数组顺序即生效顺序：`before` 组的规则按数组顺序插在规则最前，`after` 组的规则按数组顺序插在最后一条 `MATCH` 之前；界面上的上/下移动只在同组内交换，并即时写回本文件。

  规则由「订阅配置」页的自定义规则弹窗维护，逐条即时写入本文件；目标已失效的规则会被跳过而不写入 `config.yaml`（内核遇到无法解析的目标会拒绝加载整份配置），并在弹窗中标注原因。详见 [订阅工作模式](./subscription-modes)。
* **`updated_at`**：最近一次成功更新的时间。
* **`subscription_info`**：机场返回的流量配额，包含已用上传（`upload`）、已用下载（`download`）、总配额（`total`）与过期时间戳（`expire`），会在「订阅配置」页作为流量卡片渲染。

---

## 不会保存在这里的配置

以下内容**不在** `fluxor.json` 中，避免产生误解：

| 内容 | 实际存放位置 |
|------|--------------|
| 主题、语言、默认启动页 | 浏览器 `localStorage`（每台设备各自独立） |
| 代理页的排序方式、延迟阈值、节点过滤正则、自动断开开关 | 浏览器 `localStorage` |
| 内核的运行参数（端口以外的 allow-lan / ipv6 / mode / TUN 等） | 内核自身的 `config.yaml`，由面板通过内核 API 修改 |
| 自定义延迟测试 URL | 浏览器 `localStorage` |
