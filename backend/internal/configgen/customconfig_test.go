package configgen

import (
	"fluxor/internal/config"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGenerateCustomConfig 自定义模式产物：模板骨架 + dns + 标准规则集 + proxies，
// 不含 proxy-providers（节点与订阅无关）。
func TestGenerateCustomConfig(t *testing.T) {
	dir := t.TempDir()
	oldTarget := config.ConfigTarget
	config.ConfigTarget = dir + "/config.yaml"
	defer func() { config.ConfigTarget = oldTarget }()

	cfg := config.SubscribeConfig{
		ProxyPort:  7890,
		TproxyPort: 7898,
		PanelPort:  9090,
		RuleGroup:  "full", // 自定义模式应无视残留档位，固定用标准档位
		UIPanel:    "metacubexd",
		Mode:       config.ModeCustom,
		CustomNodes: []config.CustomNode{
			{
				ID:   "n1",
				Name: "香港 01",
				Type: "ss",
				Config: map[string]any{
					"server":   "1.2.3.4",
					"port":     8388,
					"password": "pw",
				},
			},
			{
				ID:   "n2",
				Name: "🇯🇵 东京 · WS",
				Type: "trojan",
				Config: map[string]any{
					"server":   "example.com",
					"port":     443,
					"password": "pw2",
					"udp":      true,
					"alpn":     []string{"h2", "http/1.1"},
				},
			},
		},
		// 三种作用域的规则各写一份：只有自定义模式那一份应该进产物
		MergeCustomRules: map[string][]config.CustomRule{
			config.RuleGroupBase: {{ID: "r1", Type: "DOMAIN-SUFFIX", Payload: "merge-base.test", Target: "🎯 全球直连", Position: "before"}},
			config.RuleGroupFull: {{ID: "r2", Type: "DOMAIN-SUFFIX", Payload: "merge-full.test", Target: "🎯 全球直连", Position: "before"}},
		},
		CustomModeRules: []config.CustomRule{
			{ID: "c1", Type: "DOMAIN-SUFFIX", Payload: "custom-mode.test", Target: "🎯 全球直连", Position: "before"},
		},
	}

	if err := GenerateCustomConfig(cfg); err != nil {
		t.Fatalf("生成失败: %v", err)
	}

	raw, err := os.ReadFile(config.ConfigTarget)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	out := string(raw)

	for _, want := range []string{"香港 01", "🇯🇵 东京 · WS", "custom-mode.test", "dns:", "rules:"} {
		if !strings.Contains(out, want) {
			t.Errorf("产物应包含 %q，实际:\n%s", want, out)
		}
	}
	// 融合模式的两份规则都不该出现：三种作用域互不影响
	for _, unwanted := range []string{"proxy-providers", "merge-base.test", "merge-full.test", "rule-providers"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("产物不应包含 %q，实际:\n%s", unwanted, out)
		}
	}

	var parsed struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("产物不是合法 YAML: %v", err)
	}
	if len(parsed.Proxies) != 2 {
		t.Fatalf("proxies 应有 2 个节点，实际 %d", len(parsed.Proxies))
	}
	first := parsed.Proxies[0]
	if first["name"] != "香港 01" || first["type"] != "ss" || first["cipher"] != "aes-256-gcm" {
		t.Fatalf("第一个节点内容不正确: %+v", first)
	}
	// 端口必须是整数（字符串会让内核按弱类型解码之外的路径失败）
	if _, ok := first["port"].(int); !ok {
		t.Fatalf("端口应为整数，实际 %T: %+v", first["port"], first["port"])
	}
	second := parsed.Proxies[1]
	if second["udp"] != true {
		t.Fatalf("布尔字段应保持布尔类型: %+v", second)
	}
	alpn, ok := second["alpn"].([]any)
	if !ok || len(alpn) != 2 {
		t.Fatalf("列表字段应下发为序列: %+v", second["alpn"])
	}
}

// TestGenerateCustomConfigNoNodes 无节点时仍生成可用配置（模板 + 标准规则集），
// 且不写入空的 proxies 块——内核实测接受只剩规则集与代理组的配置。
func TestGenerateCustomConfigNoNodes(t *testing.T) {
	dir := t.TempDir()
	oldTarget := config.ConfigTarget
	config.ConfigTarget = dir + "/config.yaml"
	defer func() { config.ConfigTarget = oldTarget }()

	if err := GenerateCustomConfig(config.SubscribeConfig{ProxyPort: 7890, PanelPort: 9090, Mode: config.ModeCustom}); err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	raw, err := os.ReadFile(config.ConfigTarget)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	// 代理组内部也有 proxies 键，这里只看顶层（行首无缩进）的 proxies 块
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "proxies:") {
			t.Fatalf("无节点时不应写入 proxies 块:\n%s", raw)
		}
	}
	if !strings.Contains(string(raw), "proxy-groups:") {
		t.Fatalf("应写入标准规则集的代理组:\n%s", raw)
	}
}

// TestGenerateCustomConfigUnknownProtocol 未知协议必须在写盘前报错，
// 而不是产出一份内核加载不了的配置。
func TestGenerateCustomConfigUnknownProtocol(t *testing.T) {
	dir := t.TempDir()
	oldTarget := config.ConfigTarget
	config.ConfigTarget = dir + "/config.yaml"
	defer func() { config.ConfigTarget = oldTarget }()

	cfg := config.SubscribeConfig{
		Mode:        config.ModeCustom,
		CustomNodes: []config.CustomNode{{ID: "n1", Name: "x", Type: "unknown-proto"}},
	}
	if err := GenerateCustomConfig(cfg); err == nil {
		t.Fatalf("应当报错")
	}
	if _, err := os.Stat(config.ConfigTarget); err == nil {
		t.Fatalf("失败时不应写入产物")
	}
}
