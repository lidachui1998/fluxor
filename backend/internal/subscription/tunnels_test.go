package subscription

import (
	"bytes"
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/configgen"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// tunnelOf 生成一条启用状态的隧道。
func tunnelOf(id, address, target, proxy string, networks ...string) config.Tunnel {
	return config.Tunnel{ID: id, Address: address, Target: target, Proxy: proxy, Network: networks}
}

// tunnelIDs 提取隧道 ID 序列，便于断言隔离与顺序。
func tunnelIDs(tunnels []config.Tunnel) []string {
	out := make([]string, 0, len(tunnels))
	for _, tunnel := range tunnels {
		out = append(out, tunnel.ID)
	}
	return out
}

// TestTunnelScopeEditable 三个作用域各只在所属模式下可编辑——「互不影响」的第一道闸门。
func TestTunnelScopeEditable(t *testing.T) {
	mergeScope, ok := mergeTunnelScope(config.RuleGroupBase)
	if !ok {
		t.Fatal("base 档位应解析为融合隧道作用域")
	}
	customScope := customModeTunnelScope()
	subScope := subscriptionTunnelScope("机场A")

	cases := []struct {
		mode               string
		merge, custom, sub bool
	}{
		{config.ModeMerge, true, false, false},
		{config.ModeCustom, false, true, false},
		{config.ModeSwitch, false, false, true},
		{"", false, false, false},
	}
	for _, tc := range cases {
		cfg := config.SubscribeConfig{Mode: tc.mode}
		if got := mergeScope.editable(cfg); got != tc.merge {
			t.Fatalf("模式 %q：融合档位可编辑应为 %v，实际 %v", tc.mode, tc.merge, got)
		}
		if got := customScope.editable(cfg); got != tc.custom {
			t.Fatalf("模式 %q：自定义模式可编辑应为 %v，实际 %v", tc.mode, tc.custom, got)
		}
		if got := subScope.editable(cfg); got != tc.sub {
			t.Fatalf("模式 %q：订阅作用域可编辑应为 %v，实际 %v", tc.mode, tc.sub, got)
		}
	}
	if _, ok := mergeTunnelScope("nope"); ok {
		t.Fatal("未知档位应被拒")
	}
	if _, ok := mergeTunnelScope(config.RuleScopeCustom); ok {
		t.Fatal("custom 不应被当成融合档位")
	}
}

// TestTunnelScopeActive 生效判定：融合只认当前档位，自定义恒生效，切换只认激活订阅。
func TestTunnelScopeActive(t *testing.T) {
	baseScope, _ := mergeTunnelScope(config.RuleGroupBase)
	fullScope, _ := mergeTunnelScope(config.RuleGroupFull)
	customScope := customModeTunnelScope()
	subScope := subscriptionTunnelScope("机场A")

	mergeFull := config.SubscribeConfig{Mode: config.ModeMerge, RuleGroup: config.RuleGroupFull}
	if baseScope.active(mergeFull) {
		t.Fatal("融合模式下非当前档位不应生效")
	}
	if !fullScope.active(mergeFull) {
		t.Fatal("融合模式下当前档位应生效")
	}
	if baseScope.active(config.SubscribeConfig{Mode: config.ModeCustom}) {
		t.Fatal("自定义模式下融合档位不应生效")
	}
	if !customScope.active(config.SubscribeConfig{Mode: config.ModeCustom}) {
		t.Fatal("自定义模式下应生效")
	}
	if !subScope.active(config.SubscribeConfig{Mode: config.ModeSwitch, ActiveSubscription: "机场A"}) {
		t.Fatal("切换模式下激活订阅应生效")
	}
	if subScope.active(config.SubscribeConfig{Mode: config.ModeSwitch, ActiveSubscription: "机场B"}) {
		t.Fatal("切换模式下非激活订阅不应生效")
	}
}

// TestTunnelScopesAreIsolated 四份存储互不串门：写一边绝不会出现在另一边。
func TestTunnelScopesAreIsolated(t *testing.T) {
	withCurrent(t, config.SubscribeConfig{
		Subscriptions: []config.Subscription{{Name: "机场A"}, {Name: "机场B"}},
	})

	baseScope, _ := mergeTunnelScope(config.RuleGroupBase)
	fullScope, _ := mergeTunnelScope(config.RuleGroupFull)
	customScope := customModeTunnelScope()
	subA := subscriptionTunnelScope("机场A")
	subB := subscriptionTunnelScope("机场B")

	baseScope.storeTunnels([]config.Tunnel{tunnelOf("b1", "127.0.0.1:1", "1.1.1.1:53", "", "tcp")})
	fullScope.storeTunnels([]config.Tunnel{tunnelOf("f1", "127.0.0.1:2", "1.1.1.1:53", "", "tcp")})
	customScope.storeTunnels([]config.Tunnel{tunnelOf("c1", "127.0.0.1:3", "1.1.1.1:53", "", "tcp")})
	subA.storeTunnels([]config.Tunnel{tunnelOf("sa", "127.0.0.1:4", "1.1.1.1:53", "", "tcp")})

	cfg := ruleConfigSnapshot()
	if got := tunnelIDs(baseScope.tunnels(cfg)); len(got) != 1 || got[0] != "b1" {
		t.Fatalf("base 档位隧道异常: %v", got)
	}
	if got := tunnelIDs(fullScope.tunnels(cfg)); len(got) != 1 || got[0] != "f1" {
		t.Fatalf("full 档位隧道异常: %v", got)
	}
	if got := tunnelIDs(customScope.tunnels(cfg)); len(got) != 1 || got[0] != "c1" {
		t.Fatalf("自定义模式隧道异常: %v", got)
	}
	if got := tunnelIDs(subA.tunnels(cfg)); len(got) != 1 || got[0] != "sa" {
		t.Fatalf("订阅 A 隧道异常: %v", got)
	}
	if got := subB.tunnels(cfg); len(got) != 0 {
		t.Fatalf("订阅 B 不应拿到别人的隧道: %v", tunnelIDs(got))
	}
	// 存放位置也必须分开：自定义模式不落在融合的 map 里
	if _, ok := cfg.MergeTunnels[config.RuleScopeCustom]; ok {
		t.Fatal("自定义模式的隧道不应写进 merge_tunnels")
	}
	if len(cfg.MergeTunnels[config.RuleGroupBase]) != 1 {
		t.Fatal("base 档位隧道被写坏")
	}
}

// TestPrepareTunnelDefaults 新增时的默认值：开关默认启用、id 由后端补、网络类型归一化。
func TestPrepareTunnelDefaults(t *testing.T) {
	ctx, err := configgen.MergeRuleSetContext(config.RuleGroupBase)
	if err != nil {
		t.Fatalf("构造上下文失败: %v", err)
	}
	groups := ctx.GroupNames()
	if len(groups) == 0 {
		t.Fatal("夹具应含代理组")
	}

	tunnel, err := prepareTunnel(config.Tunnel{
		Network: []string{"udp", "tcp"},
		Address: " 127.0.0.1:6553 ",
		Target:  " 8.8.8.8:53 ",
		Proxy:   " " + groups[0] + " ",
	}, ctx, nil, "")
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if tunnel.ID == "" {
		t.Fatal("应补上隧道 id")
	}
	if !tunnel.IsEnabled() {
		t.Fatal("未显式设置开关时应默认启用")
	}
	if tunnel.Enabled == nil {
		t.Fatal("应显式落一个布尔值，磁盘上不再出现「缺 enabled 键」的形态")
	}
	if tunnel.Address != "127.0.0.1:6553" || tunnel.Target != "8.8.8.8:53" || tunnel.Proxy != groups[0] {
		t.Fatalf("首尾空白未裁剪: %+v", tunnel)
	}
	if len(tunnel.Network) != 2 || tunnel.Network[0] != "tcp" || tunnel.Network[1] != "udp" {
		t.Fatalf("网络类型未归一化: %v", tunnel.Network)
	}
}

// TestPrepareTunnelNormalizesTarget 落库前定形目标：裸域名补 :80，裸 IP 明确报错。
//
// 实测 `Start tunnel example.com error: invalid target address example.com` —— 配置能加载、
// 内核照常运行，只是那条隧道永远起不来监听，因此必须在保存前把目标写成 host:port。
func TestPrepareTunnelNormalizesTarget(t *testing.T) {
	ctx, err := configgen.MergeRuleSetContext(config.RuleGroupBase)
	if err != nil {
		t.Fatalf("构造上下文失败: %v", err)
	}

	// 只写域名：补默认端口 80，且列表展示的即是实际目标
	tunnel, err := prepareTunnel(config.Tunnel{
		Network: []string{"tcp"}, Address: " 127.0.0.1:8888 ", Target: " example.com ",
	}, ctx, nil, "")
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if tunnel.Address != "127.0.0.1:8888" || tunnel.Target != "example.com:80" {
		t.Fatalf("地址/目标未归一化: %+v", tunnel)
	}
	if line := displayTunnelLine(tunnel); line != "tcp,127.0.0.1:8888,example.com:80" {
		t.Fatalf("展示文本应给出实际转发目标: %s", line)
	}

	// 带端口的域名/IP：原样保留
	for target, want := range map[string]string{"dns.google:53": "dns.google:53", "8.8.8.8:8888": "8.8.8.8:8888"} {
		if got, err := prepareTunnel(config.Tunnel{
			Network: []string{"tcp"}, Address: "127.0.0.1:8888", Target: target,
		}, ctx, nil, ""); err != nil || got.Target != want {
			t.Fatalf("目标 %s 应原样保留: %+v %v", target, got, err)
		}
	}

	// 裸 IP（含未加方括号的 IPv6）：拒绝，错误信息提示补端口（不猜 IP 的端口）
	for _, target := range []string{"8.8.8.8", "::1"} {
		if _, err := prepareTunnel(config.Tunnel{
			Network: []string{"tcp"}, Address: "127.0.0.1:8888", Target: target,
		}, ctx, nil, ""); err == nil {
			t.Fatalf("裸 IP 目标 %q 应被拒", target)
		}
	}

	// 关闭的隧道不做归一化也不校验：地址/目标写错了也仍然关得掉
	disabled := false
	if got, err := prepareTunnel(config.Tunnel{
		Network: []string{"tcp"}, Address: "写错了", Target: "8.8.8.8", Enabled: &disabled,
	}, ctx, nil, ""); err != nil || got.Address != "写错了" || got.Target != "8.8.8.8" {
		t.Fatalf("关闭的隧道应原样保留: %+v %v", got, err)
	}
}

// TestPrepareTunnelAllowsDisablingInvalidTunnel 关闭一条已失效的隧道必须走得通。
//
// 场景：机场把代理组改了名，隧道引用的 proxy 不存在 → 校验必然失败。此时用户唯一的
// 操作就是把开关关掉（关掉的隧道不写进 config.yaml，不需要校验）。若这里也拦，用户
// 就只能删掉重建。
func TestPrepareTunnelAllowsDisablingInvalidTunnel(t *testing.T) {
	ctx, err := configgen.MergeRuleSetContext(config.RuleGroupBase)
	if err != nil {
		t.Fatalf("构造上下文失败: %v", err)
	}
	enabled, disabled := true, false

	// 启用状态下引用不存在的代理组：必须被拒
	if _, err := prepareTunnel(config.Tunnel{
		Network: []string{"tcp"}, Address: "127.0.0.1:6553", Target: "8.8.8.8:53", Proxy: "已改名的组", Enabled: &enabled,
	}, ctx, nil, ""); err == nil {
		t.Fatal("启用状态下 proxy 不存在应被拒")
	}

	// 关闭状态下同样内容：放行（不写进配置，校验没有意义）
	tunnel, err := prepareTunnel(config.Tunnel{
		Network: []string{"tcp"}, Address: "127.0.0.1:6553", Target: "8.8.8.8:53", Proxy: "已改名的组", Enabled: &disabled,
	}, ctx, nil, "")
	if err != nil {
		t.Fatalf("关闭一条失效隧道不应被拦: %v", err)
	}
	if tunnel.IsEnabled() {
		t.Fatal("开关状态应被保留")
	}

	// 关闭的隧道不参与判重：两条同地址隧道，其中一条关着不该报冲突
	existing := []config.Tunnel{tunnelOf("t1", "127.0.0.1:6553", "1.1.1.1:53", "", "tcp")}
	off := tunnelOf("t2", "127.0.0.1:6553", "1.1.1.1:53", "", "tcp")
	off.Enabled = &disabled
	if _, err := prepareTunnel(off, ctx, existing, ""); err != nil {
		t.Fatalf("关闭的重复项不应报冲突: %v", err)
	}
	// 开着的时候必须报冲突：内核会因端口占用起不来
	on := tunnelOf("t3", "127.0.0.1:6553", "1.1.1.1:53", "", "tcp", "udp")
	if _, err := prepareTunnel(on, ctx, existing, ""); err == nil {
		t.Fatal("启用的同地址隧道应报冲突")
	}
	// 修改自身时不应与自己冲突
	if _, err := prepareTunnel(existing[0], ctx, existing, "t1"); err != nil {
		t.Fatalf("修改自身不应报冲突: %v", err)
	}
}

// TestBuildTunnelViews 视图：合法/非法原因与「订阅文件未就绪」三种形态都要能表达。
func TestBuildTunnelViews(t *testing.T) {
	ctx, err := configgen.MergeRuleSetContext(config.RuleGroupBase)
	if err != nil {
		t.Fatalf("构造上下文失败: %v", err)
	}
	groups := ctx.GroupNames()
	disabled := false
	tunnels := []config.Tunnel{
		tunnelOf("ok", "127.0.0.1:6553", "8.8.8.8:53", groups[0], "tcp", "udp"),
		tunnelOf("bad", "127.0.0.1:6554", "8.8.8.8:53", "没这个组", "tcp"),
		{ID: "off", Network: []string{"udp"}, Address: "127.0.0.1:6555", Target: "8.8.8.8:53", Enabled: &disabled},
	}

	views := buildTunnelViews(tunnels, ctx, "")
	if len(views) != 3 {
		t.Fatalf("视图条数错误: %d", len(views))
	}
	if !views[0].Valid || views[0].Reason != "" || !views[0].Enabled {
		t.Fatalf("合法隧道视图异常: %+v", views[0])
	}
	if views[0].Line != "tcp/udp,127.0.0.1:6553,8.8.8.8:53,"+groups[0] {
		t.Fatalf("单行展示形式异常: %s", views[0].Line)
	}
	if views[1].Valid || views[1].Reason == "" {
		t.Fatalf("非法隧道应带原因: %+v", views[1])
	}
	// 关闭的隧道同样参与校验（界面上要能看出它为什么无效），但开关状态如实下发
	if !views[2].Valid || views[2].Enabled {
		t.Fatalf("关闭的隧道视图异常: %+v", views[2])
	}
	if views[2].Line != "udp,127.0.0.1:6555,8.8.8.8:53" {
		t.Fatalf("未填 proxy 时单行形式不应有尾随逗号: %s", views[2].Line)
	}

	// 订阅文件未就绪：统一标注不可校验的原因，但内容照常下发（用户要能删掉它）
	notReady := buildTunnelViews(tunnels, nil, "订阅文件尚未就绪，请先保存并应用")
	for i, view := range notReady {
		if view.Valid || view.Reason != "订阅文件尚未就绪，请先保存并应用" {
			t.Fatalf("第 %d 条未就绪视图异常: %+v", i, view)
		}
	}
}

// TestServeTunnelRequestLifecycle 走一遍真实 HTTP 链路：新增 / 排序 / 关闭 / 删除。
//
// 融合模式下隧道挂规则集档位，写入后立即持久化并在「当前档位生效」时重新生成 config.yaml；
// 这里把配置目标指到临时目录，顺带断言产物里隧道块的落点与开关行为。
func TestServeTunnelRequestLifecycle(t *testing.T) {
	dir := t.TempDir()
	oldTarget, oldWorkDir, oldPid := config.ConfigTarget, config.CoreWorkDir, config.CorePidFile
	oldConfigFile := config.FluxorConfigFile
	// 三处运行路径都指向临时目录：写操作会真的落盘（持久化 + 重新生成 config.yaml）
	config.ConfigTarget = filepath.Join(dir, "config.yaml")
	config.CoreWorkDir = dir
	config.FluxorConfigFile = filepath.Join(dir, "fluxor.json")
	// 内核 PID 文件指向不存在的路径：避免测试机真的跑着内核时触发热重载
	config.CorePidFile = filepath.Join(dir, "core.pid")
	defer func() {
		config.ConfigTarget, config.CoreWorkDir, config.CorePidFile = oldTarget, oldWorkDir, oldPid
		config.FluxorConfigFile = oldConfigFile
	}()

	withCurrent(t, config.SubscribeConfig{
		Mode:          config.ModeMerge,
		RuleGroup:     config.RuleGroupBase,
		Subscriptions: []config.Subscription{{Name: "机场A"}},
	})

	scope, ok := mergeTunnelScope(config.RuleGroupBase)
	if !ok {
		t.Fatal("构造融合隧道作用域失败")
	}
	ctx, err := scope.context(ruleConfigSnapshot())
	if err != nil {
		t.Fatalf("构造上下文失败: %v", err)
	}
	groups := ctx.GroupNames()

	// 新增两条：第一条引用代理组，第二条不指定 proxy
	t1 := tunnelOf("", "127.0.0.1:6553", "8.8.8.8:53", groups[0], "tcp", "udp")
	t2 := tunnelOf("", "127.0.0.1:6554", "dns.google:53", "", "udp")
	for _, payload := range []config.Tunnel{t1, t2} {
		rec := httptest.NewRecorder()
		body, _ := json.Marshal(payload)
		serveTunnelRequest(rec, httptest.NewRequest(http.MethodPost, config.BaseURL+mergeTunnelsRoutePath+config.RuleGroupBase, bytes.NewReader(body)), scope)
		if rec.Code != http.StatusOK {
			t.Fatalf("新增失败: %d %s", rec.Code, rec.Body.String())
		}
		var resp customRulesPayload
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if len(resp.Tunnels) == 0 {
			t.Fatal("响应里应带上隧道列表（规则接口与隧道接口必须同构）")
		}
	}

	cfg := ruleConfigSnapshot()
	stored := scope.tunnels(cfg)
	if len(stored) != 2 || stored[0].ID == "" || stored[1].ID == "" {
		t.Fatalf("隧道未落库或未补 id: %+v", stored)
	}
	// 生效作用域写入后应立即重新生成 config.yaml
	configYAML, err := os.ReadFile(config.ConfigTarget)
	if err != nil {
		t.Fatalf("运行配置未生成: %v", err)
	}
	if !bytes.Contains(configYAML, []byte("tunnels:")) {
		t.Fatalf("产物里缺少 tunnels 块:\n%s", configYAML)
	}

	// 排序：第二条上移
	rec := httptest.NewRecorder()
	moveBody, _ := json.Marshal(moveRuleRequest{ID: stored[1].ID, Direction: config.RuleMoveUp})
	serveTunnelRequest(rec, httptest.NewRequest(http.MethodPatch, config.BaseURL+mergeTunnelsRoutePath+config.RuleGroupBase, bytes.NewReader(moveBody)), scope)
	if rec.Code != http.StatusOK {
		t.Fatalf("排序失败: %d %s", rec.Code, rec.Body.String())
	}
	if got := tunnelIDs(scope.tunnels(ruleConfigSnapshot())); got[0] != stored[1].ID {
		t.Fatalf("排序未生效: %v", got)
	}
	// 已在最前，继续上移必须如实报错而不是假装成功
	rec = httptest.NewRecorder()
	serveTunnelRequest(rec, httptest.NewRequest(http.MethodPatch, config.BaseURL+mergeTunnelsRoutePath+config.RuleGroupBase, bytes.NewReader(moveBody)), scope)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("边界移动应回 400，实际 %d", rec.Code)
	}

	// 关闭「引用代理组」的那条（开关走 PUT）：产物中该条消失，另一条照旧生效。
	// 按地址定位而不是按下标：上面刚做过排序，下标已经变了
	byAddress := make(map[string]config.Tunnel)
	for _, item := range scope.tunnels(ruleConfigSnapshot()) {
		byAddress[item.Address] = item
	}
	target, ok := byAddress["127.0.0.1:6553"]
	if !ok {
		t.Fatalf("找不到待关闭的隧道: %v", tunnelIDs(scope.tunnels(ruleConfigSnapshot())))
	}
	disabled := false
	target.Enabled = &disabled
	rec = httptest.NewRecorder()
	toggleBody, _ := json.Marshal(target)
	serveTunnelRequest(rec, httptest.NewRequest(http.MethodPut, config.BaseURL+mergeTunnelsRoutePath+config.RuleGroupBase, bytes.NewReader(toggleBody)), scope)
	if rec.Code != http.StatusOK {
		t.Fatalf("关闭开关失败: %d %s", rec.Code, rec.Body.String())
	}
	configYAML, err = os.ReadFile(config.ConfigTarget)
	if err != nil {
		t.Fatalf("读取运行配置失败: %v", err)
	}
	if bytes.Contains(configYAML, []byte("127.0.0.1:6553")) {
		t.Fatalf("关闭的隧道不应写进产物:\n%s", configYAML)
	}
	if !bytes.Contains(configYAML, []byte("127.0.0.1:6554")) {
		t.Fatalf("仍启用的隧道应留在产物里:\n%s", configYAML)
	}

	// 删除剩下的那条
	remaining := scope.tunnels(ruleConfigSnapshot())
	if len(remaining) != 2 {
		t.Fatalf("两条隧道都应保留在列表里（关闭只是不写进配置）: %v", tunnelIDs(remaining))
	}
	rec = httptest.NewRecorder()
	serveTunnelRequest(rec, httptest.NewRequest(http.MethodDelete, config.BaseURL+mergeTunnelsRoutePath+config.RuleGroupBase+"?id="+byAddress["127.0.0.1:6554"].ID, nil), scope)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除失败: %d %s", rec.Code, rec.Body.String())
	}
	if got := scope.tunnels(ruleConfigSnapshot()); len(got) != 1 || got[0].Address != "127.0.0.1:6553" {
		t.Fatalf("删除未生效: %v", tunnelIDs(got))
	}

	// 删除不存在的 id：404 而不是静默成功
	rec = httptest.NewRecorder()
	serveTunnelRequest(rec, httptest.NewRequest(http.MethodDelete, config.BaseURL+mergeTunnelsRoutePath+config.RuleGroupBase+"?id=missing", nil), scope)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("删除不存在的隧道应回 404，实际 %d", rec.Code)
	}
}

// TestServeTunnelRequestModeGuards 跨模式访问必须被拒且指出该去哪儿改。
func TestServeTunnelRequestModeGuards(t *testing.T) {
	withCurrent(t, config.SubscribeConfig{Mode: config.ModeCustom})

	scope, _ := mergeTunnelScope(config.RuleGroupBase)
	rec := httptest.NewRecorder()
	serveTunnelRequest(rec, httptest.NewRequest(http.MethodGet, config.BaseURL+mergeTunnelsRoutePath+config.RuleGroupBase, nil), scope)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("自定义模式下访问融合入口应回 400，实际 %d", rec.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp["message"] == "" {
		t.Fatal("拒绝时应说明原因与入口")
	}
}
