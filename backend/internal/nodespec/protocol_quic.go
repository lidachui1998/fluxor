package nodespec

// QUIC / UDP 系协议：Mieru / Sudoku / Hysteria / Hysteria2 / TUIC / ShadowQUIC。
//
// 字段对照 mihomo v1.19.31：
//   - adapter/outbound/mieru.go      MieruOption
//   - adapter/outbound/sudoku.go     SudokuOption
//   - adapter/outbound/hysteria.go   HysteriaOption
//   - adapter/outbound/hysteria2.go  Hysteria2Option
//   - adapter/outbound/tuic.go       TuicOption
//   - adapter/outbound/shadowquic.go ShadowQuicOption
//
// 未下发的字段与原因：
//   - sudoku 的 httpmask.* 与 http-mask-* 系列：HTTP 伪装（传输层）配置，子项众多；
//   - hysteria2 的 realm-opts：Relay/Realm 中转配置，非 TLS/传输层的通用项；
//   - 各协议的 cwnd / bbr-profile / recv-window 等调优项保留为高级项，默认不下发。
//
// 这些协议本身没有 udp 开关字段（除 mieru 外）：UDP 是它们的天然传输。

// hysteriaProtocols Hysteria 底层传输（wiki：udp / wechat-video / faketcp）。
var hysteriaProtocols = []string{"udp", "wechat-video", "faketcp"}

// hysteria2Obfs Hysteria2 QUIC 混淆器（为空表示不启用）。
var hysteria2Obfs = []string{"salamander", "gecko"}

// mieruMultiplexing Mieru 多路复用档位（默认 MULTIPLEXING_LOW）。
var mieruMultiplexing = []string{"MULTIPLEXING_OFF", "MULTIPLEXING_LOW", "MULTIPLEXING_MIDDLE", "MULTIPLEXING_HIGH"}

// mieruHandshakeModes Mieru 握手模式（默认 HANDSHAKE_STANDARD）。
var mieruHandshakeModes = []string{"HANDSHAKE_STANDARD", "HANDSHAKE_NO_WAIT"}

// sudokuAEADMethods Sudoku AEAD 算法（none 不提供 AEAD 保护，不建议）。
var sudokuAEADMethods = []string{"chacha20-poly1305", "aes-128-gcm", "none"}

// sudokuTableTypes Sudoku 字节布局类型。
var sudokuTableTypes = []string{"prefer_ascii", "prefer_entropy", "up_ascii_down_entropy", "up_entropy_down_ascii"}

// sudokuMultiplex Sudoku 多路复用模式。
var sudokuMultiplex = []string{"off", "auto", "on"}

// udpRelayModes TUIC UDP 中继模式。
var udpRelayModes = []string{"native", "quic"}

var mieruProtocol = Protocol{
	Type: "mieru",
	Name: "Mieru",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			num("port", "Port"),
			str("port-range", "Port Range"),
			req(sel("transport", "Transport", "TCP", "TCP", "UDP")),
			req(str("username", "Username")),
			req(secret("password", "Password")),
			sel("multiplexing", "Multiplexing", "MULTIPLEXING_LOW", mieruMultiplexing...),
			sel("handshake-mode", "Handshake Mode", "HANDSHAKE_STANDARD", mieruHandshakeModes...),
		),
		advancedSection(
			str("traffic-pattern", "Traffic Pattern"),
			flag("udp", "UDP Relay"),
		),
		basicCommon(),
	),
	// port 与 port-range（端口范围段）二选一
	RequireAny: [][]string{{"port"}, {"port-range"}},
}

var sudokuProtocol = Protocol{
	Type: "sudoku",
	Name: "Sudoku",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(secret("key", "Key")),
			sel("aead-method", "AEAD Method", "chacha20-poly1305", sudokuAEADMethods...),
			sel("table-type", "Table Type", "prefer_ascii", sudokuTableTypes...),
			sel("multiplex", "Multiplex", "off", sudokuMultiplex...),
			flag("enable-pure-downlink", "Enable Pure Downlink"),
		),
		advancedSection(
			num("padding-min", "Padding Min"),
			num("padding-max", "Padding Max"),
			str("custom-table", "Custom Table"),
			list("custom-tables", "Custom Tables"),
		),
		basicCommon(),
	),
}

