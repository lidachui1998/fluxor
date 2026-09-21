package nodespec

// 组网与隧道类协议：WireGuard / MASQUE / TrustTunnel / Tailscale / ZeroTier /
// EasyTier / OpenVPN。
//
// 字段对照 mihomo v1.19.31：
//   - adapter/outbound/wireguard.go    WireGuardOption（+ 内嵌 WireGuardPeerOption）
//   - adapter/outbound/masque.go       MasqueOption
//   - adapter/outbound/trusttunnel.go  TrustTunnelOption
//   - adapter/outbound/tailscale.go    TailscaleOption
//   - adapter/outbound/zerotier.go     ZeroTierOption
//   - adapter/outbound/easytier.go     EasyTierOption
//   - adapter/outbound/openvpn.go      OpenVPNOption
//
// 未下发的字段与原因：
//   - 各协议的 ip-stack 块（mode / congestion-controller）：属于各自协议的协议栈选项，
//     不是 TLS/传输层的通用块（需要时可在 blocks.go 加一个 ipStackField 复用）；
//   - wireguard 的 peers 列表与 amnezia-wg-option：多 peer 场景与 Amnezia 扩展，
//     界面只提供「单 peer」的扁平写法（server/port/public-key 等顶层字段）；
//   - zerotier 的 orbit、openvpn 的 peer-info：列表/映射型高级字段；
//   - Tailscale / ZeroTier / EasyTier 没有 server / port：它们靠控制面或网络 ID 组网。

// masqueNetworks MASQUE 传输方式（为空 = 默认 quic）。
var masqueNetworks = []string{"quic", "h2", "h3-l4proxy"}

// openvpnProtos OpenVPN 传输协议。
var openvpnProtos = []string{"udp", "tcp"}

// openvpnAuths OpenVPN 数据验证算法。
var openvpnAuths = []string{"MD5", "SHA1", "SHA256", "SHA384", "SHA512"}

// openvpnCompLzo OpenVPN 压缩方式。
var openvpnCompLzo = []string{"yes", "no", "adaptive"}

var wireguardProtocol = Protocol{
	Type: "wireguard",
	Name: "WireGuard",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			str("ip", "Local IPv4"),
			str("ipv6", "Local IPv6"),
			req(secretText("private-key", "Private Key")),
			req(text("public-key", "Peer Public Key")),
			flag("udp", "UDP Relay"),
		),
		advancedSection(
			secretText("pre-shared-key", "Pre-Shared Key"),
			list("reserved", "Reserved"),
			list("allowed-ips", "Allowed IPs"),
			num("persistent-keepalive", "Persistent Keepalive"),
			num("mtu", "MTU"),
			num("workers", "Workers"),
			flag("remote-dns-resolve", "Remote DNS Resolve"),
			list("dns", "DNS"),
		),
		basicCommon(),
	),
	// 内核要求至少有一个本机地址（IPv4 或 IPv6），否则建不出隧道
	RequireAny: [][]string{{"ip"}, {"ipv6"}},
}

var masqueProtocol = Protocol{
	Type: "masque",
	Name: "MASQUE",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			str("ip", "Local IPv4"),
			str("ipv6", "Local IPv6"),
			req(secretText("private-key", "Private Key")),
			req(text("public-key", "Public Key")),
			flag("udp", "UDP Relay"),
		),
		transportSection(
			sel("network", "Network", "quic", masqueNetworks...),
		),
		tlsSection(
			sniField(),
			skipCertVerifyField(),
			nameCertVerifyField(),
		),
		advancedSection(
			str("uri", "URI"),
			num("mtu", "MTU"),
			num("handshake-timeout", "Handshake Timeout"),
			sel("congestion-controller", "Congestion Controller", "", congestionControllers...),
			num("cwnd", "CWND"),
			sel("bbr-profile", "BBR Profile", "", bbrProfiles...),
			flag("remote-dns-resolve", "Remote DNS Resolve"),
			list("dns", "DNS"),
		),
		basicCommon(),
	),
	// 同 WireGuard：本机地址（IPv4 或 IPv6）至少一个
	RequireAny: [][]string{{"ip"}, {"ipv6"}},
}

var trusttunnelProtocol = Protocol{
	Type: "trusttunnel",
	Name: "TrustTunnel",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			str("username", "Username"),
			secret("password", "Password"),
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
		),
		advancedSection(
			flag("health-check", "Health Check"),
			flag("quic", "QUIC"),
			sel("congestion-controller", "Congestion Controller", "", congestionControllers...),
			num("cwnd", "CWND"),
			sel("bbr-profile", "BBR Profile", "", bbrProfiles...),
			num("max-connections", "Max Connections"),
			num("min-streams", "Min Streams"),
			num("max-streams", "Max Streams"),
		),
		basicCommon(),
	),
}

var tailscaleProtocol = Protocol{
	Type: "tailscale",
	Name: "Tailscale",
	Fields: concat(
		basicSection(
			// 无 server / port：由控制面下发节点信息
			secret("auth-key", "Auth Key"),
			flag("udp", "UDP Relay"),
		),
		advancedSection(
			str("hostname", "Hostname"),
			str("control-url", "Control URL"),
			str("state-dir", "State Dir"),
			flag("ephemeral", "Ephemeral"),
			flag("accept-routes", "Accept Routes"),
			str("exit-node", "Exit Node"),
			flag("exit-node-allow-lan-access", "Exit Node Allow LAN Access"),
		),
		basicCommon(),
	),
}

