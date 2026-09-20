package configgen

import (
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RuleSkip 描述一条被跳过的自定义规则及原因。
type RuleSkip struct {
	Rule   config.CustomRule
	Reason string
}

// ApplyResult 汇总一次自定义规则注入的结果。
type ApplyResult struct {
	// Applied 真正写入配置的规则条数。
	Applied int
	// Skipped 未写入的规则（目标/规则集已不存在，或与既有规则重复）。
	//
	// 这类规则不写入的原因：内核在加载配置时若发现目标解析不到，会直接拒绝整份
	// 配置（实测 `rules[0] [DOMAIN,x.com,G] error: proxy [G] not found`）。
	// 机场更新后代理组改名属于常态，此时「少一条自定义规则」远优于「配置完全
	// 无法加载、面板与内核一起不可用」。
	Skipped []RuleSkip
}

// ValidateCustomRule 校验单条自定义规则并返回最终写入配置的规则行。
//
// 校验项：类型在白名单内、载荷格式合法（RULE-SET 需存在于 rule-providers）、
// 目标存在于该配置的代理组/代理节点或内置目标中。
func ValidateCustomRule(rule config.CustomRule, env configcheck.RuleEnv) (string, error) {
	spec, ok := configcheck.LookupRuleSpec(rule.Type)
	if !ok {
		return "", fmt.Errorf("不支持的规则类型: %s", strings.TrimSpace(rule.Type))
	}
	if err := configcheck.ValidateRulePayload(spec, rule.Payload, env.Providers); err != nil {
		return "", err
	}
	if err := configcheck.ValidateRuleTarget(rule.Target, env.Targets); err != nil {
		return "", err
	}
	return configcheck.BuildRuleLine(spec, rule.Payload, rule.Target, rule.NoResolve), nil
}

// RuleContext 描述一份配置可供自定义规则使用的上下文。
type RuleContext struct {
	Doc      *configcheck.Doc
	Env      configcheck.RuleEnv
	existing map[string]struct{}
}

// LoadRuleContext 读取一份 Clash 配置文件并构建规则上下文。
//
// 切换模式下传入的应是该订阅的原始节点文件（proxies/<订阅名>.yaml）：
// 自定义规则的合法目标与规则集名称都以这份文件为准。
//
// Doc 为该文件的文档，供调用方读取更多信息（如代理组名称）。
func LoadRuleContext(path string) (*RuleContext, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := configcheck.ParseValidatedDoc(content)
	if err != nil {
		return nil, fmt.Errorf("订阅文件 %s 无法解析: %w", path, err)
	}
	var lines []string
	if seq := doc.Get("rules"); seq != nil && seq.Kind == yaml.SequenceNode {
		for _, item := range seq.Content {
			lines = append(lines, item.Value)
		}
	}
	return NewRuleContext(doc, configcheck.RuleEnvFromDoc(doc), lines), nil
}

// NewRuleContext 用现成的文档、规则目标集合与「已存在的规则行」构造上下文。
//
// 切换模式经 LoadRuleContext 从订阅文件构造；融合模式没有订阅文件，
// 直接用该档位模板的代理组与内置规则构造（doc 可为 nil，此时不带文档）。
func NewRuleContext(doc *configcheck.Doc, env configcheck.RuleEnv, existingLines []string) *RuleContext {
	ctx := &RuleContext{
		Doc:      doc,
		Env:      env,
		existing: make(map[string]struct{}, len(existingLines)),
	}
	for _, line := range existingLines {
		ctx.existing[normalizeRuleLine(line)] = struct{}{}
	}
	return ctx
}

// HasRuleLine 判定某条规则行是否已存在（按去空白后的文本比较）。
func (c *RuleContext) HasRuleLine(line string) bool {
	_, ok := c.existing[normalizeRuleLine(line)]
	return ok
}

// GroupNames 返回该订阅**代理组**名称（去重并排序），供前端目标下拉使用。
//
// 只用代理组、不暴露代理节点：节点的命名由机场决定，随时可能改名或增删，
// 而规则目标一旦解析不到，内核会拒绝加载整份配置。引用代理组则稳定得多
// （组名由订阅自带规则共同依赖，机场不会轻易改动）。
//
// 注意与校验集合的差别：RuleEnv.Targets 仍包含节点名——内核确实接受指向节点的
// 规则，若校验时把节点排除，用户已有的「指向节点」的规则会被判为失效而静默跳过。
func (c *RuleContext) GroupNames() []string {
	// 切换模式：直接从订阅文件读代理组（只列组，不列节点）
	if c.Doc != nil {
		groups := make(map[string]struct{})
		for _, name := range c.Doc.NodeNames("proxy-groups") {
			groups[name] = struct{}{}
		}
		return sortedKeys(groups)
	}
	// 融合模式：没有订阅文件，改为从该档位模板的目标集合里剔除内置目标
	return nonBuiltinKeys(c.Env.Targets)
}

// ProviderNames 返回该订阅可引用的规则集名称（已排序）。
func (c *RuleContext) ProviderNames() []string {
	return sortedKeys(c.Env.Providers)
}

// ApplyCustomRules 把自定义规则幂等注入文档的 rules 序列。
//
// 插入位置由每条规则的 Position 决定：
//   - before（默认）：插到 rules 最前，优先级高于订阅自带规则；
//   - after：插到最后一条 MATCH 之前，保留订阅原装分流逻辑。
//
// 同一分组内的先后顺序 = 切片顺序：before 组按切片顺序依次插在最前，
// after 组按切片顺序插在 MATCH 之前，因此界面上的排序即最终生效顺序。
//
// 幂等：重复调用不会累积重复规则（已在配置中的规则行直接跳过）。
// 返回 error 仅限「文档结构不可安全改写」（如 rules 不是列表）——此时宁可整份
// 失败也不改写，避免把订阅自带规则整块吃掉。
func ApplyCustomRules(doc *configcheck.Doc, rules []config.CustomRule) (ApplyResult, error) {
	var result ApplyResult
	if len(rules) == 0 {
		return result, nil
	}
	env := configcheck.RuleEnvFromDoc(doc)

	seq := doc.Get("rules")
	if seq == nil {
		// 订阅配置可能完全没有 rules 键（内核允许），此处补建序列节点
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		doc.SetNode("rules", seq)
	} else if seq.Kind == yaml.ScalarNode && seq.Tag == "!!null" {
		// `rules:` 后留空时解析为 null 标量，同样替换为序列
		seq.Kind = yaml.SequenceNode
		seq.Tag = "!!seq"
		seq.Value = ""
	}
	if seq.Kind != yaml.SequenceNode {
		return result, fmt.Errorf("配置中的 rules 不是列表，无法安全追加自定义规则")
	}

	existing := make(map[string]struct{}, len(seq.Content)+len(rules))
	for _, item := range seq.Content {
		existing[normalizeRuleLine(item.Value)] = struct{}{}
	}

	var beforeNodes, afterNodes []*yaml.Node
	for _, rule := range rules {
		line, err := ValidateCustomRule(rule, env)
		if err != nil {
			result.Skipped = append(result.Skipped, RuleSkip{Rule: rule, Reason: err.Error()})
			continue
		}
		key := normalizeRuleLine(line)
		if _, dup := existing[key]; dup {
			result.Skipped = append(result.Skipped, RuleSkip{Rule: rule, Reason: "该规则已存在于配置中"})
			continue
		}
		existing[key] = struct{}{}
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: line}
		if config.NormalizeRulePosition(rule.Position) == config.RulePositionBefore {
			beforeNodes = append(beforeNodes, node)
		} else {
			afterNodes = append(afterNodes, node)
		}
		result.Applied++
	}

	if len(beforeNodes) == 0 && len(afterNodes) == 0 {
		return result, nil
	}

	// after 的落点：最后一条 MATCH 之前；没有 MATCH 则追加到末尾。
	insertAt := lastMatchIndex(seq)
	combined := make([]*yaml.Node, 0, len(seq.Content)+len(beforeNodes)+len(afterNodes))
	combined = append(combined, beforeNodes...)
	combined = append(combined, seq.Content[:insertAt]...)
	combined = append(combined, afterNodes...)
	combined = append(combined, seq.Content[insertAt:]...)
	seq.Content = combined
	return result, nil
}

