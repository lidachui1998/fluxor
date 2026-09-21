package nodespec

// V2Ray 系与 SSH 协议：VMess / VLESS / Trojan / AnyTLS / SSH。
//
// 字段对照 mihomo v1.19.31：
//   - adapter/outbound/vmess.go  VmessOption
//   - adapter/outbound/vless.go  VlessOption
//   - adapter/outbound/trojan.go TrojanOption
//   - adapter/outbound/anytls.go AnyTLSOption
//   - adapter/outbound/ssh.go    SshOption
//
// 未下发的字段与原因：
//   - tlsmirror-opts / mekya-opts 等实验性传输（见 blocks.go 顶部说明）；
//   - xhttp-opts 的 padding / session / seq 等调优子项（只保留常用项）；
//   - vless 的 ws-headers：内核里已废弃、代码中无引用；
//   - trojan 的 ss-opts：嵌套的二次加密选项块，与 mieru/sudoku 的高级项同类，未开放。

// vmessCiphers VMess 加密方法（wiki：auto / none / zero / aes-128-gcm / chacha20-poly1305）。
var vmessCiphers = []string{"auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305"}

// packetEncodings UDP 包编码方式（packetaddr：v2ray 5+；xudp：xray）。
var packetEncodings = []string{"packetaddr", "xudp"}

// 各协议支持的网络（对应内核 NewVmess/NewVless/NewTrojan 里的 switch 分支）。
var (
	vmessNetworks  = []string{"tcp", "ws", "h2", "grpc", "http", "mkcp"}
	vlessNetworks  = []string{"tcp", "ws", "h2", "grpc", "http", "xhttp"}
	trojanNetworks = []string{"tcp", "ws", "grpc"}
)

var vmessProtocol = Protocol{
	Type: "vmess",
	Name: "VMess",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(str("uuid", "UUID")),
			req(sel("cipher", "Cipher", "auto", vmessCiphers...)),
			flag("udp", "UDP Relay"),
			flag("tls", "TLS"),
		),
		transportSection(
			networkField(vmessNetworks...),
			wsOptsField(),
			h2OptsField(),
			grpcOptsField(),
			httpOptsField(),
			mkcpOptsField(),
		),
		tlsSection(
			servernameField(),
			alpnField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			clientFingerprintField(),
			certificateField(),
			privateKeyField(),
			echOptsField(),
			realityOptsField(),
			shadowTLSOptsField(),
			restlsOptsField(),
			jlsOptsField(),
		),
		advancedSection(
			// 内核解码器要求 alterId 必须存在，取 0 也要写进配置
			always(num("alterId", "Alter ID")),
			sel("packet-encoding", "Packet Encoding", "", packetEncodings...),
			flag("global-padding", "Global Padding"),
			flag("authenticated-length", "Authenticated Length"),
		),
		basicCommon(),
	),
}

var vlessProtocol = Protocol{
	Type: "vless",
	Name: "VLESS",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(str("uuid", "UUID")),
			flag("tls", "TLS"),
			flag("udp", "UDP Relay"),
		),
		transportSection(
			networkField(vlessNetworks...),
			wsOptsField(),
			h2OptsField(),
			grpcOptsField(),
			httpOptsField(),
			xhttpOptsField(),
		),
		tlsSection(
			servernameField(),
			alpnField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			clientFingerprintField(),
			certificateField(),
			privateKeyField(),
			echOptsField(),
			realityOptsField(),
			shadowTLSOptsField(),
			restlsOptsField(),
			jlsOptsField(),
		),
		advancedSection(
			sel("flow", "Flow", "", "xtls-rprx-vision"),
			strDef("encryption", "Encryption", "none"),
			sel("packet-encoding", "Packet Encoding", "", packetEncodings...),
			flag("packet-addr", "Packet Addr"),
			flag("xudp", "XUDP"),
		),
		basicCommon(),
	),
}

var trojanProtocol = Protocol{
	Type: "trojan",
	Name: "Trojan",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(secret("password", "Password")),
			flag("udp", "UDP Relay"),
		),
		transportSection(
			networkField(trojanNetworks...),
			wsOptsField(),
			grpcOptsField(),
		),
		tlsSection(
			sniField(),
			alpnField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			clientFingerprintField(),
			certificateField(),
			privateKeyField(),
			echOptsField(),
			realityOptsField(),
			shadowTLSOptsField(),
			restlsOptsField(),
			jlsOptsField(),
		),
		basicCommon(),
	),
}

var anytlsProtocol = Protocol{
	Type: "anytls",
	Name: "AnyTLS",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(secret("password", "Password")),
			flag("udp", "UDP Relay"),
		),
		tlsSection(
			sniField(),
			alpnField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
			fingerprintField(),
			clientFingerprintField(),
			certificateField(),
			privateKeyField(),
			echOptsField(),
			shadowTLSOptsField(),
			restlsOptsField(),
			jlsOptsField(),
		),
		advancedSection(
			str("client-metadata", "Client Metadata"),
			num("idle-session-check-interval", "Idle Session Check Interval"),
			num("idle-session-timeout", "Idle Session Timeout"),
			num("min-idle-session", "Min Idle Session"),
			flag("disable-reuse", "Disable Reuse"),
		),
		basicCommon(),
	),
}

var sshProtocol = Protocol{
	Type: "ssh",
	Name: "SSH",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			req(str("username", "Username")),
			secret("password", "Password"),
		),
		advancedSection(
			// 密钥内容或路径；用户名/密码与密钥二选一
			secretText("private-key", "Private Key"),
			secret("private-key-passphrase", "Private Key Passphrase"),
			list("host-key", "Host Key"),
			list("host-key-algorithms", "Host Key Algorithms"),
		),
		basicCommon(),
	),
}
