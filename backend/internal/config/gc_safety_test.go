package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 本文件守住 P0 级的一条不变量：
//
//	settings.json 不可信时，**绝不能**以它为判据去删除其它文件里的数据。
//
// 回归背景：LoadAll 原先无条件调用 GCResources()。settings.json 解析失败时内存态会
// 回落为默认值（订阅表为空），于是 GC 的 keep 集合为空，rules.json / tunnels.json /
// subscription-meta.json 里所有按订阅名存放的条目都会被判成孤儿并**落盘删除**——
// 用户全部自定义规则、流量隧道与机场流量/到期元数据一次性消失，而这三份文件没有
// .corrupt 备份。这直接违背了「损坏的影响面仅限该文件自身」的设计承诺。

// writeFileRaw 写入一个文件（测试里直接落原始内容，便于构造损坏场景）。
func writeFileRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// useTempDataDir 把运行数据目录指到临时目录（并在结束时复位）。
func useTempDataDir(t *testing.T) string {
	t.Helper()
	prev := FluxorDataDir
	dir := t.TempDir()
	SetDataDir(dir)
	t.Cleanup(func() { SetDataDir(prev) })
	return dir
}

// TestCorruptSettingsDoesNotWipeOtherFiles settings.json 损坏时必须跳过孤儿的实际删除。
func TestCorruptSettingsDoesNotWipeOtherFiles(t *testing.T) {
	useTempDataDir(t)

	// 一份「看似有订阅、但整体不是合法 JSON」的 settings.json
	writeFileRaw(t, FluxorSettingsFile, `{"mode":"switch","subscriptions":[{"name":"机场A",`)

	// 规则 / 隧道 / 元数据都有内容，键为订阅名「机场A」
	rulesJSON, _ := json.Marshal(RulesFile{
		BySub: map[string][]CustomRule{
			"机场A": {{ID: "r1", Type: "DOMAIN-SUFFIX", Payload: "keep.example", Target: "DIRECT", Position: RulePositionBefore}},
		},
	})
	writeFileRaw(t, FluxorRulesFile, string(rulesJSON))

	tunnelsJSON, _ := json.Marshal(TunnelsFile{
		BySub: map[string][]Tunnel{
			"机场A": {{ID: "t1", Network: []string{TunnelNetworkTCP}, Address: "127.0.0.1:17001", Target: "1.1.1.1:53"}},
		},
	})
	writeFileRaw(t, FluxorTunnelsFile, string(tunnelsJSON))

	metaJSON, _ := json.Marshal(MetaFile{
		Subscriptions: map[string]SubscriptionMeta{
			"机场A": {UpdatedAt: "2026-01-01T00:00:00Z", Info: map[string]any{"upload": 1}},
		},
	})
	writeFileRaw(t, FluxorMetaFile, string(metaJSON))

	LoadAll()

	// settings 确实被判为损坏（这是后续跳过的前提）
	if !settingsStore.Damaged() {
		t.Fatal("非法 JSON 的 settings.json 应被判为损坏")
	}

	// 三份文件的内容必须原封不动
	var rules RulesFile
	if err := json.Unmarshal([]byte(readFileRaw(t, FluxorRulesFile)), &rules); err != nil {
		t.Fatalf("rules.json 已被破坏: %v", err)
	}
	if len(rules.BySub["机场A"]) != 1 {
		t.Fatalf("rules.json 里的规则被误删了: %+v", rules.BySub)
	}

	var tunnels TunnelsFile
	if err := json.Unmarshal([]byte(readFileRaw(t, FluxorTunnelsFile)), &tunnels); err != nil {
		t.Fatalf("tunnels.json 已被破坏: %v", err)
	}
	if len(tunnels.BySub["机场A"]) != 1 {
		t.Fatalf("tunnels.json 里的隧道被误删了: %+v", tunnels.BySub)
	}

	var meta MetaFile
	if err := json.Unmarshal([]byte(readFileRaw(t, FluxorMetaFile)), &meta); err != nil {
		t.Fatalf("subscription-meta.json 已被破坏: %v", err)
	}
	if _, ok := meta.Subscriptions["机场A"]; !ok {
		t.Fatalf("subscription-meta.json 里的元数据被误删了: %+v", meta.Subscriptions)
	}

	// 损坏的 settings.json 必须留下备份，且原文件仍然存在（没有被默认值覆盖）
	if _, err := os.Stat(FluxorSettingsFile); err != nil {
		t.Fatalf("损坏的 settings.json 不应被删除/覆盖: %v", err)
	}
	matches, _ := filepath.Glob(FluxorSettingsFile + ".corrupt-*")
	if len(matches) == 0 {
		t.Fatal("损坏的 settings.json 应留下 .corrupt-* 备份")
	}
}

