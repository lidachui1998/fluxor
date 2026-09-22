package configgen

import (
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// tunnelOf 生成一条启用状态的隧道（Address/Target 合法，proxy 可为空）。
func tunnelOf(id, address, target, proxy string, networks ...string) config.Tunnel {
	return config.Tunnel{ID: id, Address: address, Target: target, Proxy: proxy, Network: networks}
}

// disabledTunnel 把隧道标记为关闭（开关不写进 config.yaml）。
func disabledTunnel(tunnel config.Tunnel) config.Tunnel {
	enabled := false
	tunnel.Enabled = &enabled
	return tunnel
}

// tunnelEnv 构造一个含代理组与内置目标的校验环境。
func tunnelEnv(t *testing.T) configcheck.RuleEnv {
	t.Helper()
	env, err := MergeRuleSetEnv(RuleGroupBase)
	if err != nil {
		t.Fatalf("构造规则环境失败: %v", err)
	}
	return env
}

// TestValidateTunnel 校验项：网络类型、host:port 形式、端口范围、proxy 是否存在。
func TestValidateTunnel(t *testing.T) {
	env := tunnelEnv(t)
	base := tunnelOf("t1", "127.0.0.1:6553", "8.8.8.8:53", "", "tcp", "udp")

	cases := []struct {
		name   string
		mutate func(config.Tunnel) config.Tunnel
		isErr  bool
	}{
		{name: "合法", mutate: func(x config.Tunnel) config.Tunnel { return x }},
		{name: "关闭 proxy", mutate: func(x config.Tunnel) config.Tunnel { return x }},
		{name: "合法代理组", mutate: func(x config.Tunnel) config.Tunnel { x.Proxy = "🚀 节点选择"; return x }},
		{name: "内置目标", mutate: func(x config.Tunnel) config.Tunnel { x.Proxy = "DIRECT"; return x }},
		{name: "目标用域名", mutate: func(x config.Tunnel) config.Tunnel { x.Target = "dns.google:53"; return x }},
		// 目标只写域名时补默认端口（:80）；裸 IP 不猜端口，要求用户写明
		{name: "目标裸域名", mutate: func(x config.Tunnel) config.Tunnel { x.Target = "example.com"; return x }},
		{name: "目标裸 IPv4", mutate: func(x config.Tunnel) config.Tunnel { x.Target = "8.8.8.8"; return x }, isErr: true},
		{name: "目标 IPv6 未加方括号", mutate: func(x config.Tunnel) config.Tunnel { x.Target = "::1"; return x }, isErr: true},
		{name: "目标含冒号但非 host:port", mutate: func(x config.Tunnel) config.Tunnel { x.Target = "a:b"; return x }, isErr: true},
		{name: "目标端口越界", mutate: func(x config.Tunnel) config.Tunnel { x.Target = "8.8.8.8:99999"; return x }, isErr: true},
		{name: "目标带方括号 IPv6", mutate: func(x config.Tunnel) config.Tunnel { x.Target = "[::1]:53"; return x }},
		{name: "IPv6 地址", mutate: func(x config.Tunnel) config.Tunnel { x.Address = "[::1]:6553"; return x }},
		{name: "网络类型非法", mutate: func(x config.Tunnel) config.Tunnel { x.Network = []string{"icmp"}; return x }, isErr: true},
		{name: "网络类型为空", mutate: func(x config.Tunnel) config.Tunnel { x.Network = nil; return x }, isErr: true},
		{name: "地址缺端口", mutate: func(x config.Tunnel) config.Tunnel { x.Address = "127.0.0.1"; return x }, isErr: true},
		{name: "地址为空", mutate: func(x config.Tunnel) config.Tunnel { x.Address = "  "; return x }, isErr: true},
		{name: "端口越界", mutate: func(x config.Tunnel) config.Tunnel { x.Address = "127.0.0.1:70000"; return x }, isErr: true},
		{name: "端口为 0", mutate: func(x config.Tunnel) config.Tunnel { x.Address = "127.0.0.1:0"; return x }, isErr: true},
		{name: "端口非数字", mutate: func(x config.Tunnel) config.Tunnel { x.Address = "127.0.0.1:abc"; return x }, isErr: true},
		{name: "proxy 不存在", mutate: func(x config.Tunnel) config.Tunnel { x.Proxy = "不存在的组"; return x }, isErr: true},
		{name: "proxy 含逗号", mutate: func(x config.Tunnel) config.Tunnel { x.Proxy = "A,B"; return x }, isErr: true},
	}
	for _, tc := range cases {
		tunnel := tc.mutate(base)
		err := ValidateTunnel(tunnel, env)
		if tc.isErr && err == nil {
			t.Fatalf("%s：应报错", tc.name)
		}
		if !tc.isErr && err != nil {
			t.Fatalf("%s：不应报错: %v", tc.name, err)
		}
	}
}

// TestApplyTunnelsWritesEnabledOnly 只有开着的隧道进产物；关闭的既不写入也不报原因。
func TestApplyTunnelsWritesEnabledOnly(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	tunnels := []config.Tunnel{
		tunnelOf("t1", "127.0.0.1:6553", "8.8.8.8:53", "🚀 节点选择", "tcp", "udp"),
		disabledTunnel(tunnelOf("t2", "127.0.0.1:7777", "dns.google:53", "", "udp")),
	}

	result, err := ApplyTunnels(doc, tunnels)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if result.Applied != 1 || result.Disabled != 1 || len(result.Skipped) != 0 {
		t.Fatalf("期望写入 1 条、关闭 1 条、无跳过，实际 %+v", result)
	}

	items := tunnelItems(t, doc)
	if len(items) != 1 {
		t.Fatalf("产物隧道条数错误: %v", items)
	}
	if items[0]["address"] != "127.0.0.1:6553" || items[0]["target"] != "8.8.8.8:53" || items[0]["proxy"] != "🚀 节点选择" {
		t.Fatalf("隧道字段错误: %+v", items[0])
	}
	if networks := items[0]["network"].([]any); len(networks) != 2 || networks[0] != "tcp" || networks[1] != "udp" {
		t.Fatalf("network 应为 [tcp udp]，实际 %+v", networks)
	}
}

// TestApplyTunnelsSkipsInvalid 无效项被跳过而不是写进配置：proxy 不存在会让内核拒绝整份配置。
func TestApplyTunnelsSkipsInvalid(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	tunnels := []config.Tunnel{
		tunnelOf("t1", "127.0.0.1:6553", "8.8.8.8:53", "已删除的组", "tcp"),
		tunnelOf("t2", "127.0.0.1:6554", "8.8.8.8:53", "", "tcp"),
	}

	result, err := ApplyTunnels(doc, tunnels)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if result.Applied != 1 || len(result.Skipped) != 1 || result.Skipped[0].Tunnel.ID != "t1" {
		t.Fatalf("期望跳过 t1，实际 %+v", result)
	}
	items := tunnelItems(t, doc)
	if len(items) != 1 || items[0]["address"] != "127.0.0.1:6554" {
		t.Fatalf("产物应只含合法的那条: %+v", items)
	}
}

// TestApplyTunnelsNormalizesTarget 目标归一化随写入生效：裸域名补 :80，裸 IP 被跳过。
//
// 内核用 socks5.ParseAddr 解析目标，裸主机解析失败后**只打一行日志并跳过该条**
// （实测 `Start tunnel example.com error: invalid target address example.com`）：配置能加载、
// 内核照常运行，隧道却静默失效。因此写入前必须把目标定形成 host:port。
func TestApplyTunnelsNormalizesTarget(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	tunnels := []config.Tunnel{
		tunnelOf("t1", "127.0.0.1:8888", "example.com", "", "tcp"),
		tunnelOf("t2", "127.0.0.1:8889", "8.8.8.8", "", "tcp"),
		tunnelOf("t3", "127.0.0.1:8890", "dns.google:53", "", "tcp"),
	}

	result, err := ApplyTunnels(doc, tunnels)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	// t1 补 :80 后写入；t2 是裸 IP 被跳过；t3 原样写入
	if result.Applied != 2 || len(result.Skipped) != 1 || result.Skipped[0].Tunnel.ID != "t2" {
		t.Fatalf("期望跳过裸 IP 那条，实际 %+v", result)
	}
	items := tunnelItems(t, doc)
	if items[0]["target"] != "example.com:80" || items[1]["target"] != "dns.google:53" {
		t.Fatalf("目标未按归一化结果写入: %+v", items)
	}
}

// TestApplyTunnelsSkipsDuplicateBinding 同地址且网络有交集的项只写第一条（内核会端口冲突）。
//
// 判重按「网络是否相交」而不是比字符串：tcp 与 udp 互不冲突（内核各起一个监听器），
// 而 [tcp, udp] 与 [tcp] 形态不同却会抢同一个 tcp 端口。
func TestApplyTunnelsSkipsDuplicateBinding(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	tunnels := []config.Tunnel{
		tunnelOf("t1", "127.0.0.1:6553", "8.8.8.8:53", "", "tcp"),
		tunnelOf("t2", "127.0.0.1:6553", "1.1.1.1:53", "", "tcp", "udp"),
		tunnelOf("t3", "127.0.0.1:6553", "9.9.9.9:53", "", "udp"),
	}

	result, err := ApplyTunnels(doc, tunnels)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	// t1（tcp）与 t3（udp）互不冲突；t2 与两者都抢端口，必须被跳过
	if result.Applied != 2 || len(result.Skipped) != 1 || result.Skipped[0].Tunnel.ID != "t2" {
		t.Fatalf("期望跳过 t2，实际 %+v", result)
	}
	if len(tunnelItems(t, doc)) != 2 {
		t.Fatalf("产物条数错误: %+v", tunnelItems(t, doc))
	}
}

// TestApplyTunnelsNoopWhenNothingEnabled 没有任何启用项时完全不触碰文档。
//
// 切换模式的 config.yaml 是订阅文件副本：注入过的那一块会在下次复制时自然消失，
// 因此不需要（也不应该）为了「清空」去删除机场自带的 tunnels 块。
func TestApplyTunnelsNoopWhenNothingEnabled(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	before, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	result, err := ApplyTunnels(doc, []config.Tunnel{disabledTunnel(tunnelOf("t1", "127.0.0.1:1", "1.1.1.1:53", "", "tcp"))})
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if result.Applied != 0 || result.Disabled != 1 {
		t.Fatalf("结果异常: %+v", result)
	}
	after, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("无启用隧道时不应改动文档:\n%s", after)
	}
	if doc.Get("tunnels") != nil {
		t.Fatal("不应凭空写入 tunnels 键")
	}
}

