package tproxy

import "testing"

// TestParseTproxyException 覆盖例外规则的解析：IPv4/IPv6 单地址与网段、
// 端口、协议:端口，以及 v4-mapped 这类历史上会静默下发出错 CIDR 的输入。
func TestParseTproxyException(t *testing.T) {
	cases := []struct {
		in      string
		typ     string
		wantIP  string // typ=="ip" 时的期望 CIDR 文本
		wantV6  bool
		proto   string
		port    int
		wantErr bool
	}{
		// IPv4
		{in: "192.168.1.0/24", typ: "ip", wantIP: "192.168.1.0/24"},
		{in: "172.17.0.0/16", typ: "ip", wantIP: "172.17.0.0/16"},
		{in: "192.168.1.1", typ: "ip", wantIP: "192.168.1.1/32"},
		// IPv6：单地址必须是 /128，此前一律拼 "/32" 会被放大成整个 /32 网段
		{in: "2001:db8::/32", typ: "ip", wantIP: "2001:db8::/32", wantV6: true},
		{in: "2001:db8::1", typ: "ip", wantIP: "2001:db8::1/128", wantV6: true},
		{in: "fe80::/10", typ: "ip", wantIP: "fe80::/10", wantV6: true},
		{in: "::1", typ: "ip", wantIP: "::1/128", wantV6: true},
		{in: "ff02::1:2/128", typ: "ip", wantIP: "ff02::1:2/128", wantV6: true},
		// v4-mapped：显式拒绝，不再生成家族/掩码不匹配的非法 CIDR
		{in: "::ffff:192.168.1.1/128", wantErr: true},
		{in: "::ffff:0:0/96", wantErr: true},
		// 端口与协议:端口（与家族无关，两个家族都会下发）
		{in: "53", typ: "port", port: 53},
		{in: "tcp:443", typ: "port", proto: "tcp", port: 443},
		{in: "UDP:53", typ: "port", proto: "udp", port: 53},
		// 非法输入
		{in: "", wantErr: true},
		{in: "not-an-ip", wantErr: true},
		{in: "sctp:80", wantErr: true},
		{in: "tcp:70000", wantErr: true},
		{in: "tcp:", wantErr: true},
	}

	for _, c := range cases {
		typ, ipNet, proto, port, err := parseTproxyException(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: 期望报错，实际 typ=%s ipNet=%v proto=%s port=%d", c.in, typ, ipNet, proto, port)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: 非预期错误: %v", c.in, err)
			continue
		}
		if typ != c.typ {
			t.Errorf("%q: typ=%s，期望 %s", c.in, typ, c.typ)
			continue
		}
		if c.typ == "ip" {
			if ipNet == nil || ipNet.String() != c.wantIP {
				t.Errorf("%q: ipNet=%v，期望 %s", c.in, ipNet, c.wantIP)
			}
			if got := isV6Net(ipNet); got != c.wantV6 {
				t.Errorf("%q: isV6Net=%v，期望 %v", c.in, got, c.wantV6)
			}
		} else {
			if proto != c.proto || port != c.port {
				t.Errorf("%q: proto=%s port=%d，期望 %s/%d", c.in, proto, port, c.proto, c.port)
			}
		}
	}
}

// TestMatchPolicyRoutingProbes 覆盖策略路由探测的纯函数部分，
// 保证 v4/v6 两家族共用同一套判定（探测逻辑不因家族而分叉）。
func TestMatchPolicyRoutingProbes(t *testing.T) {
	fwmarkOut := `0:	from all lookup local
32765:	from all fwmark 0x1 lookup 100
32766:	from all lookup main
`
	if !matchFwmarkRule(fwmarkOut) {
		t.Error("应识别出 fwmark 1 → table 100 的策略路由")
	}
	if matchFwmarkRule("0:\tfrom all lookup local\n") {
		t.Error("不应把默认规则误判为策略路由")
	}
	if matchFwmarkRule("32765:\tfrom all fwmark 0x1 lookup 200\n") {
		t.Error("不应把其它 table 的 fwmark 规则误判为目标策略路由")
	}

	if !matchLocalRoute("local ::/0 dev lo table 100\n") {
		t.Error("应识别出 IPv6 的 local 路由")
	}
	if !matchLocalRoute("local 0.0.0.0/0 dev lo table 100\n") {
		t.Error("应识别出 IPv4 的 local 路由")
	}
	if matchLocalRoute("2001:db8::/32 dev eth0 table 100\n") {
		t.Error("不应把非 local 路由误判为本地路由")
	}
}

// TestFamilyDefinitions 守住两套家族的差异点：家族/关键字/集合类型/默认路由
// 必须自洽，且 IPv6 必须绕过组播（否则 DHCPv6 的 ff02::1:2 会被劫持）。
func TestFamilyDefinitions(t *testing.T) {
	if tproxyFamilyV4.isV6 || tproxyFamilyV6.isV6 != true {
		t.Fatal("家族标记错误")
	}
	if tproxyFamilyV4.nftFamily == tproxyFamilyV6.nftFamily {
		t.Error("v4/v6 必须使用不同的 nft 家族")
	}
	if tproxyFamilyV4.addrKw != "ip" || tproxyFamilyV6.addrKw != "ip6" {
		t.Error("地址关键字与家族不匹配")
	}
	if tproxyFamilyV4.setType != "ipv4_addr" || tproxyFamilyV6.setType != "ipv6_addr" {
		t.Error("集合元素类型与家族不匹配")
	}
	if tproxyFamilyV4.defaultRt != "0.0.0.0/0" || tproxyFamilyV6.defaultRt != "::/0" {
		t.Error("策略路由默认路由与家族不匹配")
	}

	hasBypass := func(f tproxyFamily, want string) bool {
		for _, n := range f.bypassNets {
			if n == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"::1/128", "fc00::/7", "fe80::/10", "ff00::/8"} {
		if !hasBypass(tproxyFamilyV6, want) {
			t.Errorf("IPv6 绕过网段缺少 %s", want)
		}
	}
	if !hasBypass(tproxyFamilyV4, "224.0.0.0/4") {
		t.Error("IPv4 绕过网段缺少组播段")
	}
}

// TestStripComment 守住例外行的注释处理（前后端与文档均依赖该行为）。
func TestStripComment(t *testing.T) {
	cases := map[string]string{
		"223.5.5.5 #注释":    "223.5.5.5",
		"  # 整行注释":         "",
		"2001:db8::/32#紧贴": "2001:db8::/32",
		"":                 "",
	}
	for in, want := range cases {
		if got := stripComment(in); got != want {
			t.Errorf("stripComment(%q)=%q，期望 %q", in, got, want)
		}
	}
}
