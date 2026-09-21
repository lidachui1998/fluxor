package configcheck

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// PayloadKind 规则载荷的语义类别，决定校验方式。
type PayloadKind int

const (
	// PayloadDomain 域名类载荷（DOMAIN / DOMAIN-KEYWORD）。
	PayloadDomain PayloadKind = iota
	// PayloadDomainSuffix 域名后缀类载荷（DOMAIN-SUFFIX）。
	PayloadDomainSuffix
	// PayloadDomainRegex 正则类载荷（DOMAIN-REGEX）。
	PayloadDomainRegex
	// PayloadGeosite GeoSite 分类名。
	PayloadGeosite
	// PayloadGeoIP 国家/地区代码或 lan。
	PayloadGeoIP
	// PayloadCIDR IPv4/IPv6 网段。
	PayloadCIDR
	// PayloadASN 自治域号。
	PayloadASN
	// PayloadPort 端口或端口段。
	PayloadPort
	// PayloadProcessName 进程名。
	PayloadProcessName
	// PayloadNetwork 网络类型（tcp / udp）。
	PayloadNetwork
	// PayloadRuleSet 规则集名称，必须存在于 rule-providers。
	PayloadRuleSet
)

// RuleSpec 描述一类内核支持的规则类型及其载荷约束。
//
// 白名单刻意只覆盖「单载荷 + 单一目标」的规则类型：AND / OR / NOT / SUB-RULE
// 需要嵌套规则语法，MATCH 会截断其后所有规则，均不适合由表单拼装。
//
// 清单里的每一项都以 mihomo（v1.19.31）实测 `-t` 通过为准；例如 PROTOCOL
// 虽在文档中出现，但该版本实测报 `unsupported rule type: PROTOCOL`，故不收录。
type RuleSpec struct {
	// Type 规则类型名，即写入规则行首段的值。
	Type string `json:"type"`
	// Example 载荷示例，前端用作输入框占位提示。
	Example string `json:"example"`
	// NoResolve 该类型是否支持 no-resolve 选项（IP 类规则与 RULE-SET）。
	NoResolve bool `json:"no_resolve"`
	// Payload 载荷类别，仅用于校验，不必下发给前端。
	Payload PayloadKind `json:"-"`
}

// ruleSpecs 内核支持的规则类型白名单。
var ruleSpecs = []RuleSpec{
	{Type: "DOMAIN", Example: "example.com", Payload: PayloadDomain},
	{Type: "DOMAIN-SUFFIX", Example: "example.com", Payload: PayloadDomainSuffix},
	{Type: "DOMAIN-KEYWORD", Example: "example", Payload: PayloadDomain},
	{Type: "DOMAIN-REGEX", Example: `^ads\..*$`, Payload: PayloadDomainRegex},
	{Type: "GEOSITE", Example: "github", Payload: PayloadGeosite},
	{Type: "GEOIP", Example: "CN", Payload: PayloadGeoIP, NoResolve: true},
	{Type: "IP-CIDR", Example: "1.1.1.0/24", Payload: PayloadCIDR, NoResolve: true},
	{Type: "IP-CIDR6", Example: "2001:db8::/32", Payload: PayloadCIDR, NoResolve: true},
	{Type: "IP-SUFFIX", Example: "1.1.1.0/24", Payload: PayloadCIDR, NoResolve: true},
	{Type: "IP-ASN", Example: "13335", Payload: PayloadASN, NoResolve: true},
	{Type: "SRC-IP-CIDR", Example: "192.168.1.0/24", Payload: PayloadCIDR, NoResolve: true},
	{Type: "SRC-PORT", Example: "443", Payload: PayloadPort},
	{Type: "DST-PORT", Example: "443", Payload: PayloadPort},
	{Type: "PROCESS-NAME", Example: "curl", Payload: PayloadProcessName},
	{Type: "NETWORK", Example: "tcp", Payload: PayloadNetwork},
	// RULE-SET 的载荷是规则集名称，前端会渲染为下拉选择，示例仅作占位提示
	{Type: "RULE-SET", Example: "从该订阅的规则集中选择", Payload: PayloadRuleSet, NoResolve: true},
}