// TestApplyTunnelsBeforeRules 产物中 tunnels 必须排在 rule-providers / rules 之前。
func TestApplyTunnelsBeforeRules(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if _, err := ApplyTunnels(doc, []config.Tunnel{tunnelOf("t1", "127.0.0.1:6553", "8.8.8.8:53", "", "tcp")}); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	keys := topLevelKeys(t, doc)
	if mustIndex(t, keys, "tunnels") > mustIndex(t, keys, "rule-providers") {
		t.Fatalf("未归一化前 tunnels 也应插在 rule-providers 之前: %v", keys)
	}

	// 生成链路随后还会按 topBlockRank 归一化键序，结论必须一致
	doc.OrderTopLevel()
	ordered := topLevelKeys(t, doc)
	if !(mustIndex(t, ordered, "proxy-groups") < mustIndex(t, ordered, "tunnels") &&
		mustIndex(t, ordered, "tunnels") < mustIndex(t, ordered, "rule-providers")) {
		t.Fatalf("归一化后 tunnels 位置错误: %v", ordered)
	}
	if mustIndex(t, ordered, "rules") < mustIndex(t, ordered, "tunnels") {
		t.Fatalf("归一化后 tunnels 应排在 rules 之前: %v", ordered)
	}
}

// TestApplyRuntimeOverridesToFile 切换模式的落盘链路：规则与隧道一趟写入、可重复执行。
func TestApplyRuntimeOverridesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(providerFixture), 0644); err != nil {
		t.Fatalf("写入夹具失败: %v", err)
	}

	rules := []config.CustomRule{ruleOf(t, "DOMAIN-SUFFIX", "ads.example.com", "REJECT", config.RulePositionBefore, false)}
	tunnels := []config.Tunnel{tunnelOf("t1", "127.0.0.1:6553", "8.8.8.8:53", "🚀 节点选择", "tcp", "udp")}

	result, tunnelResult, err := ApplyRuntimeOverridesToFile(path, rules, tunnels)
	if err != nil {
		t.Fatalf("改写失败: %v", err)
	}
	if result.Applied != 1 || tunnelResult.Applied != 1 {
		t.Fatalf("写入数量异常: rules=%+v tunnels=%+v", result, tunnelResult)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	// 幂等：重复执行产物逐字节一致（订阅每次更新都会重放一次注入）
	if _, _, err := ApplyRuntimeOverridesToFile(path, rules, tunnels); err != nil {
		t.Fatalf("重复改写失败: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("重复注入产物不一致:\n%s\n---\n%s", first, second)
	}

	doc, err := configcheck.ParseDoc(second)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 订阅文件本身没有被改动：rules 多了一条自定义规则、tunnels 是新键
	if doc.Get("tunnels") == nil {
		t.Fatal("未写入 tunnels 块")
	}
	lines := ruleLines(t, doc)
	if lines[0] != "DOMAIN-SUFFIX,ads.example.com,REJECT" {
		t.Fatalf("自定义规则未插到最前: %v", lines)
	}
	// 隧道键必须落在 rules 之前（切换模式的 config.yaml 不做整体键序归一）
	tunnelKeys := topLevelKeys(t, doc)
	if mustIndex(t, tunnelKeys, "tunnels") > mustIndex(t, tunnelKeys, "rules") {
		t.Fatalf("tunnels 应写在 rules 之前: %v", tunnelKeys)
	}
}

// TestApplyRuntimeOverridesToFileNoop 两者都为空时完全不读写文件。
func TestApplyRuntimeOverridesToFileNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("not: yaml-for-clash\n"), 0644); err != nil {
		t.Fatalf("写入夹具失败: %v", err)
	}
	if _, _, err := ApplyRuntimeOverridesToFile(path, nil, nil); err != nil {
		t.Fatalf("空输入不应报错: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(content) != "not: yaml-for-clash\n" {
		t.Fatalf("空输入不应改写文件: %s", content)
	}
}