// ApplyCustomRulesToFile 就地改写配置文件：读取 → 注入 → 写回。
//
// 无自定义规则时完全不读写文件（保持「订阅文件原样副本」的既有语义）。
// 校验与改写全部在内存中完成，任一步失败都不会落盘，避免留下内核加载不了的
// 半成品配置。
func ApplyCustomRulesToFile(path string, rules []config.CustomRule) (ApplyResult, error) {
	if len(rules) == 0 {
		return ApplyResult{}, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ApplyResult{}, err
	}
	doc, err := configcheck.ParseValidatedDoc(content)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("配置文件 %s 无法解析: %w", path, err)
	}
	result, err := ApplyCustomRules(doc, rules)
	if err != nil {
		return result, err
	}
	out, err := doc.Bytes()
	if err != nil {
		return result, err
	}
	return result, os.WriteFile(path, out, 0644)
}

// lastMatchIndex 返回最后一条 MATCH 规则的下标；不存在时返回序列长度（即末尾）。
func lastMatchIndex(seq *yaml.Node) int {
	for i := len(seq.Content) - 1; i >= 0; i-- {
		if isMatchRule(seq.Content[i].Value) {
			return i
		}
	}
	return len(seq.Content)
}

// isMatchRule 判定一行规则是否为 MATCH（取首个逗号前的字段，大小写不敏感）。
func isMatchRule(line string) bool {
	head, _, _ := strings.Cut(line, ",")
	return strings.EqualFold(strings.TrimSpace(head), "MATCH")
}

// normalizeRuleLine 归一化规则文本，用于幂等判重。
//
// 只裁剪首尾空白，不做大小写折叠：DOMAIN-REGEX 的载荷是大小写敏感的正则，
// 折叠大小写会把两条语义不同的正则误判为重复而漏写。
func normalizeRuleLine(line string) string {
	return strings.TrimSpace(line)
}

// nonBuiltinKeys 返回目标集合中除内核内置目标（DIRECT/REJECT/PASS）以外的名称，已排序。
func nonBuiltinKeys(targets map[string]struct{}) []string {
	builtins := make(map[string]struct{}, len(configcheck.BuiltinRuleTargets()))
	for _, name := range configcheck.BuiltinRuleTargets() {
		builtins[name] = struct{}{}
	}
	rest := make(map[string]struct{}, len(targets))
	for name := range targets {
		if _, isBuiltin := builtins[name]; isBuiltin {
			continue
		}
		rest[name] = struct{}{}
	}
	return sortedKeys(rest)
}

// sortedKeys 返回映射键的排序副本，保证前端下拉与列表顺序稳定。
func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
