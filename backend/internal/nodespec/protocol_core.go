package nodespec

// 基础代理协议：HTTP / SOCKS5 / Shadowsocks / ShadowsocksR / Snell。
//
// 字段对照 mihomo v1.19.31：
//   - adapter/outbound/http.go        HttpOption
//   - adapter/outbound/socks5.go      Socks5Option
//   - adapter/outbound/shadowsocks.go ShadowSocksOption
//   - adapter/outbound/shadowsocksr.go ShadowSocksROption
//   - adapter/outbound/snell.go       SnellOption
//
// 未下发的字段与原因：
//   - http-opts 的 headers 形态映射（见 blocks.go 顶部说明）；
//   - ss 的 plugin-opts 只按「键: 值」单值表达（内核的 obfs 解码器同样只吃标量）。

var httpProtocol = Protocol{
	Type: "http",
	Name: "HTTP",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			str("username", "Username"),
			secret("password", "Password"),
			flag("tls", "TLS"),
		),
		transportSection(
			// HttpOption.headers：代理请求附加头
			pairs("headers", "Headers"),
		),
		tlsSection(
			sniField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			certificateField(),
			privateKeyField(),
		),
		basicCommon(),
	),
}

var socks5Protocol = Protocol{
	Type: "socks5",
	Name: "SOCKS5",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			str("username", "Username"),
			secret("password", "Password"),
			flag("tls", "TLS"),
		),
		// Socks5Option 没有 sni 字段（TLS 只用于给 socks5 通道加密）
		tlsSection(
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			certificateField(),
			privateKeyField(),
		),
		basicCommon(),
	),
}

var ssProtocol = Protocol{
	Type: "ss",
	Name: "Shadowsocks",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(sel("cipher", "Cipher", "aes-256-gcm", ssCiphers...)),
			req(secret("password", "Password")),
			flag("udp", "UDP Relay"),
		),
		transportSection(
			sel("plugin", "Plugin", "", ssPlugins...),
			pairs("plugin-opts", "Plugin Options"),
		),
		tlsSection(
			// 插件的 TLS 握手（shadow-tls / restls / jls）使用该指纹
			clientFingerprintField(),
		),
		advancedSection(
			flag("udp-over-tcp", "UDP over TCP"),
			num("udp-over-tcp-version", "UDP over TCP Version"),
		),
		basicCommon(),
	),
}

// ssrProtocol ShadowsocksR：上游自 2021 年起已停止维护，内核仅做兼容保留，
// 因此标记为过时并在界面提示（仍然可以正常保存与生成配置）。
var ssrProtocol = Protocol{
	Type:       "ssr",
	Name:       "ShadowsocksR",
	Deprecated: true,
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(sel("cipher", "Cipher", "chacha20-ietf", ssCiphers...)),
			req(secret("password", "Password")),
			req(sel("obfs", "Obfs", "plain", ssrObfs...)),
			req(sel("protocol", "Protocol", "origin", ssrProtocols...)),
		),
		advancedSection(
			str("obfs-param", "Obfs Param"),
			str("protocol-param", "Protocol Param"),
			flag("udp", "UDP Relay"),
		),
		basicCommon(),
	),
}

var snellProtocol = Protocol{
	Type: "snell",
	Name: "Snell",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(secret("psk", "PSK")),
			numDef("version", "Version", 4),
			flag("udp", "UDP Relay"),
		),
		transportSection(
			// 混淆模式决定下面用到的子项：tls/http 用 host；shadow-tls 用 password/version/alpn；
			// jls 用 username/password；restls 用 password/version-hint/restls-script
			group("obfs-opts", "Obfs Options", nil,
				sel("mode", "Mode", "", snellObfsModes...),
				str("host", "Host"),
				secret("password", "Password"),
				num("version", "Version"),
				list("alpn", "ALPN"),
				str("username", "Username"),
				str("version-hint", "Version Hint"),
				str("restls-script", "Restls Script"),
			),
		),
		tlsSection(
			clientFingerprintField(),
		),
		advancedSection(
			// reuse 仅在 v4/v5 生效（内核内部对 v5 回落为 v4 客户端）
			flag("reuse", "Reuse"),
		),
		basicCommon(),
	),
}
