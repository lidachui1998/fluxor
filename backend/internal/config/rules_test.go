package config

import "testing"

// ruleAt 生成一条用于排序测试的规则（只需 id/position 参与逻辑）。
func ruleAt(id, position string) CustomRule {
	return CustomRule{ID: id, Type: "DOMAIN", Payload: id, Target: "G", Position: position}
}

// idsOf 提取规则 ID 序列，便于断言顺序。
func idsOf(rules []CustomRule) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.ID)
	}
	return out
}

func assertOrder(t *testing.T, got []CustomRule, want ...string) {
	t.Helper()
	actual := idsOf(got)
	if len(actual) != len(want) {
		t.Fatalf("顺序长度不符: 实际 %v，期望 %v", actual, want)
	}
	for i := range want {
		if actual[i] != want[i] {
			t.Fatalf("顺序不符: 实际 %v，期望 %v", actual, want)
		}
	}
}

// TestNormalizeRulePosition 归一化：默认最前，after 需要显式指定且大小写不敏感。
func TestNormalizeRulePosition(t *testing.T) {
	cases := map[string]string{
		"":        RulePositionBefore,
		"   ":     RulePositionBefore,
		"before":  RulePositionBefore,
		"after":   RulePositionAfter,
		" After ": RulePositionAfter,
		"AFTER":   RulePositionAfter,
		"bogus":   RulePositionBefore,
	}
	for input, want := range cases {
		if got := NormalizeRulePosition(input); got != want {
			t.Fatalf("NormalizeRulePosition(%q) = %q，期望 %q", input, got, want)
		}
	}
}

// TestMoveCustomRuleWithinBucket 同分组内上/下移动。
func TestMoveCustomRuleWithinBucket(t *testing.T) {
	rules := []CustomRule{
		ruleAt("a", RulePositionBefore),
		ruleAt("b", RulePositionBefore),
		ruleAt("c", RulePositionBefore),
	}

	moved, ok := MoveCustomRule(rules, "c", RuleMoveUp)
	if !ok {
		t.Fatal("c 应能上移")
	}
	assertOrder(t, moved, "a", "c", "b")

	moved, ok = MoveCustomRule(moved, "c", RuleMoveDown)
	if !ok {
		t.Fatal("c 应能下移回原位")
	}
	assertOrder(t, moved, "a", "b", "c")
}

// TestMoveCustomRuleBoundary 分组边界不再移动，且不得越组交换。
func TestMoveCustomRuleBoundary(t *testing.T) {
	rules := []CustomRule{
		ruleAt("b1", RulePositionBefore),
		ruleAt("b2", RulePositionBefore),
		ruleAt("a1", RulePositionAfter),
		ruleAt("a2", RulePositionAfter),
	}

	// 首个 before 规则不能继续上移
	if _, ok := MoveCustomRule(rules, "b1", RuleMoveUp); ok {
		t.Fatal("已在分组最前的规则不应被移动")
	}
	// 最后一个 before 规则的下方是 after 组：不得跨组交换
	if _, ok := MoveCustomRule(rules, "b2", RuleMoveDown); ok {
		t.Fatal("不得与 after 组交换位置（会让界面顺序与生效顺序不一致）")
	}
	// after 组的边界同理
	if _, ok := MoveCustomRule(rules, "a1", RuleMoveUp); ok {
		t.Fatal("不得与 before 组交换位置")
	}
	if _, ok := MoveCustomRule(rules, "a2", RuleMoveDown); ok {
		t.Fatal("已在分组最后的规则不应被移动")
	}
}

// TestMoveCustomRuleSkipsOtherBucket before 组被 after 规则隔开时，仍与同组相邻规则交换。
func TestMoveCustomRuleSkipsOtherBucket(t *testing.T) {
	rules := []CustomRule{
		ruleAt("b1", RulePositionBefore),
		ruleAt("a1", RulePositionAfter),
		ruleAt("b2", RulePositionBefore),
	}
	moved, ok := MoveCustomRule(rules, "b2", RuleMoveUp)
	if !ok {
		t.Fatal("b2 应能与同组的 b1 交换")
	}
	assertOrder(t, moved, "b2", "a1", "b1")
}

