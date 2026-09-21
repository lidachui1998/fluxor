package configgen

import (
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// regionGroups 从 proxy-groups 节点取出「带 filter 地区筛选」的组，
// 返回 name -> 该组是否声明了 include-all-providers。
//
// 带 filter 的组必须显式声明节点来源（use / include-all-providers / proxies），
// 否则内核实测直接拒绝加载整份配置：`use` or `proxies` missing。
func regionGroups(t *testing.T, groups *yaml.Node) map[string]bool {
	t.Helper()
	if groups == nil || groups.Kind != yaml.SequenceNode {
		t.Fatalf("proxy-groups 应为序列，实际 %+v", groups)
	}
	out := make(map[string]bool)
	for _, group := range groups.Content {
		if group.Kind != yaml.MappingNode {
			continue
		}
		var name string
		hasFilter, hasAllProviders := false, false
		for i := 0; i+1 < len(group.Content); i += 2 {
			switch group.Content[i].Value {
			case "name":
				name = group.Content[i+1].Value
			case "filter":
				hasFilter = true
			case "include-all-providers":
				hasAllProviders = group.Content[i+1].Value == "true"
			case "use":
				t.Fatalf("组 %s 仍带 use 列表：full 档位应统一用 include-all-providers", name)
			}
		}
		if !hasFilter {
			continue
		}
		if name == "" {
			t.Fatalf("带 filter 的组缺少 name")
		}
		out[name] = hasAllProviders
	}
	return out
}

// TestGenerateConfigFullGroupsUseAllProviders 端到端：产物可被校验，
// 地区组以 include-all-providers 引用全部订阅 provider，且不再出现订阅名。
func TestGenerateConfigFullGroupsUseAllProviders(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/config.yaml"
	oldTarget := config.ConfigTarget
	config.ConfigTarget = target
	defer func() { config.ConfigTarget = oldTarget }()

	// 含 YAML 特殊字符与 emoji 的名称：新写法下订阅名只作为 proxy-providers 的键出现，
	// 绝不会进入代理组，因此这类名称不再有任何破坏语法的可能。
	names := []string{"机场A", "a,b]c#d: e", "🇭🇰港x"}
	subs := make([]config.Subscription, 0, len(names))
	for _, n := range names {
		subs = append(subs, config.Subscription{Name: n})
	}
	cfg := config.SubscribeConfig{
		RuleGroup:     config.RuleGroupFull,
		ProxyPort:     7890,
		TproxyPort:    7895,
		PanelPort:     9090,
		PanelSecret:   "s3cret",
		UIPanel:       "zashboard",
		Subscriptions: subs,
	}
	if err := GenerateConfig(cfg); err != nil {
		t.Fatalf("生成失败: %v", err)
	}

	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	if err := configcheck.ValidateClashConfig(content); err != nil {
		t.Fatalf("产物不是合法 Clash 配置: %v", err)
	}

	doc, err := configcheck.ParseDoc(content)
	if err != nil {
		t.Fatalf("解析产物失败: %v", err)
	}
	providers := doc.MappingKeys("proxy-providers")
	if strings.Join(providers, "|") != strings.Join(names, "|") {
		t.Fatalf("proxy-providers 键错误:\n实际: %v\n期望: %v", providers, names)
	}

	groups := regionGroups(t, doc.Get("proxy-groups"))
	if len(groups) != 5 {
		t.Fatalf("期望 5 个带 filter 的地区组，实际 %d 个: %v", len(groups), groups)
	}
	for name, hasAllProviders := range groups {
		if !hasAllProviders {
			t.Fatalf("地区组 %s 未声明 include-all-providers：内核会以 `use` or `proxies` missing 拒绝加载", name)
		}
	}

	// 代理组里不应再出现任何订阅名（它们只属于 proxy-providers 的键）
	text := string(content)
	if strings.Contains(text, "use:") {
		t.Fatalf("产物中不应再出现 use 列表:\n%s", tailLines(text, 12))
	}
}

// TestGenerateConfigWithoutSubscriptionsUsesBaseConfig 无订阅时退化为基础配置，
// 不得产出既无 use 也无 proxies 的地区组。
func TestGenerateConfigWithoutSubscriptionsUsesBaseConfig(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/config.yaml"
	oldTarget := config.ConfigTarget
	config.ConfigTarget = target
	defer func() { config.ConfigTarget = oldTarget }()

	cfg := config.SubscribeConfig{
		RuleGroup:  config.RuleGroupFull,
		ProxyPort:  7890,
		TproxyPort: 7895,
		PanelPort:  9090,
	}
	if err := GenerateConfig(cfg); err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	for _, banned := range []string{"proxy-groups", "proxy-providers", "include-all-providers"} {
		if strings.Contains(string(content), banned) {
			t.Fatalf("无订阅的基础配置不应包含 %s:\n%s", banned, tailLines(string(content), 12))
		}
	}
}

// TestAppendRuleSetFullRequiresSubscriptions 兜底：full 档位在无订阅时直接报错，
// 而不是产出一份内核加载不了的配置。
func TestAppendRuleSetFullRequiresSubscriptions(t *testing.T) {
	doc := configcheck.NewDoc()
	err := appendRuleSet(doc, config.SubscribeConfig{RuleGroup: config.RuleGroupFull})
	if err == nil {
		t.Fatalf("无订阅时应报错")
	}
	if !strings.Contains(err.Error(), "订阅") {
		t.Fatalf("错误信息应说明缺少订阅，实际: %v", err)
	}
}

// TestMergeRuleSetEnvIncludesRegionGroups full 档位的自定义规则可选目标仍含全部地区组。
//
// 地区组的引用方式从 use 列表换成 include-all-providers 后，组名读取路径必须不变，
// 否则前端下拉与后端校验会一起丢掉这些目标。
func TestMergeRuleSetEnvIncludesRegionGroups(t *testing.T) {
	env, err := MergeRuleSetEnv(RuleGroupFull)
	if err != nil {
		t.Fatalf("读取规则环境失败: %v", err)
	}
	for _, name := range []string{
		"🇭🇰 香港节点", "🇹🇼 台湾节点", "🇯🇵 日本节点",
		"🇸🇬 新加坡节点", "🇺🇸 美国节点", "🚀 节点选择",
	} {
		if _, ok := env.Targets[name]; !ok {
			t.Fatalf("可选目标缺少代理组 %s", name)
		}
	}
}

// tailLines 返回文本末尾 n 行，便于在断言失败时打印关键片段。
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
