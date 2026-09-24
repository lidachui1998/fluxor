package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// setupTempDataDir 把数据目录指到临时目录并载入空配置，返回目录路径。
//
// 四个 store 是包级单例，因此每个用例都要重新 Load（LoadAll 会顺带跑迁移与 GC）。
func setupTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetDataDir(dir)
	t.Cleanup(capturePaths())
	LoadAll()
	return dir
}

// —— 测试辅助：把「整份列表写回」包装成锁内读—改—写（与生产调用点同形）——

func setSubRules(t *testing.T, name string, rules []CustomRule) {
	t.Helper()
	if err := UpdateSubscriptionRules(name, func([]CustomRule) ([]CustomRule, error) { return rules, nil }); err != nil {
		t.Fatalf("UpdateSubscriptionRules: %v", err)
	}
}

func setSubTunnels(t *testing.T, name string, tunnels []Tunnel) {
	t.Helper()
	if err := UpdateSubscriptionTunnels(name, func([]Tunnel) ([]Tunnel, error) { return tunnels, nil }); err != nil {
		t.Fatalf("UpdateSubscriptionTunnels: %v", err)
	}
}

func setTemplateRules(t *testing.T, scope string, rules []CustomRule) {
	t.Helper()
	if err := UpdateTemplateRules(scope, func([]CustomRule) ([]CustomRule, error) { return rules, nil }); err != nil {
		t.Fatalf("UpdateTemplateRules: %v", err)
	}
}

func setTemplateTunnels(t *testing.T, scope string, tunnels []Tunnel) {
	t.Helper()
	if err := UpdateTemplateTunnels(scope, func([]Tunnel) ([]Tunnel, error) { return tunnels, nil }); err != nil {
		t.Fatalf("UpdateTemplateTunnels: %v", err)
	}
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}
	return out
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// TestSaveSettingsIgnoresResourceFields 锁定拆分后的核心契约：
// 「整份配置覆盖写」接口看不到规则与隧道——它们由各自的 store 持有，
// 因此请求体里的过期快照不可能再覆盖服务端（旧 AdoptServerOwnedFields 补丁的替代品）。
func TestSaveSettingsIgnoresResourceFields(t *testing.T) {
	setupTempDataDir(t)

	if err := SaveSettings(SubscribeConfig{
		ProxyPort: 7890,
		Subscriptions: []Subscription{{
			Name: "机场A", URL: "https://example.com/a",
		}},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// 服务端存下规则与隧道
	rule := CustomRule{ID: "r1", Type: "DOMAIN", Payload: "a.test", Target: "DIRECT", Position: RulePositionBefore}
	tunnel := Tunnel{ID: "t1", Network: []string{"tcp"}, Address: "127.0.0.1:1080", Target: "a.test:80", Proxy: "DIRECT"}
	setSubRules(t, "机场A", []CustomRule{rule})
	setSubTunnels(t, "机场A", []Tunnel{tunnel})
	setTemplateRules(t, RuleGroupBase, []CustomRule{rule})
	setTemplateTunnels(t, RuleScopeCustom, []Tunnel{tunnel})

	// 请求体带回一份过期快照：规则与隧道都被清空
	stale := SubscribeConfig{
		ProxyPort:         7890,
		Subscriptions:     []Subscription{{Name: "机场A", URL: "https://example.com/a"}},
		MergeCustomRules:  map[string][]CustomRule{RuleGroupBase: {}},
		CustomModeTunnels: nil,
	}
	if err := SaveSettings(stale); err != nil {
		t.Fatalf("SaveSettings(stale): %v", err)
	}

	Mu.RLock()
	defer Mu.RUnlock()
	if got := Current.MergeCustomRulesFor(RuleGroupBase); len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("融合档位规则被过期快照覆盖: %+v", got)
	}
	if got := Current.SubscriptionRulesFor("机场A"); len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("订阅级规则被过期快照覆盖: %+v", got)
	}
	if got := Current.SubscriptionTunnelsFor("机场A"); len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("订阅级隧道被过期快照覆盖: %+v", got)
	}
	if got := Current.TemplateTunnelsFor(RuleScopeCustom); len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("自定义模式隧道被过期快照覆盖: %+v", got)
	}
}