// BuiltinRuleTargets 内核内置的规则目标，恒可用，无需存在于配置的代理组中。
//
// 仅收录实测可用的三个：DIRECT（直连）、REJECT（拒绝）、PASS（跳过本条继续匹配）。
var builtinRuleTargets = []string{"DIRECT", "REJECT", "PASS"}

// BuiltinRuleTargets 返回内置目标的副本。
func BuiltinRuleTargets() []string {
	out := make([]string, len(builtinRuleTargets))
	copy(out, builtinRuleTargets)
	return out
}

// RuleSpecs 返回规则类型白名单的副本。
func RuleSpecs() []RuleSpec {
	out := make([]RuleSpec, len(ruleSpecs))
	copy(out, ruleSpecs)
	return out
}

// LookupRuleSpec 按类型名查找规则规格；类型名大小写不敏感。
func LookupRuleSpec(ruleType string) (RuleSpec, bool) {
	want := strings.ToUpper(strings.TrimSpace(ruleType))
	for _, spec := range ruleSpecs {
		if spec.Type == want {
			return spec, true
		}
	}
	return RuleSpec{}, false
}

// ValidateRulePayload 校验载荷是否符合该规则类型的要求。
//
// 只做「格式」层面的校验：能否解析、是否含会破坏规则行结构的字符。
// 载荷是否真实存在（如 GEOSITE 分类、RULE-SET 规则集）需要调用方提供上下文。
func ValidateRulePayload(spec RuleSpec, payload string, ruleProviders map[string]struct{}) error {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return fmt.Errorf("%s 的取值不能为空", spec.Type)
	}
	if strings.ContainsAny(payload, ",\r\n") {
		return fmt.Errorf("%s 的取值不能包含逗号或换行", spec.Type)
	}

	switch spec.Payload {
	case PayloadDomain, PayloadDomainSuffix:
		// 域名类载荷允许中文（部分订阅用中文域名），只需排除空白与结构字符
		if strings.ContainsAny(payload, " \t") {
			return fmt.Errorf("%s 的取值不能包含空白字符", spec.Type)
		}
	case PayloadDomainRegex:
		if _, err := regexp.Compile(payload); err != nil {
			return fmt.Errorf("%s 不是合法的正则表达式: %v", spec.Type, err)
		}
	case PayloadGeosite:
		if !isGeositeCode(payload) {
			return fmt.Errorf("%s 的取值只能包含字母、数字、`.`、`-`、`_`、`!`（如 github、geolocation-!cn）", spec.Type)
		}
	case PayloadGeoIP:
		if !isGeoIPCode(payload) {
			return fmt.Errorf("%s 的取值应为国家/地区代码（如 CN、US）或内建别名 lan / private", spec.Type)
		}
	case PayloadCIDR:
		if _, err := netip.ParsePrefix(payload); err != nil {
			return fmt.Errorf("%s 的取值不是合法的 CIDR 网段（如 1.1.1.0/24）", spec.Type)
		}
	case PayloadASN:
		if !isDigits(payload) {
			return fmt.Errorf("%s 的取值应为纯数字 ASN（如 13335）", spec.Type)
		}
	case PayloadPort:
		if err := validatePort(payload); err != nil {
			return fmt.Errorf("%s 的取值无效: %w", spec.Type, err)
		}
	case PayloadProcessName:
		if strings.ContainsAny(payload, " \t") {
			return fmt.Errorf("%s 的取值不能包含空白字符", spec.Type)
		}
	case PayloadNetwork:
		switch strings.ToLower(payload) {
		case "tcp", "udp":
		default:
			return fmt.Errorf("%s 的取值只能是 tcp 或 udp", spec.Type)
		}
	case PayloadRuleSet:
		if _, ok := ruleProviders[payload]; !ok {
			// 文案不写「该订阅」：融合/自定义模式用的是模板自带的 rule-providers，
			// 压根没有订阅参与，说成订阅会把用户引到错误的地方去查
			return fmt.Errorf("规则集 %q 不在当前可选规则集里（该作用域没有这个 rule-provider）", payload)
		}
	}
	return nil
}