// readFileRaw 读取文件内容（失败直接终止用例）。
func readFileRaw(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(data)
}

// TestReadFailureAlsoBlocksWrites 读盘失败（非 NotExist）同样必须拒绝后续写入。
//
// 否则内存态已回落默认值、磁盘上的真实配置还在，下一次保存就会用默认值把它覆盖掉——
// 没有报错、没有备份，只是某次重启后配置变成了初始状态。
func TestReadFailureAlsoBlocksWrites(t *testing.T) {
	dir := useTempDataDir(t)

	// 让 settings.json 成为一个目录：os.ReadFile 会返回非 NotExist 的错误
	settingsDir := filepath.Join(dir, "settings.json")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}

	if err := settingsStore.Load(); err == nil {
		t.Fatal("读盘失败应返回错误")
	}
	if !settingsStore.Damaged() {
		t.Fatal("读盘失败也应把 store 标为不可信（否则后续写入会覆盖磁盘）")
	}
	err := settingsStore.Update(func(*Settings) error { return nil })
	if err == nil {
		t.Fatal("不可信的 store 必须拒绝写入")
	}
}

// TestGCResourcesStillCollectsOrphansWithHealthySettings 正常路径不能被削弱：
// settings 可信时，SaveSettings 仍然要回收真正的孤儿。
func TestGCResourcesStillCollectsOrphansWithHealthySettings(t *testing.T) {
	useTempDataDir(t)
	LoadAll()

	// 先写一份「属于机场A」的规则，再让注册表里只剩机场B → 机场A 的规则成为孤儿
	if err := SaveSettings(SubscribeConfig{
		ProxyPort: 7890, PanelPort: 9090, TproxyPort: 7898,
		Mode: ModeMerge, RuleGroup: RuleGroupBase, UIPanel: "metacubexd",
		Subscriptions: []Subscription{{Name: "机场A", URL: "https://example.invalid/a"}},
	}); err != nil {
		t.Fatalf("播种失败: %v", err)
	}
	if err := UpdateSubscriptionRules("机场A", func([]CustomRule) ([]CustomRule, error) {
		return []CustomRule{{ID: "r1", Type: "DOMAIN-SUFFIX", Payload: "x.example", Target: "DIRECT"}}, nil
	}); err != nil {
		t.Fatalf("写规则失败: %v", err)
	}

	if err := SaveSettings(SubscribeConfig{
		ProxyPort: 7890, PanelPort: 9090, TproxyPort: 7898,
		Mode: ModeMerge, RuleGroup: RuleGroupBase, UIPanel: "metacubexd",
		Subscriptions: []Subscription{{Name: "机场B", URL: "https://example.invalid/b"}},
	}); err != nil {
		t.Fatalf("改名失败: %v", err)
	}

	if got := SubscriptionRules("机场A"); len(got) != 0 {
		t.Fatalf("删除订阅后其规则应被回收，实际残留 %d 条", len(got))
	}
}

// TestCurrentSnapshotSafeUnderConcurrentSaves CurrentSnapshot 必须持锁，
// 与并发的 SaveSettings（内部会整体替换 Current）一起使用时不得出现数据竞争。
// 配合 -race 使用。
func TestCurrentSnapshotSafeUnderConcurrentSaves(t *testing.T) {
	useTempDataDir(t)
	LoadAll()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = SaveSettings(SubscribeConfig{
				ProxyPort: 7890, PanelPort: 9090, TproxyPort: 7898,
				Mode: ModeMerge, RuleGroup: RuleGroupBase, UIPanel: "metacubexd",
				Subscriptions: []Subscription{{Name: "订阅A", URL: "https://example.invalid/sub"}},
			})
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := CurrentSnapshot()
				// 真实读取方都会遍历这两个字段，这里模拟同样的访问模式
				for range snap.Subscriptions {
				}
				_ = snap.Mode
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}
