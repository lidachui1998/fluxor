package nodespec

// 本文件收录可复用的「配置块」：传输层选项块、TLS 相关标量与嵌套块。
//
// 它们按 wiki 的「传输层配置」与「TLS配置」两页 + 内核 adapter/outbound 的实际
// struct 定义整理，被多个协议共享，因此单独抽出，避免同一份字段在协议表里抄多遍
// 而互相漂移。块名与内核配置键一一对应（ws-opts / reality-opts / ech-opts …）。
//
// 刻意不下发的字段：
//   - tlsmirror-opts（三层嵌套 + 步骤数组，表单无法可靠表达）、mekya-opts（实验性传输）；
//   - xhttp-opts 的 padding / session / seq / reuse-settings 等几十个调优项，
//     只保留 path / host / mode / headers / no-grpc-header 这几个常用项；
//   - vless 的 ws-headers（内核 v1.19.31 里该字段已废弃、代码中无任何引用）；
//   - http-opts.headers（类型是 map[string][]string，界面只能表达单值，避免写错类型）。

// ---------------------------------------------------------------- 传输层选项块

// networkField 传输方式选择器。空值 = 内核默认 tcp；transport 选项块按此值条件显示。
func networkField(options ...string) Field {
	return sel("network", "Network", "", options...)
}

// wsOptsField WebSocket 传输选项（network=ws 时生效）。
func wsOptsField() Field {
	return group("ws-opts", "WebSocket", on("network", "ws"),
		strDef("path", "Path", "/"),
		pairs("headers", "Headers"),
		num("max-early-data", "Max Early Data"),
		str("early-data-header-name", "Early Data Header Name"),
		flag("v2ray-http-upgrade", "V2Ray HTTP Upgrade"),
		flag("v2ray-http-upgrade-fast-open", "V2Ray HTTP Upgrade Fast Open"),
	)
}

// h2OptsField HTTP/2 传输选项（network=h2 时生效）。
func h2OptsField() Field {
	return group("h2-opts", "HTTP/2", on("network", "h2"),
		list("host", "Host"),
		strDef("path", "Path", "/"),
	)
}

// grpcOptsField gRPC 传输选项（network=grpc 时生效）。
func grpcOptsField() Field {
	return group("grpc-opts", "gRPC", on("network", "grpc"),
		str("grpc-service-name", "Service Name"),
		str("grpc-user-agent", "User Agent"),
		num("ping-interval", "Ping Interval"),
		num("max-connections", "Max Connections"),
		num("min-streams", "Min Streams"),
		num("max-streams", "Max Streams"),
	)
}

// httpOptsField HTTP 传输选项（network=http 时生效）。
func httpOptsField() Field {
	return group("http-opts", "HTTP", on("network", "http"),
		sel("method", "Method", "", "GET", "POST", "PUT", "HEAD", "DELETE", "OPTIONS", "PATCH"),
		list("path", "Path"),
	)
}

// xhttpOptsField XHTTP 传输选项（network=xhttp 时生效，仅 VLESS）。
func xhttpOptsField() Field {
	return group("xhttp-opts", "XHTTP", on("network", "xhttp"),
		strDef("path", "Path", "/"),
		str("host", "Host"),
		str("mode", "Mode"),
		pairs("headers", "Headers"),
		flag("no-grpc-header", "No gRPC Header"),
	)
}

// mkcpOptsField mKCP 传输选项（network=mkcp 时生效，仅 VMess）。
func mkcpOptsField() Field {
	return group("mkcp-opts", "mKCP", on("network", "mkcp"),
		num("mtu", "MTU"),
		num("tti", "TTI"),
		num("uplink-capacity", "Uplink Capacity"),
		num("downlink-capacity", "Downlink Capacity"),
		flag("congestion", "Congestion"),
		num("write-buffer", "Write Buffer"),
		num("read-buffer", "Read Buffer"),
		str("seed", "Seed"),
		str("header", "Header"),
	)
}

// ---------------------------------------------------------------- TLS 标量字段

// sniField SNI/服务器名称（各协议命名不同：多数协议用 sni，VMess/VLESS 用 servername）。
func sniField() Field { return str("sni", "SNI") }

// servernameField VMess / VLESS 的服务器名称（等价于其他协议的 sni）。
func servernameField() Field { return str("servername", "Server Name") }

// alpnField ALPN 列表（内核默认值由各协议自身决定，此处不下发）。
func alpnField() Field { return list("alpn", "ALPN") }

// skipCertVerifyField 跳过证书校验。
func skipCertVerifyField() Field { return flag("skip-cert-verify", "Skip Cert Verify") }

// nameCertVerifyField 按名称校验证书（与指纹配套）。
func nameCertVerifyField() Field { return str("name-cert-verify", "Name Cert Verify") }

// fingerprintField 证书指纹（配合 name-cert-verify 实现证书固定）。
func fingerprintField() Field { return str("fingerprint", "Certificate Fingerprint") }

// clientFingerprintField 客户端 TLS 指纹（uTLS 模拟的浏览器指纹）。
func clientFingerprintField() Field {
	return sel("client-fingerprint", "Client Fingerprint", "", tlsFingerprints...)
}

// certificateField 内联客户端证书（PEM）。
func certificateField() Field { return text("certificate", "Certificate") }

// privateKeyField 内联客户端私钥（PEM）。
func privateKeyField() Field { return secretText("private-key", "Private Key") }

// ---------------------------------------------------------------- TLS 嵌套块

// realityOptsField REALITY 选项（VLESS / VMess / Trojan）。
func realityOptsField() Field {
	return group("reality-opts", "REALITY", nil,
		req(str("public-key", "Public Key")),
		str("short-id", "Short ID"),
		flag("support-x25519mlkem768", "X25519MLKEM768"),
	)
}

// echOptsField Encrypted Client Hello 选项。
func echOptsField() Field {
	return group("ech-opts", "ECH", nil,
		flag("enable", "Enable"),
		str("config", "Config"),
		str("query-server-name", "Query Server Name"),
	)
}

// shadowTLSOptsField ShadowTLS 选项（配合 shadow-tls 插件/混淆使用）。
func shadowTLSOptsField() Field {
	return group("shadow-tls-opts", "ShadowTLS", nil,
		secret("password", "Password"),
		num("version", "Version"),
	)
}

// restlsOptsField Restls 选项。
func restlsOptsField() Field {
	return group("restls-opts", "Restls", nil,
		secret("password", "Password"),
		str("version-hint", "Version Hint"),
		str("restls-script", "Restls Script"),
	)
}

// jlsOptsField JLS 选项。
func jlsOptsField() Field {
	return group("jls-opts", "JLS", nil,
		req(str("username", "Username")),
		req(secret("password", "Password")),
	)
}