// ValidateRuleTarget 校验规则目标是否可解析。
//
// 内核在加载配置时会把每个目标解析为代理或代理组，解析失败即整份配置加载失败
// （实测报错形如 `rules[0] [DOMAIN,x.com,G] error: proxy [G] not found`），
// 因此这里必须与 knownTargets 严格比对。
func ValidateRuleTarget(target string, knownTargets map[string]struct{}) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("规则目标不能为空")
	}
	if strings.ContainsAny(target, ",\r\n") {
		return fmt.Errorf("规则目标不能包含逗号或换行")
	}
	if _, ok := knownTargets[target]; !ok {
		// 同上：作用域可能是订阅、档位模板或自定义模式，文案不绑定某一种
		return fmt.Errorf("规则目标 %q 不在当前可选目标里（节点可能已改名，请重新选择）", target)
	}
	return nil
}

// BuildRuleLine 按内核语法组装规则行：`TYPE,PAYLOAD,TARGET[,no-resolve]`。
func BuildRuleLine(spec RuleSpec, payload, target string, noResolve bool) string {
	line := spec.Type + "," + strings.TrimSpace(payload) + "," + strings.TrimSpace(target)
	if noResolve && spec.NoResolve {
		line += ",no-resolve"
	}
	return line
}

// RuleEnv 描述一份配置中可用的规则目标与规则集名称。
type RuleEnv struct {
	// Targets 可作为规则目标的名称：代理组 + 代理节点 + 内置目标。
	Targets map[string]struct{}
	// Providers rule-providers 的键，供 RULE-SET 校验。
	Providers map[string]struct{}
}

// ContainsTarget 判定目标是否可用。
func (e RuleEnv) ContainsTarget(target string) bool {
	_, ok := e.Targets[target]
	return ok
}

// RuleEnvFromDoc 从一份 Clash 配置中收集规则目标与规则集名称。
//
// 代理节点也计入【校验用】目标集合：实测 `DOMAIN,x.com,节点A` 这类直指节点的规则
// 可正常加载，因此不能因为「界面上不再让用户选节点」就把这类规则判为失效——
// 那会让用户已有的规则被静默跳过。前端的目标下拉另有来源（只列代理组，见
// configgen.RuleContext.GroupNames）。
//
// 注意切换模式下节点由订阅文件静态声明，因此能在这里看到；融合模式下节点来自
// proxy-providers，运行时才加载，静态校验看不到——故融合模式只能引用代理组。
func RuleEnvFromDoc(doc *Doc) RuleEnv {
	env := RuleEnv{
		Targets:   make(map[string]struct{}),
		Providers: make(map[string]struct{}),
	}
	for _, name := range builtinRuleTargets {
		env.Targets[name] = struct{}{}
	}
	for _, name := range doc.NodeNames("proxy-groups") {
		env.Targets[name] = struct{}{}
	}
	for _, name := range doc.NodeNames("proxies") {
		env.Targets[name] = struct{}{}
	}
	for _, name := range doc.MappingKeys("rule-providers") {
		env.Providers[name] = struct{}{}
	}
	return env
}

// isGeositeCode 判定 GeoSite 分类名（如 github、geolocation-!cn、category-ads-all）。
func isGeositeCode(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_', r == '!':
		default:
			return false
		}
	}
	return s != ""
}

// isGeoIPCode 判定国家/地区代码或内建别名（lan / private）。
func isGeoIPCode(s string) bool {
	// lan 与 private 是内核内建的保留名（实测 GEOIP,lan / GEOIP,private 均可加载）
	switch strings.ToLower(s) {
	case "lan", "private":
		return true
	}
	if len(s) < 2 || len(s) > 4 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// isDigits 判定字符串是否为非空纯数字。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validatePort 校验端口或端口段（如 443、1000-2000）。
func validatePort(s string) error {
	parts := strings.Split(s, "-")
	if len(parts) > 2 {
		return fmt.Errorf("应形如 443 或 1000-2000")
	}
	for _, part := range parts {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return fmt.Errorf("应形如 443 或 1000-2000")
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("端口需在 1-65535 之间")
		}
	}
	if len(parts) == 2 {
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		if start > end {
			return fmt.Errorf("端口段起始值不能大于结束值")
		}
	}
	return nil
}
