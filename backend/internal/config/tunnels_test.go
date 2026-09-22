package config

import "testing"

// tunnelAt 生成一条用于排序/判重测试的隧道（只需 id/address/network 参与逻辑）。
func tunnelAt(id, address string, networks ...string) Tunnel {
	return Tunnel{ID: id, Address: address, Network: networks, Target: "1.1.1.1:53"}
}

// TestNormalizeTunnelNetworks 网络类型：只认 tcp/udp，去重且顺序固定。
//
// 顺序固定是为了产物可逐字节比对；空值与非法值必须分别处理（前者忽略、后者拒绝），
// 否则「多填了一个空项」会被当成配置错误。
func TestNormalizeTunnelNetworks(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		want  string
		isErr bool
	}{
		{name: "两者", input: []string{"udp", "tcp"}, want: "tcp,udp"},
		{name: "仅 tcp", input: []string{"tcp"}, want: "tcp"},
		{name: "仅 udp", input: []string{"UDP"}, want: "udp"},
		{name: "重复去重", input: []string{"tcp", "tcp", "udp"}, want: "tcp,udp"},
		{name: "空项忽略", input: []string{"tcp", "  "}, want: "tcp"},
		{name: "非法值", input: []string{"tcp", "icmp"}, isErr: true},
		{name: "全空", input: []string{"", " "}, isErr: true},
		{name: "nil", input: nil, isErr: true},
	}
	for _, tc := range cases {
		got, err := NormalizeTunnelNetworks(tc.input)
		if tc.isErr {
			if err == nil {
				t.Fatalf("%s：应报错，实际得到 %v", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s：不应报错: %v", tc.name, err)
		}
		if joined := joinNetworks(got); joined != tc.want {
			t.Fatalf("%s：实际 %q，期望 %q", tc.name, joined, tc.want)
		}
	}
}

func joinNetworks(networks []string) string {
	out := ""
	for i, n := range networks {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}

// TestNormalizeTunnelAddress 监听地址：必须 host:port 且端口合法。
func TestNormalizeTunnelAddress(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
		isErr bool
	}{
		{name: "IPv4", input: "127.0.0.1:8888", want: "127.0.0.1:8888"},
		{name: "通配地址", input: "0.0.0.0:8888", want: "0.0.0.0:8888"},
		{name: "首尾空白", input: " 127.0.0.1:8888 ", want: "127.0.0.1:8888"},
		{name: "IPv6 加方括号", input: "[::1]:8888", want: "[::1]:8888"},
		{name: "缺端口", input: "127.0.0.1", isErr: true},
		{name: "端口为 0", input: "127.0.0.1:0", isErr: true},
		{name: "端口越界", input: "127.0.0.1:65536", isErr: true},
		{name: "端口非数字", input: "127.0.0.1:abc", isErr: true},
		{name: "空值", input: "  ", isErr: true},
	}
	for _, tc := range cases {
		got, err := NormalizeTunnelAddress(tc.input)
		if tc.isErr {
			if err == nil {
				t.Fatalf("%s：应报错，实际得到 %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s：不应报错: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s：实际 %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// TestNormalizeTunnelTarget 目标归一化：域名可省端口（补 :80），IP 必须写明端口。
//
// 两端都必须落到 host:port —— 内核用 socks5.ParseAddr 解析目标，裸主机不会被拒绝加载，
// 而是起监听时打一行日志就跳过（隧道静默失效）。域名有约定俗成的 http 端口，IP 没有，
// 因此裸 IP 一律要求用户补端口。
func TestNormalizeTunnelTarget(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   string
		isErr  bool
	}{
		{name: "IP 加端口", target: "8.8.8.8:53", want: "8.8.8.8:53"},
		{name: "域名加端口", target: "example.com:443", want: "example.com:443"},
		{name: "IPv6 加方括号与端口", target: "[2001:db8::1]:53", want: "[2001:db8::1]:53"},
		{name: "首尾空白", target: " example.com:443 ", want: "example.com:443"},
		// 纯域名补默认端口 80
		{name: "裸域名补 80", target: "example.com", want: "example.com:80"},
		{name: "裸域名带空白", target: " dns.google ", want: "dns.google:80"},
		{name: "裸域名带子域", target: "a.b.example.co.uk", want: "a.b.example.co.uk:80"},
		// 裸 IP 不猜端口：报错让用户写明
		{name: "裸 IPv4", target: "8.8.8.8", isErr: true},
		{name: "裸 IPv6", target: "::1", isErr: true},
		{name: "多段冒号", target: "a:b", isErr: true},
		{name: "端口为 0", target: "8.8.8.8:0", isErr: true},
		{name: "端口越界", target: "8.8.8.8:65536", isErr: true},
		{name: "端口非数字", target: "8.8.8.8:abc", isErr: true},
		{name: "空值", target: " ", isErr: true},
		{name: "含逗号", target: "a:1,example.com:2", isErr: true},
	}
	for _, tc := range cases {
		got, err := NormalizeTunnelTarget(tc.target)
		if tc.isErr {
			if err == nil {
				t.Fatalf("%s：应报错，实际得到 %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s：不应报错: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s：实际 %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// TestMoveTunnel 隧道整份列表即一组：只在相邻项之间交换，边界返回 moved=false。
func TestMoveTunnel(t *testing.T) {
	tunnels := []Tunnel{tunnelAt("a", "127.0.0.1:1", "tcp"), tunnelAt("b", "127.0.0.1:2", "tcp"), tunnelAt("c", "127.0.0.1:3", "tcp")}

	up, moved := MoveTunnel(tunnels, "b", RuleMoveUp)
	if !moved || up[0].ID != "b" || up[1].ID != "a" {
		t.Fatalf("上移失败: %v", tunnelIDs(up))
	}
	// 原切片不得被就地改动：调用方可能仍持有请求级快照
	if tunnels[0].ID != "a" {
		t.Fatalf("原切片被就地改动: %v", tunnelIDs(tunnels))
	}

	down, moved := MoveTunnel(tunnels, "b", RuleMoveDown)
	if !moved || down[2].ID != "b" {
		t.Fatalf("下移失败: %v", tunnelIDs(down))
	}

	if _, moved := MoveTunnel(tunnels, "a", RuleMoveUp); moved {
		t.Fatal("首条不应能继续上移")
	}
	if _, moved := MoveTunnel(tunnels, "c", RuleMoveDown); moved {
		t.Fatal("末条不应能继续下移")
	}
	if _, moved := MoveTunnel(tunnels, "missing", RuleMoveUp); moved {
		t.Fatal("不存在的 id 不应报告移动成功")
	}
	if _, moved := MoveTunnel(tunnels, "b", "sideways"); moved {
		t.Fatal("未知方向不应报告移动成功")
	}
}

func tunnelIDs(tunnels []Tunnel) []string {
	out := make([]string, 0, len(tunnels))
	for _, tunnel := range tunnels {
		out = append(out, tunnel.ID)
	}
	return out
}

// TestTunnelBindingsOverlap 只有「同地址 + 网络类型有交集」才算冲突。
func TestTunnelBindingsOverlap(t *testing.T) {
	cases := []struct {
		name string
		a, b Tunnel
		want bool
	}{
		{
			name: "同地址同网络",
			a:    tunnelAt("a", "127.0.0.1:6553", "tcp"),
			b:    tunnelAt("b", "127.0.0.1:6553", "tcp"),
			want: true,
		},
		{
			name: "同地址网络有交集",
			a:    tunnelAt("a", "127.0.0.1:6553", "tcp", "udp"),
			b:    tunnelAt("b", "127.0.0.1:6553", "udp"),
			want: true,
		},
		{
			name: "同地址网络不相交",
			a:    tunnelAt("a", "127.0.0.1:6553", "tcp"),
			b:    tunnelAt("b", "127.0.0.1:6553", "udp"),
			want: false,
		},
		{
			name: "地址不同",
			a:    tunnelAt("a", "127.0.0.1:6553", "tcp"),
			b:    tunnelAt("b", "127.0.0.1:6554", "tcp"),
			want: false,
		},
		{
			name: "首尾空白不影响比较",
			a:    tunnelAt("a", "127.0.0.1:6553", "tcp"),
			b:    Tunnel{ID: "b", Address: " 127.0.0.1:6553 ", Network: []string{" tcp "}},
			want: true,
		},
	}
	for _, tc := range cases {
		if got := TunnelBindingsOverlap(tc.a, tc.b); got != tc.want {
			t.Fatalf("%s：实际 %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// TestTunnelIsEnabledDefault 未显式设置开关（历史数据）视为启用。
//
// 隧道是用户主动添加的，默认就该生效；只有显式关掉才不写进 config.yaml。
func TestTunnelIsEnabledDefault(t *testing.T) {
	enabled, disabled := true, false
	if !(Tunnel{}.IsEnabled()) {
		t.Fatal("nil 开关应视为启用")
	}
	if !(Tunnel{Enabled: &enabled}.IsEnabled()) {
		t.Fatal("显式 true 应视为启用")
	}
	if (Tunnel{Enabled: &disabled}).IsEnabled() {
		t.Fatal("显式 false 应视为关闭")
	}
	if (Tunnel{Enabled: &disabled}).EnabledValue() {
		t.Fatal("EnabledValue 应与 IsEnabled 一致")
	}
}

// TestCopyTunnelsDeepCopy 深拷贝：Network 切片也要独立，nil 保持 nil。
func TestCopyTunnelsDeepCopy(t *testing.T) {
	if CopyTunnels(nil) != nil {
		t.Fatal("nil 应保持 nil（避免把「没有隧道」写成显式空切片）")
	}

	src := []Tunnel{tunnelAt("a", "127.0.0.1:1", "tcp", "udp")}
	dst := CopyTunnels(src)
	dst[0].Network[0] = "changed"
	dst[0].Address = "changed"
	if src[0].Network[0] != "tcp" {
		t.Fatal("Network 与源共享底层数组")
	}
	if src[0].Address != "127.0.0.1:1" {
		t.Fatal("副本改动影响了源")
	}
}

// TestTemplateTunnelsFor 隧道按作用域分开取：档位只认自己的那一份，custom 走独立字段。
func TestTemplateTunnelsFor(t *testing.T) {
	cfg := SubscribeConfig{
		MergeTunnels:      map[string][]Tunnel{RuleGroupBase: {tunnelAt("b1", "127.0.0.1:1", "tcp")}},
		CustomModeTunnels: []Tunnel{tunnelAt("c1", "127.0.0.1:2", "tcp")},
		Subscriptions: []Subscription{
			{Name: "机场A", Tunnels: []Tunnel{tunnelAt("s1", "127.0.0.1:3", "tcp")}},
		},
	}

	if got := cfg.MergeTunnelsFor(RuleGroupBase); len(got) != 1 || got[0].ID != "b1" {
		t.Fatalf("base 档位隧道异常: %+v", got)
	}
	if got := cfg.MergeTunnelsFor(RuleGroupFull); got != nil {
		t.Fatalf("full 档位应为空，实际 %+v", got)
	}
	if got := cfg.TemplateTunnelsFor(RuleScopeCustom); len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("自定义模式隧道异常: %+v", got)
	}
	if got := cfg.TemplateTunnelsFor(RuleGroupBase); len(got) != 1 || got[0].ID != "b1" {
		t.Fatalf("档位作用域应读 merge_tunnels: %+v", got)
	}
	if got := cfg.SubscriptionTunnelsFor("机场A"); len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("订阅隧道异常: %+v", got)
	}
	if got := cfg.SubscriptionTunnelsFor("不存在"); got != nil {
		t.Fatalf("未知订阅应返回 nil，实际 %+v", got)
	}
}

// TestAdoptServerOwnedTunnelFields 隧道同样只由专用接口维护：过期快照不得覆盖服务端状态。
func TestAdoptServerOwnedTunnelFields(t *testing.T) {
	prev := SubscribeConfig{
		MergeTunnels:      map[string][]Tunnel{RuleGroupBase: {tunnelAt("b1", "127.0.0.1:1", "tcp")}},
		CustomModeTunnels: []Tunnel{tunnelAt("c1", "127.0.0.1:2", "tcp")},
		Subscriptions: []Subscription{
			{Name: "机场A", Tunnels: []Tunnel{tunnelAt("s1", "127.0.0.1:3", "tcp")}},
		},
	}
	// 请求体里带着一份过期快照：base 档位是空的（隧道已被用户删掉）、订阅隧道是旧值
	dst := SubscribeConfig{
		MergeTunnels:      map[string][]Tunnel{RuleGroupBase: {}},
		CustomModeTunnels: nil,
		Subscriptions: []Subscription{
			{Name: "机场A", Tunnels: []Tunnel{tunnelAt("stale", "127.0.0.1:9", "tcp")}},
			{Name: "新机场", Tunnels: []Tunnel{tunnelAt("new", "127.0.0.1:10", "tcp")}},
		},
	}

	dst.AdoptServerOwnedFields(prev)

	if len(dst.MergeTunnels[RuleGroupBase]) != 1 || dst.MergeTunnels[RuleGroupBase][0].ID != "b1" {
		t.Fatalf("融合档位隧道应取服务端状态，实际 %+v", dst.MergeTunnels[RuleGroupBase])
	}
	if len(dst.CustomModeTunnels) != 1 || dst.CustomModeTunnels[0].ID != "c1" {
		t.Fatalf("自定义模式隧道应取服务端状态，实际 %+v", dst.CustomModeTunnels)
	}
	if len(dst.Subscriptions[0].Tunnels) != 1 || dst.Subscriptions[0].Tunnels[0].ID != "s1" {
		t.Fatalf("已知订阅的隧道应取服务端状态，实际 %+v", dst.Subscriptions[0].Tunnels)
	}
	// 服务端不认识的全新订阅没有旧值可取：保留请求体内容，免得「带隧道创建订阅」丢数据
	if len(dst.Subscriptions[1].Tunnels) != 1 || dst.Subscriptions[1].Tunnels[0].ID != "new" {
		t.Fatalf("全新订阅的隧道应保留请求体内容，实际 %+v", dst.Subscriptions[1].Tunnels)
	}
	// 深拷贝：服务端那份随后被就地改动时不应影响已采纳的副本
	dst.MergeTunnels[RuleGroupBase][0].Address = "changed"
	if prev.MergeTunnels[RuleGroupBase][0].Address != "127.0.0.1:1" {
		t.Fatal("采纳的隧道与全局状态共享底层数组")
	}
}
