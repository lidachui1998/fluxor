package subscription

import (
	"fluxor/internal/config"
	"testing"
)

// withCurrent 临时替换全局配置并保证测试结束后还原。
func withCurrent(t *testing.T, cfg config.SubscribeConfig) {
	t.Helper()
	config.Mu.Lock()
	prev := config.Current
	config.Current = cfg
	config.Mu.Unlock()
	t.Cleanup(func() {
		config.Mu.Lock()
		config.Current = prev
		config.Mu.Unlock()
	})
}

// TestRuleScopeResolution 三种模板级作用域的解析：档位只认 base/full，自定义模式单独一个作用域。
func TestRuleScopeResolution(t *testing.T) {
	for _, tier := range []string{config.RuleGroupBase, config.RuleGroupFull} {
		scope, ok := mergeRuleScope(tier)
		if !ok || scope.name != tier {
			t.Fatalf("档位 %s 应解析为融合作用域", tier)
		}
	}
	// 自定义模式不是融合档位：走融合入口应被拒（否则又会回到「两种模式共用一份列表」的老问题）
	if _, ok := mergeRuleScope(config.RuleScopeCustom); ok {
		t.Fatal("custom 不应被当成融合档位")
	}
	if _, ok := mergeRuleScope("nope"); ok {
		t.Fatal("未知档位应被拒")
	}
	if scope := customModeRuleScope(); scope.name != config.RuleScopeCustom {
		t.Fatalf("自定义作用域名应为 %s，实际 %s", config.RuleScopeCustom, scope.name)
	}
}

// TestRuleScopeEditable 作用域只在所属模式下可编辑——这是「两种模式互不影响」的第一道闸门。
func TestRuleScopeEditable(t *testing.T) {
	baseScope, _ := mergeRuleScope(config.RuleGroupBase)
	customScope := customModeRuleScope()

	cases := []struct {
		mode           string
		mergeEditable  bool
		customEditable bool
	}{
		{config.ModeMerge, true, false},
		{config.ModeCustom, false, true},
		{config.ModeSwitch, false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		cfg := config.SubscribeConfig{Mode: tc.mode}
		if got := baseScope.editable(cfg); got != tc.mergeEditable {
			t.Fatalf("模式 %q：融合档位可编辑应为 %v，实际 %v", tc.mode, tc.mergeEditable, got)
		}
		if got := customScope.editable(cfg); got != tc.customEditable {
			t.Fatalf("模式 %q：自定义作用域可编辑应为 %v，实际 %v", tc.mode, tc.customEditable, got)
		}
	}
}

// TestRuleScopeActive 生效判定：融合档位看当前 rule_group，自定义作用域在自定义模式下恒生效。
func TestRuleScopeActive(t *testing.T) {
	baseScope, _ := mergeRuleScope(config.RuleGroupBase)
	fullScope, _ := mergeRuleScope(config.RuleGroupFull)
	customScope := customModeRuleScope()

	cases := []struct {
		mode      string
		ruleGroup string
		base      bool
		full      bool
		custom    bool
	}{
		{config.ModeMerge, config.RuleGroupBase, true, false, false},
		{config.ModeMerge, config.RuleGroupFull, false, true, false},
		{config.ModeCustom, config.RuleGroupFull, false, false, true}, // 自定义模式无视残留档位
		{config.ModeSwitch, config.RuleGroupBase, false, false, false},
	}
	for _, tc := range cases {
		cfg := config.SubscribeConfig{Mode: tc.mode, RuleGroup: tc.ruleGroup}
		if got := baseScope.active(cfg); got != tc.base {
			t.Fatalf("模式 %q/%s：base 生效应为 %v，实际 %v", tc.mode, tc.ruleGroup, tc.base, got)
		}
		if got := fullScope.active(cfg); got != tc.full {
			t.Fatalf("模式 %q/%s：full 生效应为 %v，实际 %v", tc.mode, tc.ruleGroup, tc.full, got)
		}
		if got := customScope.active(cfg); got != tc.custom {
			t.Fatalf("模式 %q：自定义作用域生效应为 %v，实际 %v", tc.mode, tc.custom, got)
		}
	}
}

// TestRuleScopesAreIsolated 三类作用域的读写互不串门。
//
// 这是本次拆分的核心回归点：融合档位与自定义模式各自持有规则，写一边绝不能出现在另一边。
func TestRuleScopesAreIsolated(t *testing.T) {
	withCurrent(t, config.SubscribeConfig{})

	baseScope, _ := mergeRuleScope(config.RuleGroupBase)
	fullScope, _ := mergeRuleScope(config.RuleGroupFull)
	customScope := customModeRuleScope()

	baseScope.store([]config.CustomRule{{ID: "b1", Payload: "base.test"}})
	fullScope.store([]config.CustomRule{{ID: "f1", Payload: "full.test"}})
	customScope.store([]config.CustomRule{{ID: "c1", Payload: "custom.test"}})

	cfg := ruleConfigSnapshot()
	if got := len(baseScope.rules(cfg)); got != 1 || baseScope.rules(cfg)[0].ID != "b1" {
		t.Fatalf("base 档位规则异常: %+v", baseScope.rules(cfg))
	}
	if got := len(fullScope.rules(cfg)); got != 1 || fullScope.rules(cfg)[0].ID != "f1" {
		t.Fatalf("full 档位规则异常: %+v", fullScope.rules(cfg))
	}
	if got := len(customScope.rules(cfg)); got != 1 || customScope.rules(cfg)[0].ID != "c1" {
		t.Fatalf("自定义作用域规则异常: %+v", customScope.rules(cfg))
	}
	// 存放位置也必须是分开的：自定义规则不落在融合的 map 里
	if _, ok := cfg.MergeCustomRules[config.RuleScopeCustom]; ok {
		t.Fatal("自定义模式的规则不应写进 merge_custom_rules")
	}
	if len(cfg.MergeCustomRules[config.RuleGroupBase]) != 1 {
		t.Fatal("base 档位规则被写坏")
	}
}

// TestRuleScopeContexts 目标集合：融合档位只有模板代理组，自定义模式额外含手工节点。
func TestRuleScopeContexts(t *testing.T) {
	cfg := config.SubscribeConfig{
		CustomNodes: []config.CustomNode{{ID: "n1", Name: "自建节点"}},
	}

	baseScope, _ := mergeRuleScope(config.RuleGroupBase)
	baseCtx, err := baseScope.context(cfg)
	if err != nil {
		t.Fatalf("融合档位上下文失败: %v", err)
	}
	if baseCtx.Env.ContainsTarget("自建节点") || len(baseCtx.NodeNames()) != 0 {
		t.Fatal("融合档位的目标里不应有手工节点")
	}

	customCtx, err := customModeRuleScope().context(cfg)
	if err != nil {
		t.Fatalf("自定义作用域上下文失败: %v", err)
	}
	if !customCtx.Env.ContainsTarget("自建节点") {
		t.Fatal("自定义模式的目标里应包含手工节点")
	}
	if len(customCtx.NodeNames()) != 1 || customCtx.NodeNames()[0] != "自建节点" {
		t.Fatalf("自定义模式的节点列表异常: %+v", customCtx.NodeNames())
	}
	// 模板代理组与融合标准档位一致（自定义模式固定用标准规则集）
	baseGroups := baseCtx.GroupNames()
	customGroups := customCtx.GroupNames()
	if len(baseGroups) != len(customGroups) {
		t.Fatalf("两种作用域的模板代理组应一致: %v vs %v", baseGroups, customGroups)
	}
}