// TestMoveCustomRuleInvalidInput 未知 id / 非法方向：不产生任何变化。
func TestMoveCustomRuleInvalidInput(t *testing.T) {
	rules := []CustomRule{ruleAt("a", RulePositionBefore), ruleAt("b", RulePositionBefore)}
	for _, tc := range []struct{ id, direction string }{
		{"nope", RuleMoveUp},
		{"a", "sideways"},
		{"a", ""},
	} {
		if moved, ok := MoveCustomRule(rules, tc.id, tc.direction); ok {
			t.Fatalf("id=%q direction=%q 不应产生变化", tc.id, tc.direction)
		} else {
			assertOrder(t, moved, "a", "b")
		}
	}
}

// TestMoveCustomRuleDoesNotMutateInput 返回新切片，调用方持有的快照不被就地改动。
func TestMoveCustomRuleDoesNotMutateInput(t *testing.T) {
	rules := []CustomRule{ruleAt("a", RulePositionBefore), ruleAt("b", RulePositionBefore)}
	if _, ok := MoveCustomRule(rules, "b", RuleMoveUp); !ok {
		t.Fatal("应能移动")
	}
	assertOrder(t, rules, "a", "b")
}

// TestSortCustomRulesForDisplay 展示顺序：before 组在前，各自保持原有相对顺序。
func TestSortCustomRulesForDisplay(t *testing.T) {
	rules := []CustomRule{
		ruleAt("a1", RulePositionAfter),
		ruleAt("b1", RulePositionBefore),
		ruleAt("a2", RulePositionAfter),
		ruleAt("b2", RulePositionBefore),
		ruleAt("legacy-unknown", ""), // 空值按默认最前处理
	}
	assertOrder(t, SortCustomRulesForDisplay(rules), "b1", "b2", "legacy-unknown", "a1", "a2")
}

// TestAdoptServerOwnedFields 规则字段以服务端为准：过期快照不得覆盖已删/已改的规则。
//
// 这是「规则字段归规则接口所有」的完整版：以前只在调用方**没带**该键时才沿用服务端，
// 于是「保存并应用」提交的前端快照（规则弹窗改过之后就是过期的）会把删掉的规则写回来、
// 把新增的规则覆盖掉——实测复现过，因此改成一律取服务端状态。
func TestAdoptServerOwnedFields(t *testing.T) {
	prev := SubscribeConfig{
		MergeCustomRules: map[string][]CustomRule{
			RuleGroupBase: {{ID: "b1", Payload: "base.test"}},
			RuleGroupFull: {},
		},
		CustomModeRules: []CustomRule{{ID: "c1", Payload: "custom.test"}},
		Subscriptions: []Subscription{
			{Name: "机场A", CustomRules: []CustomRule{{ID: "s1", Payload: "sub.test"}}},
		},
	}

	stale := CustomRule{ID: "stale", Payload: "deleted.test"}
	// 请求体来自过期快照：三个作用域都塞了一条服务端已经没有的规则
	dst := SubscribeConfig{
		MergeCustomRules: map[string][]CustomRule{
			RuleGroupBase: {{ID: "stale", Payload: "deleted.test"}},
			RuleGroupFull: {{ID: "stale", Payload: "deleted.test"}},
		},
		CustomModeRules: []CustomRule{stale},
		Subscriptions: []Subscription{
			{Name: "机场A", CustomRules: []CustomRule{stale}},                              // 旧规则复活
			{Name: "机场B", CustomRules: []CustomRule{{ID: "new1", Payload: "kept.test"}}}, // 服务端没有 → 保留
		},
	}

	dst.AdoptServerOwnedFields(prev)

	if got := dst.MergeCustomRules[RuleGroupBase]; len(got) != 1 || got[0].ID != "b1" {
		t.Fatalf("融合 base 档位应取服务端状态，实际: %+v", got)
	}
	if got := dst.MergeCustomRules[RuleGroupFull]; len(got) != 0 {
		t.Fatalf("融合 full 档位应取服务端状态（空），实际: %+v", got)
	}
	if len(dst.CustomModeRules) != 1 || dst.CustomModeRules[0].ID != "c1" {
		t.Fatalf("自定义模式应取服务端状态，实际: %+v", dst.CustomModeRules)
	}
	if got := dst.Subscriptions[0].CustomRules; len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("切换模式应按订阅名取服务端状态，实际: %+v", got)
	}
	if got := dst.Subscriptions[1].CustomRules; len(got) != 1 || got[0].ID != "new1" {
		t.Fatalf("服务端没有的新订阅应保留请求体里的规则，实际: %+v", got)
	}

	// 深拷贝：改继承结果不能影响上一份状态
	dst.CustomModeRules[0].Payload = "changed"
	if prev.CustomModeRules[0].Payload != "custom.test" {
		t.Fatal("取服务端状态时必须深拷贝，不能与全局状态共享底层数组")
	}
	dst.Subscriptions[0].CustomRules[0].Payload = "changed"
	if prev.Subscriptions[0].CustomRules[0].Payload != "sub.test" {
		t.Fatal("订阅规则同样要深拷贝")
	}
}