// TestTunnelSkipsMatchesApply 提示侧与写入侧必须给出同一结论。
//
// 两处若各写一份判断，用户会遇到「提示没问题、产物里少一条」这类无从排查的现象。
func TestTunnelSkipsMatchesApply(t *testing.T) {
	ctx, err := MergeRuleSetContext(RuleGroupBase)
	if err != nil {
		t.Fatalf("构造上下文失败: %v", err)
	}
	tunnels := []config.Tunnel{
		tunnelOf("ok", "127.0.0.1:6553", "8.8.8.8:53", "🚀 节点选择", "tcp", "udp"),
		tunnelOf("dup", "127.0.0.1:6553", "1.1.1.1:53", "", "tcp"),
		tunnelOf("badproxy", "127.0.0.1:6554", "1.1.1.1:53", "没这个组", "tcp"),
		tunnelOf("badddr", "127.0.0.1", "1.1.1.1:53", "", "tcp"),
		disabledTunnel(tunnelOf("off", "127.0.0.1:9999", "1.1.1.1:53", "没这个组", "tcp")),
	}

	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	result, err := ApplyTunnels(doc, tunnels)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}

	skips := TunnelSkips(tunnels, ctx)
	if len(skips) != len(result.Skipped) {
		t.Fatalf("提示与写入不一致: 提示 %d 条、实际跳过 %d 条（%v vs %v）", len(skips), len(result.Skipped), skips, result.Skipped)
	}
	// 关闭的那条不算「未写入」：它本来就不该进配置，报出来会误导用户
	if len(skips) != 3 {
		t.Fatalf("应报告 3 条无效隧道，实际 %v", skips)
	}
}

