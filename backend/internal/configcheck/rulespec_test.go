package configcheck

import "testing"

// TestValidateRulePayload 覆盖白名单类型载荷的接受/拒绝边界。
func TestValidateRulePayload(t *testing.T) {
	providers := map[string]struct{}{"ads": {}, "private": {}}

	cases := []struct {
		name    string
		ruleTyp string
		payload string
		wantErr bool
	}{
		{"域名", "DOMAIN", "example.com", false},
		{"域名后缀", "DOMAIN-SUFFIX", "example.com", false},
		{"域名关键词", "DOMAIN-KEYWORD", "example", false},
		{"正则", "DOMAIN-REGEX", `^ads\..*$`, false},
		{"非法正则", "DOMAIN-REGEX", `^ads\..*($`, true},
		{"GEOSITE", "GEOSITE", "geolocation-!cn", false},
		{"GEOSITE 非法字符", "GEOSITE", "git hub", true},
		{"GEOIP 国家码", "GEOIP", "CN", false},
		{"GEOIP lan", "GEOIP", "lan", false},
		{"GEOIP private", "GEOIP", "private", false},
		{"GEOIP 别名大小写", "GEOIP", "Private", false},
		{"GEOIP 非法", "GEOIP", "CHINA", true},
		{"IPv4 网段", "IP-CIDR", "1.1.1.0/24", false},
		{"IPv6 网段", "IP-CIDR6", "2001:db8::/32", false},
		{"非法网段", "IP-CIDR", "1.1.1.1", true},
		{"ASN", "IP-ASN", "13335", false},
		{"ASN 非法", "IP-ASN", "AS13335", true},
		{"端口", "DST-PORT", "443", false},
		{"端口段", "DST-PORT", "1000-2000", false},
		{"倒序端口段", "DST-PORT", "2000-1000", true},
		{"端口段三段", "DST-PORT", "1-2-3", true},
		{"端口越界", "DST-PORT", "70000", true},
		{"源端口段", "SRC-PORT", "1000-2000", false},
		{"进程名", "PROCESS-NAME", "curl", false},
		{"网络类型", "NETWORK", "udp", false},
		{"网络类型非法", "NETWORK", "sctp", true},
		{"规则集存在", "RULE-SET", "ads", false},
		{"规则集不存在", "RULE-SET", "nope", true},
		{"空载荷", "DOMAIN", "  ", true},
		{"含逗号", "DOMAIN-SUFFIX", "a.com,b.com", true},
		{"含换行", "DOMAIN-SUFFIX", "a.com\nb.com", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, ok := LookupRuleSpec(tc.ruleTyp)
			if !ok {
				t.Fatalf("规则类型 %s 不在白名单中", tc.ruleTyp)
			}
			err := ValidateRulePayload(spec, tc.payload, providers)
			if tc.wantErr && err == nil {
				t.Fatalf("期望校验失败，实际通过（%s,%s）", tc.ruleTyp, tc.payload)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("期望校验通过，实际失败: %v", err)
			}
		})
	}
}

// TestLookupRuleSpecCaseInsensitive 类型名大小写不敏感。
func TestLookupRuleSpecCaseInsensitive(t *testing.T) {
	spec, ok := LookupRuleSpec(" domain-suffix ")
	if !ok {
		t.Fatal("小写带空白的类型名应能命中白名单")
	}
	if spec.Type != "DOMAIN-SUFFIX" {
		t.Fatalf("期望 DOMAIN-SUFFIX，实际 %s", spec.Type)
	}
	if _, ok := LookupRuleSpec("PROTOCOL"); ok {
		t.Fatal("PROTOCOL 在该内核版本实测不受支持，不应出现在白名单中")
	}
}

// TestValidateRuleTarget 目标必须存在，内置目标恒可用。
func TestValidateRuleTarget(t *testing.T) {
	targets := map[string]struct{}{"DIRECT": {}, "REJECT": {}, "PASS": {}, "🚀 节点选择": {}}

	if err := ValidateRuleTarget("🚀 节点选择", targets); err != nil {
		t.Fatalf("已存在的代理组不应报错: %v", err)
	}
	if err := ValidateRuleTarget("DIRECT", targets); err != nil {
		t.Fatalf("内置目标不应报错: %v", err)
	}
	if err := ValidateRuleTarget("不存在的组", targets); err == nil {
		t.Fatal("不存在的目标必须报错（内核会因 proxy not found 拒绝整份配置）")
	}
	if err := ValidateRuleTarget("", targets); err == nil {
		t.Fatal("空目标必须报错")
	}
}

// TestBuildRuleLine 组装规则行：仅当类型支持时才附加 no-resolve。
func TestBuildRuleLine(t *testing.T) {
	cidr, _ := LookupRuleSpec("IP-CIDR")
	if got := BuildRuleLine(cidr, "1.1.1.0/24", "DIRECT", true); got != "IP-CIDR,1.1.1.0/24,DIRECT,no-resolve" {
		t.Fatalf("IP-CIDR 规则行组装错误: %s", got)
	}
	domain, _ := LookupRuleSpec("DOMAIN-SUFFIX")
	if got := BuildRuleLine(domain, "example.com", "🚀 节点选择", true); got != "DOMAIN-SUFFIX,example.com,🚀 节点选择" {
		t.Fatalf("DOMAIN-SUFFIX 不应附加 no-resolve: %s", got)
	}
}

// TestRuleEnvFromDoc 收集目标与规则集：内置目标、代理组、代理节点、rule-providers。
func TestRuleEnvFromDoc(t *testing.T) {
	doc, err := ParseDoc([]byte(`
proxies:
  - {name: "节点A", type: ss}
proxy-groups:
  - {name: "🚀 节点选择", type: select}
rule-providers:
  ads:
    type: http
`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	env := RuleEnvFromDoc(doc)
	for _, want := range []string{"DIRECT", "REJECT", "PASS", "🚀 节点选择", "节点A"} {
		if !env.ContainsTarget(want) {
			t.Fatalf("目标 %s 应被收集", want)
		}
	}
	if _, ok := env.Providers["ads"]; !ok {
		t.Fatal("rule-providers 的键应被收集")
	}
}

// TestRuleEnvFromDocTolerant 缺失字段或结构异常时静默跳过，不报错。
func TestRuleEnvFromDocTolerant(t *testing.T) {
	doc, err := ParseDoc([]byte("proxies: not-a-list\n"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	env := RuleEnvFromDoc(doc)
	if !env.ContainsTarget("DIRECT") {
		t.Fatal("内置目标应始终可用")
	}
	if len(env.Providers) != 0 {
		t.Fatal("缺失 rule-providers 时不应收集到规则集")
	}
}
