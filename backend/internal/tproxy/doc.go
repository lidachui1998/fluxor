package tproxy

// Package tproxy 管理 TProxy 透明代理的防火墙（nftables）与策略路由规则。
//
// 安全约束（务必遵守）：
//   - 严禁拼接 shell 字符串并用 sh -c 执行；必须使用 exec.Command 原生参数切片，
//     以杜绝用户表单（例外 IP/端口）带来的命令注入。
//   - 面板退出时必须调用 DisableTProxyRules 清退全部规则，避免断网残留。
//   - 冷启动必须调用 ResetOnStartup：nft 规则不跨重启存活而开关状态是持久化的，
//     归零开关并清残留，避免「面板显示关闭、流量仍被劫持」。
//   - 清理时各项资源（nft 表 / fwmark 规则 / local 路由）各自探测、存在才删，
//     不得把策略路由的清理绑在「nft 表存在」的判定之后（详见 rules.go）。
//   - IPv4 / IPv6 是两套同构规则（见 rules.go 的 tproxyFamily）：启用时按开关
//     裁剪，清理时必须两个家族都探测——否则关掉 IPv6 开关就再也清不掉遗留规则。
//     例外网段按家族分流（v6 例外进 ip6 表），端口例外两个家族都下发。
//
// 文件划分：
//   - state.go   启用状态、IPv6 接管开关、例外缓存与各读写锁
//   - store.go   开关状态 / 例外列表 / 本机代理开关 / IPv6 接管开关在 fluxor.json
//                中的持久化，以及冷启动收敛 ResetOnStartup
//   - rules.go   规则解析与 nftables 规则的启用、探测式清理（IPv4/IPv6 同构）
//   - handlers.go /config/tproxy* HTTP 接口
