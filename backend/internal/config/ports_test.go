package config

import "testing"

// 端口与模式/档位的校验是「落库前的最后一道闸门」：绕过前端直接调接口时，
// 非法取值一旦写进 settings.json，内核会因为监听失败而起不来，
// 而错误信息只会说「配置加载失败」，很难归因到某个端口字段。

func TestValidateLocalPorts(t *testing.T) {
	base := func() SubscribeConfig {
		return SubscribeConfig{ProxyPort: 7890, PanelPort: 9090, TproxyPort: 7898}
	}

	cases := []struct {
		name    string
		mutate  func(*SubscribeConfig)
		wantErr bool
	}{
		{"默认取值可用", func(*SubscribeConfig) {}, false},
		{"tproxy 端口为 0 表示禁用", func(c *SubscribeConfig) { c.TproxyPort = 0 }, false},
		{"代理端口低于 1025 应拒绝", func(c *SubscribeConfig) { c.ProxyPort = 80 }, true},
		{"代理端口为 0 应拒绝（必填）", func(c *SubscribeConfig) { c.ProxyPort = 0 }, true},
		{"面板端口为 0 应拒绝（必填）", func(c *SubscribeConfig) { c.PanelPort = 0 }, true},
		{"超过 65535 应拒绝", func(c *SubscribeConfig) { c.PanelPort = 70000 }, true},
		{"负数应拒绝", func(c *SubscribeConfig) { c.TproxyPort = -1 }, true},
		{"端口重复应拒绝", func(c *SubscribeConfig) { c.ProxyPort = 9090 }, true},
		{"tproxy 与面板端口重复应拒绝", func(c *SubscribeConfig) { c.TproxyPort = 9090 }, true},
		{"边界值 1025 可用", func(c *SubscribeConfig) { c.ProxyPort = 1025 }, false},
		{"边界值 65535 可用", func(c *SubscribeConfig) { c.PanelPort = 65535 }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)
			err := ValidateLocalPorts(cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("应报错，实际通过（cfg=%+v）", cfg)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("不应报错: %v（cfg=%+v）", err, cfg)
			}
		})
	}
}

func TestValidateOptionalPort(t *testing.T) {
	if err := ValidateOptionalPort("TPROXY 端口", 0); err != nil {
		t.Fatalf("0 表示不设置，应放行: %v", err)
	}
	if err := ValidateOptionalPort("TPROXY 端口", 7898); err != nil {
		t.Fatalf("合法端口应放行: %v", err)
	}
	if err := ValidateOptionalPort("TPROXY 端口", 999999); err == nil {
		t.Fatal("超范围端口应拒绝")
	}
}

// TestNormalizeMode 空值回落 merge（与旧行为一致），未知取值拒绝而不是静默当成 merge。
//
// 静默当成 merge 的旧行为会把一个不存在的模式**原样落库**，之后生成链路与定时器都会
// 走进「既不是 switch 也不是 custom」的分支，行为无从预期。
func TestNormalizeMode(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", ModeMerge, false},
		{ModeMerge, ModeMerge, false},
		{ModeSwitch, ModeSwitch, false},
		{ModeCustom, ModeCustom, false},
		{"bogus", "", true},
	} {
		got, err := NormalizeMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeMode(%q) 应报错", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeMode(%q) 不应报错: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeMode(%q) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestNormalizeRuleGroup merge 下档位必须合法；其余模式下界面看不到该字段，
// 提交旧值/空值应回落 base 而不是把保存卡死。
func TestNormalizeRuleGroup(t *testing.T) {
	for _, tc := range []struct {
		ruleGroup string
		mode      string
		want      string
		wantErr   bool
	}{
		{RuleGroupBase, ModeMerge, RuleGroupBase, false},
		{RuleGroupFull, ModeMerge, RuleGroupFull, false},
		{"", ModeMerge, "", true},
		{"bogus", ModeMerge, "", true},
		{"", ModeSwitch, RuleGroupBase, false},
		{"bogus", ModeSwitch, RuleGroupBase, false},
		{"", ModeCustom, RuleGroupBase, false},
	} {
		got, err := NormalizeRuleGroup(tc.ruleGroup, tc.mode)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeRuleGroup(%q,%q) 应报错", tc.ruleGroup, tc.mode)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeRuleGroup(%q,%q) 不应报错: %v", tc.ruleGroup, tc.mode, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeRuleGroup(%q,%q) = %q, 期望 %q", tc.ruleGroup, tc.mode, got, tc.want)
		}
	}
}
