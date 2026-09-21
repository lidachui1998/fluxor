// Package nodespec 描述自定义模式（手工添加节点）支持的出站代理协议字段表。
//
// 职责边界：
//   - 提供「协议 → 字段（类型/默认值/是否必填/分区/条件显示）」的声明式描述：
//     前端据此渲染动态表单，configgen 据此组装 config.yaml 的 proxies 块；
//   - 提供取值归一化：类型转换、必填与组合校验、未知键拒绝、剔除与默认值相同的字段，
//     使持久化只保存「用户真正改过」的那部分（见 config.CustomNode）。
//
// 字段来源（对照 mihomo v1.19.31 的 adapter/outbound/*.go struct tag 与官方 wiki）：
//   - 「通用字段」与 HTTP → OpenVPN 全部 23 个协议的协议级字段；
//   - 「TLS配置」页：tls/sni/servername/alpn/skip-cert-verify/name-cert-verify/fingerprint/
//     client-fingerprint/certificate/private-key，以及 reality-opts、ech-opts、
//     shadow-tls-opts、restls-opts、jls-opts 五个嵌套块；
//   - 「传输层配置」页：network 选择器与 ws-opts、h2-opts、grpc-opts、http-opts、
//     xhttp-opts、mkcp-opts 选项块，外加 ss 的 plugin/plugin-opts 与 snell 的 obfs-opts。
//
// 刻意不下发的部分（需要时按 blocks.go 的写法补一个构造器即可）：
//   - dialer-proxy、smux、ip-stack：不属于上述两页，且都是独立嵌套块；
//   - tlsmirror-opts（三层嵌套 + 步骤数组，表单无法可靠表达）、mekya-opts（实验性传输）；
//   - xhttp-opts 的 padding / session / seq / reuse-settings 等几十个调优子项；
//   - vless 的 ws-headers（内核里已废弃、代码中无引用）、trojan 的 ss-opts、
//     hysteria2 的 realm-opts、wireguard 的 peers/amnezia-wg-option、zerotier 的 orbit、
//     openvpn 的 peer-info、sudoku 的 httpmask.* 等协议私有嵌套块。
//
// 新增协议或字段只改本包（后端单点）：前端表单、取值校验、config.yaml 生成与默认值
// 补齐全部由这份声明驱动，无需在其它层再维护一份清单。
package nodespec