var hysteriaProtocol = Protocol{
	Type: "hysteria",
	Name: "Hysteria",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			num("port", "Port"),
			str("ports", "Ports"),
			req(str("up", "Up Bandwidth")),
			req(str("down", "Down Bandwidth")),
			secret("auth", "Auth"),
			sel("protocol", "Protocol", "udp", hysteriaProtocols...),
		),
		tlsSection(
			sniField(),
			alpnField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			certificateField(),
			privateKeyField(),
			echOptsField(),
		),
		advancedSection(
			str("obfs", "Obfs Password"),
			num("up-speed", "Up Speed"),
			num("down-speed", "Down Speed"),
			flag("fast-open", "Fast Open"),
			flag("disable-mtu-discovery", "Disable MTU Discovery"),
			num("hop-interval", "Hop Interval"),
			num("recv-window", "Receive Window"),
			num("recv-window-conn", "Receive Window Conn"),
		),
		basicCommon(),
	),
	// port 与 ports（端口范围段，如 443-8443）二选一
	RequireAny: [][]string{{"port"}, {"ports"}},
}

var hysteria2Protocol = Protocol{
	Type: "hysteria2",
	Name: "Hysteria2",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			num("port", "Port"),
			str("ports", "Ports"),
			secret("password", "Password"),
			str("up", "Up Bandwidth"),
			str("down", "Down Bandwidth"),
			sel("obfs", "Obfs", "", hysteria2Obfs...),
		),
		tlsSection(
			sniField(),
			alpnField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			certificateField(),
			privateKeyField(),
			echOptsField(),
		),
		advancedSection(
			secret("obfs-password", "Obfs Password"),
			num("obfs-min-packet-size", "Obfs Min Packet Size"),
			num("obfs-max-packet-size", "Obfs Max Packet Size"),
			str("hop-interval", "Hop Interval"),
			num("cwnd", "CWND"),
			sel("bbr-profile", "BBR Profile", "", bbrProfiles...),
			num("udp-mtu", "UDP MTU"),
			num("handshake-timeout", "Handshake Timeout"),
		),
		basicCommon(),
	),
	// port 与 ports（端口跳跃范围，如 443-8443）二选一
	RequireAny: [][]string{{"port"}, {"ports"}},
}

var tuicProtocol = Protocol{
	Type: "tuic",
	Name: "TUIC",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			secret("token", "Token"),
			str("uuid", "UUID"),
			secret("password", "Password"),
			sel("udp-relay-mode", "UDP Relay Mode", "native", udpRelayModes...),
		),
		tlsSection(
			sniField(),
			alpnField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			certificateField(),
			privateKeyField(),
			echOptsField(),
		),
		advancedSection(
			str("ip", "IP Override"),
			num("heartbeat-interval", "Heartbeat Interval"),
			flag("disable-sni", "Disable SNI"),
			flag("reduce-rtt", "Reduce RTT"),
			num("request-timeout", "Request Timeout"),
			sel("congestion-controller", "Congestion Controller", "", congestionControllers...),
			num("max-udp-relay-packet-size", "Max UDP Relay Packet Size"),
			flag("fast-open", "Fast Open"),
			num("max-open-streams", "Max Open Streams"),
			flag("udp-over-stream", "UDP over Stream"),
			num("udp-over-stream-version", "UDP over Stream Version"),
		),
		basicCommon(),
	),
}

var shadowquicProtocol = Protocol{
	Type: "shadowquic",
	Name: "ShadowQUIC",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			str("username", "Username"),
			secret("password", "Password"),
		),
		tlsSection(
			sniField(),
			alpnField(),
		),
		advancedSection(
			list("quic-versions", "QUIC Versions"),
			flag("udp-over-stream", "UDP over Stream"),
			flag("zero-rtt", "Zero RTT"),
			num("keep-alive-interval", "Keep Alive Interval"),
			sel("congestion-controller", "Congestion Controller", "", congestionControllers...),
			str("up", "Up Bandwidth"),
			str("down", "Down Bandwidth"),
			num("cwnd", "CWND"),
			sel("bbr-profile", "BBR Profile", "", bbrProfiles...),
			num("max-datagram-frame-size", "Max Datagram Frame Size"),
			num("max-open-streams", "Max Open Streams"),
			num("recv-window", "Receive Window"),
			num("recv-window-conn", "Receive Window Conn"),
			flag("disable-mtu-discovery", "Disable MTU Discovery"),
		),
		basicCommon(),
	),
}
