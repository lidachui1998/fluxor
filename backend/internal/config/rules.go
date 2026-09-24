package config

// 自定义规则的排序方向。
const (
	// RuleMoveUp 向列表前部移动一位。
	RuleMoveUp = "up"
	// RuleMoveDown 向列表后部移动一位。
	RuleMoveDown = "down"
)

// MoveCustomRule 在同插入位置分组内上移/下移一条规则。
//
// 返回新切片与是否发生了移动。已在分组边界（该方向没有同组相邻规则）时
// 原样返回且 moved=false，由调用方决定如何提示——不做跨分组交换：
// before 与 after 的规则在 config.yaml 中落点完全不同（最前 vs MATCH 之前），
// 让它们互换位置只会让「界面顺序」与「实际生效顺序」不一致。
//
// 移动的是切片中的元素，因此同组规则的相对顺序即最终生效顺序。
func MoveCustomRule(rules []CustomRule, id, direction string) ([]CustomRule, bool) {
	idx := -1
	for i := range rules {
		if rules[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return rules, false
	}

	// 定位同组相邻规则：before 组向前找，after 组向后找
	bucket := NormalizeRulePosition(rules[idx].Position)
	neighbor := -1
	switch direction {
	case RuleMoveUp:
		for i := idx - 1; i >= 0; i-- {
			if NormalizeRulePosition(rules[i].Position) == bucket {
				neighbor = i
				break
			}
		}
	case RuleMoveDown:
		for i := idx + 1; i < len(rules); i++ {
			if NormalizeRulePosition(rules[i].Position) == bucket {
				neighbor = i
				break
			}
		}
	default:
		return rules, false
	}
	if neighbor < 0 {
		return rules, false
	}

	// 复制后交换：调用方持有的旧切片（如请求级快照）不应被就地改动
	out := make([]CustomRule, len(rules))
	copy(out, rules)
	out[idx], out[neighbor] = out[neighbor], out[idx]
	return out, true
}

// SortCustomRulesForDisplay 按「先 before 组、后 after 组」重排规则，供前端展示。
//
// 展示顺序与内核中的生效顺序一致：before 组按列表顺序插在规则最前，
// after 组按列表顺序插在最后一条 MATCH 之前。
func SortCustomRulesForDisplay(rules []CustomRule) []CustomRule {
	out := make([]CustomRule, 0, len(rules))
	for _, rule := range rules {
		if NormalizeRulePosition(rule.Position) == RulePositionBefore {
			out = append(out, rule)
		}
	}
	for _, rule := range rules {
		if NormalizeRulePosition(rule.Position) == RulePositionAfter {
			out = append(out, rule)
		}
	}
	return out
}

// copyRules 深拷贝规则切片：nil 保持 nil（避免把「没有规则」写成显式空切片），
// 其余情况返回独立底层数组，防止与全局状态共享后被就地改写。
func copyRules(rules []CustomRule) []CustomRule {
	if rules == nil {
		return nil
	}
	out := make([]CustomRule, len(rules))
	copy(out, rules)
	return out
}
