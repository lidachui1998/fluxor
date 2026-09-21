package configgen

// Package configgen 负责把 SubscribeConfig 渲染为内核可运行的 config.yaml。
//
// 本包为纯函数式的字符串模板拼装层，不持有状态、不访问网络、不依赖任何其它
// internal 子包。它被 core（自动生成基础配置）与 subscription（按订阅生成完整
// 配置）共同复用，独立成包可同时打破二者的循环依赖。
//
// 产物由三部分拼接而成：
//  1. template_base.go  基础字段骨架（端口、外部控制、TUN、sniffer 等）
//  2. groups_*/providers_*/rules_*  按规则集档位（lite / full）追加的代理组与规则
//  3. template_dns.go   统一注入的 DNS 块
//
// 写盘前统一经 configcheck.Doc.OrderTopLevel 归一化顶层键序：标量键（端口、模式、密钥、
// geo 参数等）在前，块在后；块内先后固定为 dns -> proxy-providers（融合）-> proxy-groups
// -> proxies（切换）-> rule-providers -> rules，未声明的块（profile / sniffer / tun 等）
// 保持原序排在 dns 之前。免得后补的键散落在数百行块的下方。切换模式的 config.yaml 由
// subscription 写入，在 patchSubscriptionFile 里做同一件事。
