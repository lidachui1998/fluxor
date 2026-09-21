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

// AdoptServerOwnedRuleFields 让「整份配置覆盖写」以**服务端已存的规则**为准。
//
// 三种作用域的自定义规则（`subscriptions[].custom_rules`、`merge_custom_rules`、
// `custom_mode_rules`）都只由专用规则接口维护；而 /subscribe/config 与 /subscribe/generate
// 是整份覆盖写，请求体由调用方把「上次读到的配置」铺开拼成——规则一旦在弹窗里改过，
// 这份快照就是**过期**的。若按「调用方带了就用调用方」处理，实测会出现：
// 刚删掉的规则在「保存并应用」后复活，刚新增的规则被旧列表覆盖丢失。
//
// 因此这里一律取服务端状态：键存在与否、是否为空都不影响——**规则接口是唯一的修改入口**。
// 唯一例外：服务端没有的**全新订阅**（按名字匹配不到）没有旧值可取，保留请求体里的规则，
// 免得「带规则创建订阅」这类调用被静默丢数据（改名也走这条路，规则随之带过去）。
//
// 调用方必须传入写锁内的上一份状态；本函数只做深拷贝，不触碰 c 的其它字段。
func (c *SubscribeConfig) AdoptServerOwnedRuleFields(prev SubscribeConfig) {
	// 自定义模式：不按档位分表，整份以服务端为准
	c.CustomModeRules = copyRules(prev.CustomModeRules)

	// 融合模式：两个档位都按服务端为准（另一档本就惰性，也不该被请求体改写）
	if prev.MergeCustomRules == nil {
		c.MergeCustomRules = nil
	} else {
		c.MergeCustomRules = make(map[string][]CustomRule, len(prev.MergeCustomRules))
		for group, rules := range prev.MergeCustomRules {
			c.MergeCustomRules[group] = copyRules(rules)
		}
	}

	// 切换模式：按订阅名取服务端那一份；服务端不认识的名字（新建/改名）保留请求体
	for i := range c.Subscriptions {
		name := c.Subscriptions[i].Name
		for j := range prev.Subscriptions {
			if prev.Subscriptions[j].Name != name {
				continue
			}
			c.Subscriptions[i].CustomRules = copyRules(prev.Subscriptions[j].CustomRules)
			break
		}
	}
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
