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

// InheritRuleOwnedFields 让 dst 继承 prev 中「由规则接口维护、调用方可能未携带」的字段。
//
// 背景：「保存并应用」（/subscribe/generate）会把请求体整体当作新配置写回
// config.Current，而请求体由前端拼装——只要它没有带上这些字段，配置生成就会
// 读到一份空规则集，产出不含自定义规则的 config.yaml；更糟的是内存态的规则被清空，
// 之后任何一次规则编辑都会把「只剩本次编辑」的列表写回文件，静默丢掉此前的规则。
//
// 归属规则接口的字段因此按「请求没带就沿用上一份状态」处理：
//   - 融合模式：整份 MergeCustomRules（按规则集档位）
//   - 切换模式：按订阅名逐条的 CustomRules
//
// 判据是「键是否出现」而不是「是否为空」：JSON 里没这个键 → nil → 继承；
// 显式传了 [] → 非 nil 空切片 → 尊重调用方（例如清空后的状态）。
//
// 继承时逐条复制切片，避免把 prev 的底层数组交给新配置共享——规则接口会用写锁
// 就地改动这些切片，共享底层数组会构成数据竞争。
func (c *SubscribeConfig) InheritRuleOwnedFields(prev SubscribeConfig) {
	// 自定义模式的自定义规则同理：请求体没带该键（nil）就沿用上一份状态
	if c.CustomModeRules == nil && prev.CustomModeRules != nil {
		copied := make([]CustomRule, len(prev.CustomModeRules))
		copy(copied, prev.CustomModeRules)
		c.CustomModeRules = copied
	}

	if c.MergeCustomRules == nil && prev.MergeCustomRules != nil {
		c.MergeCustomRules = make(map[string][]CustomRule, len(prev.MergeCustomRules))
		for group, rules := range prev.MergeCustomRules {
			if len(rules) == 0 {
				c.MergeCustomRules[group] = []CustomRule{}
				continue
			}
			copied := make([]CustomRule, len(rules))
			copy(copied, rules)
			c.MergeCustomRules[group] = copied
		}
	}

	for i := range c.Subscriptions {
		if c.Subscriptions[i].CustomRules != nil {
			continue
		}
		for j := range prev.Subscriptions {
			if prev.Subscriptions[j].Name != c.Subscriptions[i].Name {
				continue
			}
			rules := prev.Subscriptions[j].CustomRules
			if len(rules) == 0 {
				// 无规则可继承：保持 nil，避免把「空」写成显式空切片
				break
			}
			copied := make([]CustomRule, len(rules))
			copy(copied, rules)
			c.Subscriptions[i].CustomRules = copied
			break
		}
	}
}
