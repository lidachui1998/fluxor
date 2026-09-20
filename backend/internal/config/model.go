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
	DeletePhysical     []string       `json:"delete_physical,omitempty"`
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