// TestApplyTunnelsInGeneratedConfig 融合/自定义模式的产物里 tunnels 与规则并存且位置正确。
func TestApplyTunnelsInGeneratedConfig(t *testing.T) {
	dir := t.TempDir()
	oldTarget, oldWorkDir := config.ConfigTarget, config.CoreWorkDir
	config.ConfigTarget = filepath.Join(dir, "config.yaml")
	config.CoreWorkDir = dir
	defer func() {
		config.ConfigTarget, config.CoreWorkDir = oldTarget, oldWorkDir
	}()

	cfg := config.SubscribeConfig{
		ProxyPort: 7890, TproxyPort: 7898, PanelPort: 9090, Mode: config.ModeCustom,
		RuleGroup:   config.RuleGroupBase,
		CustomNodes: []config.CustomNode{{ID: "n1", Name: "自建节点", Type: "ss", Config: map[string]any{"server": "127.0.0.1", "port": 1, "cipher": "aes-128-gcm", "password": "x"}}},
		CustomModeTunnels: []config.Tunnel{
			tunnelOf("t1", "127.0.0.1:6553", "8.8.8.8:53", "自建节点", "tcp", "udp"),
			disabledTunnel(tunnelOf("t2", "127.0.0.1:6554", "1.1.1.1:53", "", "tcp")),
		},
	}
	if err := GenerateCustomConfig(cfg); err != nil {
		t.Fatalf("生成配置失败: %v", err)
	}
	content, err := os.ReadFile(config.ConfigTarget)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	doc, err := configcheck.ParseDoc(content)
	if err != nil {
		t.Fatalf("解析产物失败: %v", err)
	}
	items := tunnelItems(t, doc)
	if len(items) != 1 || items[0]["proxy"] != "自建节点" {
		t.Fatalf("产物隧道应为引用手工节点的那一条: %+v", items)
	}
	keys := topLevelKeys(t, doc)
	if mustIndex(t, keys, "proxies") > mustIndex(t, keys, "tunnels") || mustIndex(t, keys, "tunnels") > mustIndex(t, keys, "rules") {
		t.Fatalf("产物键序错误（tunnels 应在 proxies 之后、rules 之前）: %v", keys)
	}
}