var zerotierProtocol = Protocol{
	Type: "zerotier",
	Name: "ZeroTier",
	Fields: concat(
		basicSection(
			// network 是 ZeroTier 网络 ID（16 位十六进制），没有 server / port
			req(str("network", "Network ID")),
			flag("udp", "UDP Relay"),
		),
		advancedSection(
			str("state-dir", "State Dir"),
			secret("identity-secret", "Identity Secret"),
			str("planet", "Planet"),
			num("mtu", "MTU"),
			num("physical-mtu", "Physical MTU"),
			num("primary-port", "Primary Port"),
			num("secondary-port", "Secondary Port"),
			str("tcp-fallback-mode", "TCP Fallback Mode"),
			str("tcp-fallback-relay", "TCP Fallback Relay"),
			flag("low-bandwidth", "Low Bandwidth"),
			flag("encrypted-hello", "Encrypted Hello"),
			flag("remote-dns-resolve", "Remote DNS Resolve"),
			list("dns", "DNS"),
			str("remote-trace-target", "Remote Trace Target"),
			num("remote-trace-level", "Remote Trace Level"),
		),
		basicCommon(),
	),
}

var easytierProtocol = Protocol{
	Type: "easytier",
	Name: "EasyTier",
	Fields: concat(
		basicSection(
			req(str("network-name", "Network Name")),
			secret("network-secret", "Network Secret"),
			list("peers", "Peers"),
			list("listeners", "Listeners"),
			str("hostname", "Hostname"),
			str("ipv4", "Overlay IPv4"),
			flag("dhcp", "DHCP"),
			str("instance-name", "Instance Name"),
			flag("udp", "UDP Relay"),
		),
		advancedSection(
			list("mapped-listeners", "Mapped Listeners"),
			list("exit-nodes", "Exit Nodes"),
			list("proxy-networks", "Proxy Networks"),
			flag("no-listener", "Disable Listener"),
			str("state-dir", "State Dir"),
			flag("accept-dns", "Accept DNS"),
			flag("enable-exit-node", "Enable Exit Node"),
			flag("enable-encryption", "Enable Encryption"),
			strDef("encryption-algorithm", "Encryption Algorithm", "aes-gcm"),
			flag("private-mode", "Private Mode"),
			flag("latency-first", "Latency First"),
			flag("disable-p2p", "Disable P2P"),
			flag("enable-kcp-proxy", "Enable KCP Proxy"),
			flag("disable-kcp-input", "Disable KCP Input"),
			flag("enable-quic-proxy", "Enable QUIC Proxy"),
			flag("disable-quic-input", "Disable QUIC Input"),
			num("mtu", "MTU"),
			str("tld-dns-zone", "TLD DNS Zone"),
			flag("secure-mode", "Secure Mode"),
			secretText("local-private-key", "Local Private Key"),
			text("local-public-key", "Local Public Key"),
		),
		basicCommon(),
	),
	// 内核要求：listeners 为空时至少要有一个 peers（不隐式连接公共节点）
	RequireAny: [][]string{{"peers"}, {"listeners"}},
}

var openvpnProtocol = Protocol{
	Type: "openvpn",
	Name: "OpenVPN",
	Fields: concat(
		basicSection(
			req(str("server", "Server")),
			req(num("port", "Port")),
			sel("proto", "Proto", "udp", openvpnProtos...),
			req(text("ca", "CA Certificate")),
			str("username", "Username"),
			secret("password", "Password"),
			flag("udp", "UDP Relay"),
		),
		tlsSection(
			text("cert", "Client Certificate"),
			secretText("key", "Client Key"),
			text("tls-auth", "TLS Auth Key"),
			sel("key-direction", "Key Direction", "", "0", "1"),
			text("tls-crypt", "TLS Crypt Key"),
			secretText("tls-crypt-v2", "TLS Crypt v2 Key"),
		),
		advancedSection(
			// 虚拟网卡类型当前仅支持 tun
			sel("dev", "Device", "tun", "tun"),
			str("cipher", "Cipher"),
			list("data-ciphers", "Data Ciphers"),
			str("data-ciphers-fallback", "Data Ciphers Fallback"),
			sel("auth", "Auth", "SHA256", openvpnAuths...),
			sel("comp-lzo", "Comp LZO", "", openvpnCompLzo...),
			num("ping", "Ping"),
			num("ping-restart", "Ping Restart"),
			num("tran-window", "Tran Window"),
			num("handshake-timeout", "Handshake Timeout"),
			num("mtu", "MTU"),
			flag("remote-dns-resolve", "Remote DNS Resolve"),
			list("dns", "DNS"),
		),
		basicCommon(),
	),
	// 内核要求二选一：证书认证（cert 与 key 必须成对）或用户名认证（auth-user-pass）
	RequireAny: [][]string{{"cert", "key"}, {"username"}},
}
