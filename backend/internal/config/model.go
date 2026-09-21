package config

import "strings"

// SubscribeConfig 订阅配置结构体
type SubscribeConfig struct {
	ProxyPort          int            `json:"proxy_port"`
	TproxyPort         int            `json:"tproxy_port"`
	PanelPort          int            `json:"panel_port"`
	PanelSecret        string         `json:"panel_secret"`
	RuleGroup          string         `json:"rule_group"`
	UIPanel            string         `json:"ui_panel"`
	MetaBackendURL     string         `json:"meta_backend_url"`
	Mode               string         `json:"mode"`
	ActiveSubscription string         `json:"active_subscription"`
	Subscriptions      []Subscription `json:"subscriptions"`
	// CustomNodes 自定义模式下手工添加的节点。
	//
	// 只在 Mode == ModeCustom 时参与配置生成：节点被拼成 config.yaml 的 proxies 块，
	// 与订阅（proxy-providers）无关，因此自定义模式下「订阅列表」不参与生成、只是历史数据。
	CustomNodes []CustomNode `json:"custom_nodes"`
	// MergeCustomRules 融合模式的自定义规则，按规则集档位（base / full）分开存放。
	//
	// 必须按档位分开：两个档位的代理组、规则集与内置规则完全不同，同一条规则
	// 在 base 下可能指向不存在的组；分开存放后切换档位即切换各自的规则列表。
	// 自定义模式固定使用标准档位，其自定义规则也寄存在本表的 base 档下（见 ModeCustom）。
	MergeCustomRules map[string][]CustomRule `json:"merge_custom_rules,omitempty"`
	DeletePhysical   []string                `json:"delete_physical,omitempty"`
}

// 订阅中心支持的三种模式（SubscribeConfig.Mode 的取值）。
const (
	// ModeMerge 融合模式：合并全部订阅（proxy-providers），按规则集档位生成配置。
	ModeMerge = "merge"
	// ModeSwitch 切换模式：config.yaml 是所选订阅原始文件的副本，用订阅自带的规则与代理组。
	ModeSwitch = "switch"
	// ModeCustom 自定义模式：完全不使用订阅，节点由用户在界面上手工添加；节点被拼成
	// proxies 块写进配置模板，规则集固定使用标准档位（RuleGroupBase），
	// 其自定义规则与「融合模式 + 标准档位」共用同一份列表（两者的代理组与内置规则集合相同）。
	ModeCustom = "custom"
)

// IsValidMode 判定模式是否受支持。
func IsValidMode(mode string) bool {
	return mode == ModeMerge || mode == ModeSwitch || mode == ModeCustom
}

// 融合模式的规则集档位（与 SubscribeConfig.RuleGroup 的取值一致）。
const (
	// RuleGroupBase 标准档位：按国内外 IP/域名粗略分流，无 rule-providers。
	RuleGroupBase = "base"
	// RuleGroupFull 详细档位：基于 ruleset 细分，带 31 个 rule-providers。
	RuleGroupFull = "full"
)

// IsValidRuleGroup 判定规则集档位是否受支持。
func IsValidRuleGroup(ruleGroup string) bool {
	return ruleGroup == RuleGroupBase || ruleGroup == RuleGroupFull
}

// MergeCustomRulesFor 返回某个规则集档位的融合模式自定义规则（无则返回 nil）。
func (c SubscribeConfig) MergeCustomRulesFor(ruleGroup string) []CustomRule {
	if c.MergeCustomRules == nil {
		return nil
	}
	return c.MergeCustomRules[ruleGroup]
}

// Subscription 描述单个订阅源及其最近一次的更新元数据。
type Subscription struct {
	Name             string                 `json:"name"`
	URL              string                 `json:"url"`
	UpdateInterval   int                    `json:"update_interval"`
	HealthInterval   int                    `json:"health_interval"`
	Prefix           string                 `json:"prefix"`
	CustomRules      []CustomRule           `json:"custom_rules,omitempty"`
	UpdatedAt        string                 `json:"updated_at,omitempty"`
	SubscriptionInfo map[string]interface{} `json:"subscription_info,omitempty"`
}

// CustomNode 描述自定义模式下用户手工添加的一个出站代理节点。
//
// Config 只保存「与该协议默认值不同」的字段：每个协议的字段表与默认模板由
// nodespec 包提供（后端可为协议单独增补默认值），读取时补齐、保存时剔除差异，
// 因此默认值调整能自动作用到历史数据，也不会把一堆零值写进 fluxor.json。
type CustomNode struct {
	// ID 由后端生成的稳定标识，供前端编辑/删除单个节点使用。
	ID string `json:"id"`
	// Name 节点名，写入 config.yaml 的 proxies[].name，同时是规则可引用的目标名。
	Name string `json:"name"`
	// Type 协议类型（nodespec 支持的取值，如 ss / vmess / vless）。
	Type string `json:"type"`
	// Config 协议字段表里「非默认值」的字段，键为内核配置键（如 server / cipher / ws-path 无）。
	Config map[string]any `json:"config,omitempty"`
}

// CustomRule 描述切换模式下挂在某个订阅上的单条自定义规则。
//
// 存的是「结构化字段」而非拼好的规则文本：规则文本由 configgen 按内核语法组装，
// 避免用户输入逗号/空格破坏规则行结构，也便于对类型与目标做校验。
//
// 归属订阅而非全局：切换模式下每条规则都绑定该订阅自带的代理组，
// 随订阅一起增删改，不与其他订阅互相污染。
type CustomRule struct {
	// ID 由后端生成的稳定标识，供前端删除单条规则使用。
	ID string `json:"id"`
	// Type 规则类型（configcheck.RuleSpecs 暴露的白名单），如 DOMAIN-SUFFIX。
	Type string `json:"type"`
	// Payload 规则载荷，如 example.com / 1.1.1.0/24 / 规则集名称。
	Payload string `json:"payload"`
	// Target 命中后的目标：该订阅的代理组名，或 DIRECT / REJECT / PASS。
	Target string `json:"target"`
	// Position 插入位置：RulePositionBefore（默认，插在最前，优先级高于订阅自带
	// 规则）或 RulePositionAfter（插在最后一条 MATCH 之前，作为兜底补充）。
	Position string `json:"position"`
	// NoResolve 仅对 IP 类与 RULE-SET 规则有意义（跳过域名解析）。
	NoResolve bool `json:"no_resolve,omitempty"`
}

// 自定义规则的插入位置。
const (
	// RulePositionBefore 插在最前：优先级高于订阅自带规则，用于强制覆盖。
	//
	// 这是默认值：自定义规则的意图通常是「覆盖或补充订阅自带的分流」，
	// 放在最前才不会被订阅自带规则先命中而失效。
	RulePositionBefore = "before"
	// RulePositionAfter 插在最后一条 MATCH 之前：保留订阅原装分流逻辑，作为补充。
	RulePositionAfter = "after"
)

// NormalizeRulePosition 归一化插入位置，空值/非法值回落到默认的 before（最前）。
//
// 大小写与首尾空白不敏感：position 来自 HTTP 请求体，不应因为客户端写了
// "After" 就被当成非法值丢到「最前」——那会静默改变用户的优先级意图。
func NormalizeRulePosition(position string) string {
	if strings.EqualFold(strings.TrimSpace(position), RulePositionAfter) {
		return RulePositionAfter
	}
	return RulePositionBefore
}