// TestTunnelsAcceptedByCore 可选的内核校验：把产物交给真实内核 `-t`。
//
// 运行方式（缺省跳过，常规单测不依赖外部二进制）：
//
//	FLUXOR_CORE_BIN=/path/to/mihomo go test ./internal/configgen/ -run TestTunnelsAcceptedByCore -v
//
// 夹具刻意不含 GEOIP/GEOSITE 规则，因此不需要 geoip.metadb / geosite.dat。
// 校验点：tunnels 块的字段名与类型被内核接受，proxy 能解析到配置里的代理组，
// 且「只写域名的目标」经归一化后产出的地址仍是内核认得的形态。
//
// 注意 `-t` 只能证明「配置可加载」：目标写法不对时内核**不会**拒绝加载，而是在起监听
// 那一步打一行 `Start tunnel example.com error: invalid target address example.com` 并跳过
// 该条（实测，隧道静默失效）。因此「裸域名目标」这条规则由 NormalizeTunnelTarget 的
// 单测负责，这里只做产物层面的回归。
func TestTunnelsAcceptedByCore(t *testing.T) {
	coreBin := os.Getenv("FLUXOR_CORE_BIN")
	if coreBin == "" {
		t.Skip("未设置 FLUXOR_CORE_BIN，跳过内核校验")
	}

	// 隧道块走真实写入路径（ApplyTunnels）生成，而不是手写 YAML：
	// 这样连归一化（网络类型、裸域名目标补端口）也一并交给内核检验
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析夹具失败: %v", err)
	}
	tunnels := []config.Tunnel{
		tunnelOf("t1", "127.0.0.1:6553", "8.8.8.8:53", "🚀 节点选择", "tcp", "udp"),
		// 只写域名的目标：写入时补默认端口 :80
		tunnelOf("t2", "0.0.0.0:6554", "example.com", "", "tcp"),
		// 关闭的隧道不该出现在产物里
		disabledTunnel(tunnelOf("t3", "127.0.0.1:6555", "1.1.1.1:53", "", "tcp")),
	}
	if _, err := ApplyTunnels(doc, tunnels); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	content, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), content, 0644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cmd := exec.Command(coreBin, "-t", "-d", dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("内核拒绝该配置: %v\n%s", err, output)
	}
}

// tunnelItems 读取文档中的 tunnels 序列，逐项还原为 map 便于断言。
func tunnelItems(t *testing.T, doc *configcheck.Doc) []map[string]any {
	t.Helper()
	seq := doc.Get("tunnels")
	if seq == nil {
		return nil
	}
	if seq.Kind != yaml.SequenceNode {
		t.Fatalf("tunnels 应为序列，实际 kind=%d", seq.Kind)
	}
	var items []map[string]any
	if err := seq.Decode(&items); err != nil {
		t.Fatalf("解析 tunnels 失败: %v", err)
	}
	return items
}

// topLevelKeys 返回文档的顶层键序列（用于断言键的先后）。
func topLevelKeys(t *testing.T, doc *configcheck.Doc) []string {
	t.Helper()
	content, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(root.Content) == 0 {
		t.Fatal("文档为空")
	}
	top := root.Content[0]
	keys := make([]string, 0, len(top.Content)/2)
	for i := 0; i+1 < len(top.Content); i += 2 {
		keys = append(keys, top.Content[i].Value)
	}
	return keys
}

// mustIndex 返回键在顶层键序中的下标；键不存在时直接失败（用 -1 参与大小比较会静默放行）。
func mustIndex(t *testing.T, keys []string, key string) int {
	t.Helper()
	idx := indexOf(keys, key)
	if idx < 0 {
		t.Fatalf("顶层键 %s 不存在: %v", key, keys)
	}
	return idx
}