// TestSaveSettingsRenameMovesResources 订阅改名（URL 不变）时，其规则/隧道/元数据跟着搬家。
func TestSaveSettingsRenameMovesResources(t *testing.T) {
	setupTempDataDir(t)

	if err := SaveSettings(SubscribeConfig{
		Subscriptions: []Subscription{{Name: "旧名", URL: "https://example.com/a"}},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	rule := CustomRule{ID: "r1", Type: "DOMAIN", Payload: "a.test", Target: "DIRECT"}
	setSubRules(t, "旧名", []CustomRule{rule})
	if err := SaveSubscriptionMeta("旧名", "2026-01-02T03:04:05Z", map[string]any{"total": 100}); err != nil {
		t.Fatalf("SaveSubscriptionMeta: %v", err)
	}

	if err := SaveSettings(SubscribeConfig{
		Subscriptions: []Subscription{{Name: "新名", URL: "https://example.com/a"}},
	}); err != nil {
		t.Fatalf("SaveSettings(rename): %v", err)
	}

	if got := SubscriptionRules("新名"); len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("改名后规则未搬迁: %+v", got)
	}
	if got := SubscriptionRules("旧名"); got != nil {
		t.Fatalf("改名后旧名仍留有规则: %+v", got)
	}
	if updatedAt, _ := SubscriptionMetaOf("新名"); updatedAt == "" {
		t.Fatal("改名后元数据未搬迁")
	}
}

// TestSaveSettingsGarbageCollectsOrphans 订阅被删除后，其规则/隧道/元数据被回收。
func TestSaveSettingsGarbageCollectsOrphans(t *testing.T) {
	setupTempDataDir(t)

	if err := SaveSettings(SubscribeConfig{
		Subscriptions: []Subscription{{Name: "待删", URL: "https://example.com/a"}},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	setSubRules(t, "待删", []CustomRule{{ID: "r1"}})
	setSubTunnels(t, "待删", []Tunnel{{ID: "t1", Network: []string{"tcp"}}})
	if err := SaveSubscriptionMeta("待删", "now", map[string]any{"total": 1}); err != nil {
		t.Fatalf("SaveSubscriptionMeta: %v", err)
	}

	// 删掉订阅（URL 也随之不同，因此不会被误判为改名）
	if err := SaveSettings(SubscribeConfig{}); err != nil {
		t.Fatalf("SaveSettings(drop): %v", err)
	}

	rules := readJSON[RulesFile](t, FluxorRulesFile)
	tunnels := readJSON[TunnelsFile](t, FluxorTunnelsFile)
	meta := readJSON[MetaFile](t, FluxorMetaFile)
	if len(rules.BySub) != 0 || len(tunnels.BySub) != 0 || len(meta.Subscriptions) != 0 {
		t.Fatalf("孤儿资源未被回收: rules=%v tunnels=%v meta=%v", rules.BySub, tunnels.BySub, meta.Subscriptions)
	}
}

// TestMetaWriteIsIsolated 元数据写入只碰 subscription-meta.json。
//
// 这是拆分最直接的收益：定时更新（可配置间隔 × N 个订阅）不再重写订阅注册表与规则。
func TestMetaWriteIsIsolated(t *testing.T) {
	dir := setupTempDataDir(t)

	if err := SaveSettings(SubscribeConfig{
		Subscriptions: []Subscription{{Name: "机场A", URL: "https://example.com/a"}},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	setSubRules(t, "机场A", []CustomRule{{ID: "r1"}})

	// 只比对「确实已生成的」文件：未写过的文件按需生成，本就是拆分后的预期行为
	before := map[string]string{}
	for _, name := range []string{"settings.json", "rules.json", "tunnels.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		before[name] = string(data)
	}
	if _, ok := before["settings.json"]; !ok {
		t.Fatal("settings.json 应已生成")
	}

	if err := SaveSubscriptionMeta("机场A", "2026-01-02T03:04:05Z", map[string]any{"total": 42}); err != nil {
		t.Fatalf("SaveSubscriptionMeta: %v", err)
	}

	for name, want := range before {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		if string(data) != want {
			t.Errorf("元数据写入不该改动 %s", name)
		}
	}
	if _, ok := readJSON[MetaFile](t, FluxorMetaFile).Subscriptions["机场A"]; !ok {
		t.Fatal("元数据未落盘")
	}
	// 视图层应立刻可见新元数据
	Mu.RLock()
	defer Mu.RUnlock()
	if got := Current.Subscriptions[0].SubscriptionInfo["total"]; got != float64(42) && got != 42 {
		t.Fatalf("Current 视图未同步元数据: %v", got)
	}
}

// TestDefaultsDoNotLeakIntoFiles 只读默认值不落盘：空配置启动后不应生成任何配置文件。
func TestDefaultsDoNotLeakIntoFiles(t *testing.T) {
	dir := setupTempDataDir(t)

	Mu.RLock()
	proxyPort := Current.ProxyPort
	Mu.RUnlock()
	if proxyPort != defaultProxyPort {
		t.Fatalf("默认 proxy_port = %d, want %d", proxyPort, defaultProxyPort)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("未修改配置却写入了文件: %s", e.Name())
		}
	}
}

// TestConcurrentWritesAcrossStores 跨 store 并发写入不丢数据：每个文件由自己的锁串行化。
func TestConcurrentWritesAcrossStores(t *testing.T) {
	setupTempDataDir(t)

	if err := SaveSettings(SubscribeConfig{
		Subscriptions: []Subscription{{Name: "机场A", URL: "https://example.com/a"}},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			rules := make([]CustomRule, 0, i+1)
			for j := 0; j <= i; j++ {
				rules = append(rules, CustomRule{ID: "r", Payload: strings.Repeat("x", j+1)})
			}
			if err := UpdateSubscriptionRules("机场A", func([]CustomRule) ([]CustomRule, error) {
				return rules, nil
			}); err != nil {
				t.Errorf("UpdateSubscriptionRules: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := SaveSubscriptionMeta("机场A", "t", map[string]any{"i": i}); err != nil {
				t.Errorf("SaveSubscriptionMeta: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := SaveSettings(SubscribeConfig{
				ProxyPort:     defaultProxyPort + i%3,
				Subscriptions: []Subscription{{Name: "机场A", URL: "https://example.com/a"}},
			}); err != nil {
				t.Errorf("SaveSettings: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	// 三个文件都应可解析，且注册表未被规则/元数据写入破坏
	settings := readJSON[Settings](t, FluxorSettingsFile)
	if len(settings.Subscriptions) != 1 || settings.Subscriptions[0].URL != "https://example.com/a" {
		t.Fatalf("settings.json 被并发写入破坏: %+v", settings.Subscriptions)
	}
	if got := len(readJSON[RulesFile](t, FluxorRulesFile).BySub["机场A"]); got != n {
		t.Fatalf("rules.json 规则数 = %d, want %d", got, n)
	}
	if _, ok := readJSON[MetaFile](t, FluxorMetaFile).Subscriptions["机场A"]; !ok {
		t.Fatal("subscription-meta.json 被并发写入破坏")
	}
}

// TestStoreCorruptFileRefusesWrite 损坏的文件必须「备份 + 拒绝写入」，而不是以空值继续。
//
// 这是拆分要解决的核心问题之一：旧实现解析失败时以空 map 为基底继续写，会把**别人的
// 字段**一并清空（例如保存订阅配置时清掉 TProxy 绕过列表）。损坏必须只影响它自己。
func TestStoreCorruptFileRefusesWrite(t *testing.T) {
	dir := setupTempDataDir(t)

	// 造一个损坏的 rules.json（截断的 JSON）
	broken := `{"merge": {"base": [`
	if err := os.WriteFile(FluxorRulesFile, []byte(broken), 0644); err != nil {
		t.Fatalf("写入损坏文件失败: %v", err)
	}

	if err := rulesStore.Load(); err == nil {
		t.Fatal("损坏文件应当返回错误")
	}
	if err := UpdateSubscriptionRules("机场A", func([]CustomRule) ([]CustomRule, error) {
		return []CustomRule{{ID: "r1"}}, nil
	}); err == nil {
		t.Fatal("损坏状态下必须拒绝写入")
	}

	// 原始内容被完整备份，可人工恢复
	backups, err := filepath.Glob(FluxorRulesFile + ".corrupt-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("应生成一个备份文件，实际 %v (err=%v)", backups, err)
	}
	data, err := os.ReadFile(backups[0])
	if err != nil || string(data) != broken {
		t.Fatalf("备份内容与原始内容不一致: %q (err=%v)", string(data), err)
	}

	// 删除损坏文件后重新载入即可恢复写入
	if err := os.Remove(FluxorRulesFile); err != nil {
		t.Fatalf("删除损坏文件失败: %v", err)
	}
	if err := rulesStore.Load(); err != nil {
		t.Fatalf("清理后应能正常载入: %v", err)
	}
	if err := UpdateSubscriptionRules("机场A", func([]CustomRule) ([]CustomRule, error) { return []CustomRule{{ID: "r1"}}, nil }); err != nil {
		t.Fatalf("清理后应能正常写入: %v", err)
	}
	_ = dir
}

// TestStoreAtomicWriteLeavesNoTempFiles 原子写盘不应留下临时文件。
func TestStoreAtomicWriteLeavesNoTempFiles(t *testing.T) {
	dir := setupTempDataDir(t)

	if err := SaveSettings(SubscribeConfig{ProxyPort: 1234}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
	// 文件权限必须可读（CreateTemp 默认 0600，rename 前需 chmod）
	info, err := os.Stat(FluxorSettingsFile)
	if err != nil {
		t.Fatalf("stat 失败: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Errorf("文件权限 = %o, want 0644", perm)
	}
}

// TestSettingsPortsPresenceSemantics 端口 0 表示禁用，不能被默认值覆盖。
func TestSettingsPortsPresenceSemantics(t *testing.T) {
	dir := t.TempDir()
	SetDataDir(dir)
	defer capturePaths()()

	// 显式写 0（禁用）与缺失（默认值）必须区分开
	writeJSON(t, FluxorSettingsFile, map[string]any{"proxy_port": 0})
	LoadAll()

	Mu.RLock()
	proxyPort, panelPort := Current.ProxyPort, Current.PanelPort
	Mu.RUnlock()
	if proxyPort != 0 {
		t.Fatalf("显式写 0 应保留为禁用，实际 %d", proxyPort)
	}
	if panelPort != defaultPanelPort {
		t.Fatalf("缺失的端口应取默认值，实际 %d", panelPort)
	}
}

// TestSettingsViewDoesNotPersistDerivedFields settings.json 不应包含派生字段（规则/隧道/元数据）。
func TestSettingsViewDoesNotPersistDerivedFields(t *testing.T) {
	setupTempDataDir(t)

	if err := SaveSettings(SubscribeConfig{
		Subscriptions: []Subscription{{
			Name: "机场A", URL: "https://example.com/a",
			CustomRules:      []CustomRule{{ID: "sr1"}},
			Tunnels:          []Tunnel{{ID: "st1"}},
			UpdatedAt:        "2026-01-01T00:00:00Z",
			SubscriptionInfo: map[string]any{"total": 1},
		}},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	data, err := os.ReadFile(FluxorSettingsFile)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	for _, key := range []string{"custom_rules", "tunnels", "updated_at", "subscription_info"} {
		if strings.Contains(string(data), key) {
			t.Errorf("settings.json 不应包含派生字段 %q: %s", key, string(data))
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	subs, _ := raw["subscriptions"].([]any)
	if len(subs) != 1 {
		t.Fatalf("订阅注册表异常: %v", raw["subscriptions"])
	}
}

// TestEmptyMetaKeepsPrevious 空元数据既不写也不删：保留上一次已知的流量/到期。
//
// 触发场景（实测过）：切换到融合模式后内核尚未加载/拉取这些 provider，或机场不下发
// subscription-userinfo——此时抓取会「成功但内容为空」。若把它当作清空，卡片上原本
// 用缓存兜着的流量与到期会一起被抹掉，而这正是最该显示上次已知值的时刻。
func TestEmptyMetaKeepsPrevious(t *testing.T) {
	setupTempDataDir(t)

	if err := SaveSettings(SubscribeConfig{
		Subscriptions: []Subscription{{Name: "机场A", URL: "https://example.com/a"}},
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if err := SaveSubscriptionMeta("机场A", "2026-01-01T00:00:00Z", map[string]any{"total": 42}); err != nil {
		t.Fatalf("SaveSubscriptionMeta: %v", err)
	}

	// 两种空形态：完全为空 / 空 map（解析出 0 个字段）
	if err := SaveSubscriptionMeta("机场A", "", nil); err != nil {
		t.Fatalf("SaveSubscriptionMeta(empty): %v", err)
	}
	if err := SaveSubscriptionMeta("机场A", "", map[string]any{}); err != nil {
		t.Fatalf("SaveSubscriptionMeta(empty map): %v", err)
	}

	updatedAt, info := SubscriptionMetaOf("机场A")
	if updatedAt != "2026-01-01T00:00:00Z" || info["total"] != 42 {
		t.Fatalf("空元数据不应覆盖旧值: updatedAt=%q info=%v", updatedAt, info)
	}
	if _, ok := readJSON[MetaFile](t, FluxorMetaFile).Subscriptions["机场A"]; !ok {
		t.Fatal("空元数据不应删除条目")
	}

	// 有内容时正常覆盖
	if err := SaveSubscriptionMeta("机场A", "2026-02-02T00:00:00Z", map[string]any{"total": 99}); err != nil {
		t.Fatalf("SaveSubscriptionMeta: %v", err)
	}
	if updatedAt, info := SubscriptionMetaOf("机场A"); updatedAt != "2026-02-02T00:00:00Z" || info["total"] != 99 {
		t.Fatalf("有效元数据应正常覆盖: updatedAt=%q info=%v", updatedAt, info)
	}
}