// TestAdoptServerOwnedFieldsEmptyServer 服务端本来就没有规则时，请求体也不能凭空造规则。
//
// 关键场景：用户删光规则后落盘会省略该键（omitempty），下次读到的是 nil；此时若「服务端为空
// 就采纳调用方」，过期快照里的规则同样会复活。
func TestAdoptServerOwnedFieldsEmptyServer(t *testing.T) {
	dst := SubscribeConfig{
		CustomModeRules:  []CustomRule{{ID: "stale"}},
		MergeCustomRules: map[string][]CustomRule{RuleGroupBase: {{ID: "stale"}}},
		Subscriptions:    []Subscription{{Name: "机场A", CustomRules: []CustomRule{{ID: "stale"}}}},
	}
	dst.AdoptServerOwnedFields(SubscribeConfig{Subscriptions: []Subscription{{Name: "机场A"}}})

	if dst.CustomModeRules != nil {
		t.Fatalf("服务端无规则时不应采纳请求体，实际: %+v", dst.CustomModeRules)
	}
	if dst.MergeCustomRules != nil {
		t.Fatalf("服务端无融合规则时不应采纳请求体，实际: %+v", dst.MergeCustomRules)
	}
	if got := dst.Subscriptions[0].CustomRules; got != nil {
		t.Fatalf("服务端该订阅无规则时不应采纳请求体，实际: %+v", got)
	}
}

// TestTemplateRulesFor 模板级作用域取规则：融合档位读 map，自定义模式读独立字段。
func TestTemplateRulesFor(t *testing.T) {
	cfg := SubscribeConfig{
		MergeCustomRules: map[string][]CustomRule{
			RuleGroupBase: {{ID: "b1"}},
			RuleGroupFull: {{ID: "f1"}},
		},
		CustomModeRules: []CustomRule{{ID: "c1"}},
	}
	cases := map[string]string{
		RuleGroupBase:   "b1",
		RuleGroupFull:   "f1",
		RuleScopeCustom: "c1",
	}
	for scope, want := range cases {
		rules := cfg.TemplateRulesFor(scope)
		if len(rules) != 1 || rules[0].ID != want {
			t.Fatalf("作用域 %s 应取到 %s，实际 %+v", scope, want, rules)
		}
	}
	if cfg.TemplateRulesFor("unknown") != nil {
		t.Fatal("未知作用域应返回 nil")
	}
	if !IsValidRuleScope(RuleScopeCustom) || !IsValidRuleScope(RuleGroupBase) || IsValidRuleScope("nope") {
		t.Fatal("IsValidRuleScope 判定不正确")
	}
}
