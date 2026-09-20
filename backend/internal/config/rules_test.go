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
