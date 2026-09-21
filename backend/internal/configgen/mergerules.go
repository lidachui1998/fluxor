package configgen

import (
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"fmt"
	"log"
	"strings"

	"gopkg.in/yaml.v3"
)

// 融合模式支持的规则集档位。
//
// 唯一来源是 config 包（档位取值属于 SubscribeConfig 模型的一部分），
// 这里只做别名，避免两处各写一份字面量而漂移。
const (
	RuleGroupBase = config.RuleGroupBase
	RuleGroupFull = config.RuleGroupFull
)

// MergeRuleSetNames 返回融合模式支持的规则集档位列表。
func MergeRuleSetNames() []string {
	return []string{RuleGroupBase, RuleGroupFull}
}

// IsValidMergeRuleSet 判定规则集档位是否受支持。
func IsValidMergeRuleSet(ruleGroup string) bool {
	return config.IsValidRuleGroup(ruleGroup)
}

// mergeRuleSetBlocks 返回某档位的「代理组块」与「规则集块」。
//
// 直接复用生成配置时用的同一批模板常量，而不是另维护一份代理组清单：
// 模板一改（增删代理组/规则集），自定义规则的可选目标与校验集合自动跟随，
// 不会出现「界面上选得到、生成时校验不过」的错配。
func mergeRuleSetBlocks(ruleGroup string) (groups, providers, rules string, err error) {
	switch ruleGroup {
	case RuleGroupBase:
		return proxyGroupsBase, "", rulesBase, nil
	case RuleGroupFull:
		// full 的代理组模板是纯静态内容（地区组以 include-all-providers 引用
		// provider，模板里不含订阅名），可直接读取组名，无需先做任何替换。
		return proxyGroupsFullTemplate, ruleProvidersFull, rulesFull, nil
	default:
		return "", "", "", fmt.Errorf("未知规则集: %s", ruleGroup)
	}
}

// MergeRuleSetEnv 返回某档位可用的规则目标与规则集名称。
//
// 目标只包含该档位模板里的代理组与内核内置目标：融合模式的节点来自
// proxy-providers、运行时才加载，静态校验看不到，引用节点名会让内核
// 拒绝加载整份配置（实测 `proxy [X] not found`）。
func MergeRuleSetEnv(ruleGroup string) (configcheck.RuleEnv, error) {
	groups, providers, _, err := mergeRuleSetBlocks(ruleGroup)
	if err != nil {
		return configcheck.RuleEnv{}, err
	}
	doc := configcheck.ParseMappingDoc([]byte(groups))
	env := configcheck.RuleEnv{
		Targets:   make(map[string]struct{}),
		Providers: make(map[string]struct{}),
	}
	for _, name := range configcheck.BuiltinRuleTargets() {
		env.Targets[name] = struct{}{}
	}
	for _, name := range doc.NodeNames("proxy-groups") {
		env.Targets[name] = struct{}{}
	}
	if providers != "" {
		providerDoc := configcheck.ParseMappingDoc([]byte(providers))
		for _, name := range providerDoc.MappingKeys("rule-providers") {
			env.Providers[name] = struct{}{}
		}
	}
	return env, nil
}

// MergeRuleSetRuleLines 返回某档位内置模板规则的规则行（用于判重）。
//
// 自定义规则若与档位内置规则完全同形，注入时会被幂等判重跳过——用户会看到
// 「保存成功但规则没生效」。因此在写入前就用这份集合拦截并给出明确原因。
func MergeRuleSetRuleLines(ruleGroup string) ([]string, error) {
	_, _, rules, err := mergeRuleSetBlocks(ruleGroup)
	if err != nil {
		return nil, err
	}
	var lines []string
	seq := configcheck.ParseMappingDoc([]byte(rules)).Get("rules")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return lines, nil
	}
	for _, item := range seq.Content {
		lines = append(lines, item.Value)
	}
	return lines, nil
}

// MergeRuleSetContext 构建某档位的规则上下文（目标 = 该档位代理组 + 内置目标，
// 既有规则行 = 该档位内置模板规则），供自定义规则的校验与判重复用。
func MergeRuleSetContext(ruleGroup string) (*RuleContext, error) {
	env, err := MergeRuleSetEnv(ruleGroup)
	if err != nil {
		return nil, err
	}
	lines, err := MergeRuleSetRuleLines(ruleGroup)
	if err != nil {
		return nil, err
	}
	return NewRuleContext(nil, env, lines), nil
}

// mergeCustomRules 取某档位的融合模式自定义规则并注入文档。
//
// 调用点必须在 appendRuleSet 之后：校验集合与最终产物都依赖该档位的代理组就位。
func applyMergeCustomRules(doc *configcheck.Doc, cfg config.SubscribeConfig) error {
	rules := cfg.MergeCustomRulesFor(cfg.RuleGroup)
	if len(rules) == 0 {
		return nil
	}
	result, err := ApplyCustomRules(doc, rules)
	if err != nil {
		return fmt.Errorf("注入融合模式自定义规则失败: %w", err)
	}
	for _, skip := range result.Skipped {
		// 跳过而不是写进配置：目标不存在的规则会让内核拒绝加载整份配置
		log.Printf("[CUSTOM-RULE] 融合模式（%s）跳过规则 %s,%s,%s: %s",
			cfg.RuleGroup, skip.Rule.Type, skip.Rule.Payload, skip.Rule.Target, skip.Reason)
	}
	return nil
}

// MergeRuleSkips 返回某档位中因目标/规则集失效而无法写入配置的规则说明。
//
// 与切换模式 writeRuntimeConfig 返回的 ApplyResult.Skipped 对齐：让接口能如实
// 回报「规则已保存，但有 N 条因目标不存在未写入配置」，而不是静默少几条规则。
// 这里只做校验、不改动任何状态。
func MergeRuleSkips(cfg config.SubscribeConfig, ruleGroup string) []string {
	env, err := MergeRuleSetEnv(ruleGroup)
	if err != nil {
		return nil
	}
	var skipped []string
	for _, rule := range cfg.MergeCustomRulesFor(ruleGroup) {
		if _, err := ValidateCustomRule(rule, env); err != nil {
			skipped = append(skipped, strings.Join([]string{rule.Type, rule.Payload, rule.Target}, ","))
		}
	}
	return skipped
}
