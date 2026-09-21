package configgen

// proxyGroupsFullTemplate 是 full 规则集的代理组模板。
//
// 末尾 5 个按地区筛选的 url-test 组以 include-all-providers: true 引用「全部订阅
// provider」，再由 filter 正则筛出该地区的节点。模板中不出现任何订阅名，也就不需要占位符，
// 生成侧只做解析与合并、不做文本插值——订阅名含 `,` / `]` 等 YAML 特殊字符时不存在破坏
// 语法的可能。实测 mihomo v1.19.31：`use: [p1, p2]` 与 `include-all-providers: true`
// 的成员集合完全一致（带不带 filter 均是）。
//
// 前提是该档位的 use 列表恒等于全部订阅。将来若需要「只引用部分 provider」，必须退回
// use 列表并按 YAML 节点注入订阅名（切勿把名字拼进文本），否则语义会变成引用全部 provider。
//
// 另一处消费方是 mergerules.go：它读取该模板的组名作为自定义规则的可选目标，
// 与本注释涉及的引用方式无关。
const proxyGroupsFullTemplate = `
proxy-groups:
  - {name: 🚀 节点选择, type: select, proxies: [♻️ 自动选择, 👉 手动选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 👉 手动选择, type: select, include-all: true}
  - {name: 📈 网络测试, type: select, proxies: [🎯 全球直连, 🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🕹️ 游戏平台, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🤖 AI 平台, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🎮 游戏服务, type: select, proxies: [🎯 全球直连, 🚀 节点选择]}
  - {name: 🪟 微软服务, type: select, proxies: [🎯 全球直连, 🚀 节点选择]}
  - {name: 🇬 谷歌服务, type: select, proxies: [🎯 全球直连, 🚀 节点选择]}
  - {name: 🍎 苹果服务, type: select, proxies: [🎯 全球直连, 🚀 节点选择]}
  - {name: 🎥 奈飞视频, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 📽️ 迪士尼+, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🎞️ Max, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🎬 Prime Video, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🍎 Apple TV+, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 📹 油管视频, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🎵 TikTok, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 📺 哔哩哔哩, type: select, proxies: [🎯 全球直连, 🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🎶 Spotify, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 🌍 国外媒体, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: 📋 Trackerslist, type: select, proxies: [🎯 全球直连, 🚀 节点选择]}
  - {name: 🇨🇳 国内域名, type: select, proxies: [🎯 全球直连, 🚀 节点选择]}
  - {name: 🀄️ 国内 IP, type: select, proxies: [🎯 全球直连, 🚀 节点选择]}
  - {name: 🌎 国外顶级域名, type: select, proxies: [🚀 节点选择, 🎯 全球直连]}
  - {name: 🌎 国外域名, type: select, proxies: [🚀 节点选择, 🎯 全球直连]}
  - {name: 📲 电报消息, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点]}
  - {name: ⬇️ 直连软件, type: select, proxies: [🎯 全球直连], hidden: true}
  - {name: 🔒 私有网络, type: select, proxies: [🎯 全球直连], hidden: true}
  - {name: 🐟 漏网之鱼, type: select, proxies: [🚀 节点选择, 🇭🇰 香港节点, 🇹🇼 台湾节点, 🇯🇵 日本节点, 🇸🇬 新加坡节点, 🇺🇸 美国节点, 🎯 全球直连]}
  - {name: 🛑 广告域名, type: select, proxies: [🔴 全球拦截, 🟢 全球绕过]}
  - {name: 🔴 全球拦截, type: select, proxies: [REJECT], hidden: true}
  - {name: 🟢 全球绕过, type: select, proxies: [PASS], hidden: true}
  - {name: 🎯 全球直连, type: select, proxies: [DIRECT], hidden: true}

  - {name: 🇭🇰 香港节点, type: url-test, tolerance: 50, include-all-providers: true, filter: "(?i)(🇭🇰|港|hk|hongkong|hong kong)"}
  - {name: 🇹🇼 台湾节点, type: url-test, tolerance: 50, include-all-providers: true, filter: "(?i)(🇹🇼|台|tw|taiwan|tai wan)"}
  - {name: 🇯🇵 日本节点, type: url-test, tolerance: 50, include-all-providers: true, filter: "(?i)(🇯🇵|日|jp|japan)"}
  - {name: 🇸🇬 新加坡节点, type: url-test, tolerance: 50, include-all-providers: true, filter: "(?i)(🇸🇬|新|sg|singapore)"}
  - {name: 🇺🇸 美国节点, type: url-test, tolerance: 100, include-all-providers: true, filter: "(?i)(🇺🇸|美|us|unitedstates|united states)"}
  - {name: ♻️ 自动选择, type: url-test, tolerance: 100, include-all: true}
`
