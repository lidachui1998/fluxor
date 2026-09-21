package configgen

import (
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// providerFixture 是一份贴近真实机场下发的切换模式配置：
// 自带代理组、规则集与 MATCH 兜底。
const providerFixture = `mixed-port: 7890
mode: rule
proxies:
  - {name: "香港 01", type: ss, server: 127.0.0.1, port: 1, cipher: aes-128-gcm, password: x}
proxy-groups:
  - {name: "🚀 节点选择", type: select, proxies: ["香港 01", DIRECT]}
  - {name: "🛑 广告拦截", type: select, proxies: [REJECT, DIRECT]}
rule-providers:
  ads:
    type: http
    behavior: domain
    url: "https://example.com/ads.mrs"
rules:
  - RULE-SET,ads,🛑 广告拦截
  - DOMAIN-SUFFIX,example.org,🚀 节点选择
  - MATCH,🚀 节点选择
`

func ruleOf(t *testing.T, ruleType, payload, target, position string, noResolve bool) config.CustomRule {
	t.Helper()
	if _, ok := configcheck.LookupRuleSpec(ruleType); !ok {
		t.Fatalf("测试用例使用了白名单外的类型: %s", ruleType)
	}
	return config.CustomRule{Type: ruleType, Payload: payload, Target: target, Position: position, NoResolve: noResolve}
}

// TestApplyCustomRulesAfterAnchor 默认位置：插在最后一条 MATCH 之前。
func TestApplyCustomRulesAfterAnchor(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rules := []config.CustomRule{
		ruleOf(t, "DOMAIN-SUFFIX", "ads.example.com", "REJECT", config.RulePositionAfter, false),
		ruleOf(t, "DOMAIN", "a.com", "🚀 节点选择", config.RulePositionAfter, false),
	}
	result, err := ApplyCustomRules(doc, rules)
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if result.Applied != 2 || len(result.Skipped) != 0 {
		t.Fatalf("期望注入 2 条且无跳过，实际 applied=%d skipped=%v", result.Applied, result.Skipped)
	}

	lines := ruleLines(t, doc)
	want := []string{
		"RULE-SET,ads,🛑 广告拦截",
		"DOMAIN-SUFFIX,example.org,🚀 节点选择",
		"DOMAIN-SUFFIX,ads.example.com,REJECT",
		"DOMAIN,a.com,🚀 节点选择",
		"MATCH,🚀 节点选择",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("after 插入位置错误:\n实际: %v\n期望: %v", lines, want)
	}
}

// TestApplyCustomRulesBeforeAnchor 高优先级位置：插到规则最前。
func TestApplyCustomRulesBeforeAnchor(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rules := []config.CustomRule{
		ruleOf(t, "DOMAIN-KEYWORD", "ads", "DIRECT", config.RulePositionBefore, false),
	}
	if _, err := ApplyCustomRules(doc, rules); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	lines := ruleLines(t, doc)
	if lines[0] != "DOMAIN-KEYWORD,ads,DIRECT" {
		t.Fatalf("before 规则应位于首位，实际首条为 %s", lines[0])
	}
	if lines[len(lines)-1] != "MATCH,🚀 节点选择" {
		t.Fatalf("MATCH 应仍在末位，实际末条为 %s", lines[len(lines)-1])
	}
}

// TestApplyCustomRulesWithoutMatch 订阅没有 MATCH 时追加到末尾。
func TestApplyCustomRulesWithoutMatch(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(`proxies:
  - {name: "香港 01", type: ss}
proxy-groups:
  - {name: "G", type: select, proxies: ["香港 01"]}
rules:
  - DOMAIN,a.com,G
`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if _, err := ApplyCustomRules(doc, []config.CustomRule{
		ruleOf(t, "DOMAIN", "b.com", "G", config.RulePositionAfter, false),
	}); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	lines := ruleLines(t, doc)
	if lines[len(lines)-1] != "DOMAIN,b.com,G" {
		t.Fatalf("无 MATCH 时应追加到末尾，实际: %v", lines)
	}
}

// TestApplyCustomRulesCreatesRulesKey 兼容缺失/为空的 rules 键。
func TestApplyCustomRulesCreatesRulesKey(t *testing.T) {
	for name, body := range map[string]string{
		"缺失 rules": `proxies:
  - {name: "香港 01", type: ss}
proxy-groups:
  - {name: "G", type: select, proxies: ["香港 01"]}
`,
		"rules 为空值": `proxies:
  - {name: "香港 01", type: ss}
proxy-groups:
  - {name: "G", type: select, proxies: ["香港 01"]}
rules:
`,
		"rules 为空列表": `proxies:
  - {name: "香港 01", type: ss}
proxy-groups:
  - {name: "G", type: select, proxies: ["香港 01"]}
rules: []
`,
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := configcheck.ParseDoc([]byte(body))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			result, err := ApplyCustomRules(doc, []config.CustomRule{
				ruleOf(t, "DOMAIN", "a.com", "G", config.RulePositionAfter, false),
			})
			if err != nil {
				t.Fatalf("注入失败: %v", err)
			}
			if result.Applied != 1 {
				t.Fatalf("期望注入 1 条，实际 %d 条（跳过 %v）", result.Applied, result.Skipped)
			}
			out, err := doc.Bytes()
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			if !strings.Contains(string(out), "DOMAIN,a.com,G") {
				t.Fatalf("产物中缺少注入的规则:\n%s", out)
			}
		})
	}
}

// TestApplyCustomRulesIdempotent 重复执行不得累积重复规则。
func TestApplyCustomRulesIdempotent(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rules := []config.CustomRule{
		ruleOf(t, "DOMAIN-SUFFIX", "ads.example.com", "REJECT", config.RulePositionAfter, false),
	}
	if _, err := ApplyCustomRules(doc, rules); err != nil {
		t.Fatalf("首次注入失败: %v", err)
	}
	first, _ := doc.Bytes()

	result, err := ApplyCustomRules(doc, rules)
	if err != nil {
		t.Fatalf("二次注入失败: %v", err)
	}
	if result.Applied != 0 || len(result.Skipped) != 1 {
		t.Fatalf("二次注入应判定为重复，实际 applied=%d skipped=%v", result.Applied, result.Skipped)
	}
	second, _ := doc.Bytes()
	if string(first) != string(second) {
		t.Fatalf("幂等性被破坏:\n首次:\n%s\n二次:\n%s", first, second)
	}
}

// TestApplyCustomRulesSkipsInvalid 目标/规则集失效时跳过而非中断整份配置。
func TestApplyCustomRulesSkipsInvalid(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rules := []config.CustomRule{
		ruleOf(t, "DOMAIN", "a.com", "已改名的组", config.RulePositionAfter, false),
		ruleOf(t, "RULE-SET", "不存在的规则集", "🚀 节点选择", config.RulePositionAfter, false),
		ruleOf(t, "DOMAIN", "b.com", "🚀 节点选择", config.RulePositionAfter, false),
	}
	result, err := ApplyCustomRules(doc, rules)
	if err != nil {
		t.Fatalf("非法规则应被跳过而非报错: %v", err)
	}
	if result.Applied != 1 {
		t.Fatalf("期望仅 1 条可用，实际 %d 条", result.Applied)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("期望 2 条被跳过，实际 %d 条: %v", len(result.Skipped), result.Skipped)
	}
	for _, skip := range result.Skipped {
		if skip.Reason == "" {
			t.Fatal("跳过原因不能为空（前端需要展示）")
		}
	}
	// 被跳过的规则绝不能出现在产物里（否则内核会因目标不存在拒绝加载）
	out, _ := doc.Bytes()
	if strings.Contains(string(out), "已改名的组") || strings.Contains(string(out), "不存在的规则集") {
		t.Fatalf("非法规则被写入了配置:\n%s", out)
	}
}

// TestApplyCustomRulesRejectsBrokenRulesNode rules 结构异常时宁可失败也不改写。
func TestApplyCustomRulesRejectsBrokenRulesNode(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(`proxies:
  - {name: "香港 01", type: ss}
proxy-groups:
  - {name: "G", type: select, proxies: ["香港 01"]}
rules: not-a-list
`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if _, err := ApplyCustomRules(doc, []config.CustomRule{
		ruleOf(t, "DOMAIN", "a.com", "G", config.RulePositionAfter, false),
	}); err == nil {
		t.Fatal("rules 不是列表时必须报错，避免破坏订阅自带规则")
	}
}

// TestApplyCustomRulesToFileNoopWithoutRules 无自定义规则时不读写文件。
//
// 关键点：传入一份「非 Clash 内容」也不会报错，证明函数确实未做读取与解析，
// 切换模式下 config.yaml 保持订阅文件的逐字节副本。
func TestApplyCustomRulesToFileNoopWithoutRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "c3M6Ly9ub3QtYS1jbGFzaC1jb25maWc="
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if _, err := ApplyCustomRulesToFile(path, nil); err != nil {
		t.Fatalf("无规则时应为无操作: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(got) != original {
		t.Fatal("无自定义规则时不应改写文件")
	}
}

// TestApplyCustomRulesToFileWritesRules 覆盖真实落盘链路。
func TestApplyCustomRulesToFileWritesRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(providerFixture), 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	result, err := ApplyCustomRulesToFile(path, []config.CustomRule{
		ruleOf(t, "IP-CIDR", "1.1.1.0/24", "DIRECT", config.RulePositionAfter, true),
		ruleOf(t, "DOMAIN-SUFFIX", "ads.example.com", "REJECT", config.RulePositionBefore, false),
	})
	if err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	if result.Applied != 2 {
		t.Fatalf("期望注入 2 条，实际 %d 条", result.Applied)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	text := string(got)
	if !strings.Contains(text, "IP-CIDR,1.1.1.0/24,DIRECT,no-resolve") {
		t.Fatalf("缺少带 no-resolve 的规则:\n%s", text)
	}
	if !strings.Contains(text, "DOMAIN-SUFFIX,ads.example.com,REJECT") {
		t.Fatalf("缺少 before 规则:\n%s", text)
	}

	// 端到端：产物必须能被真实内核加载
	runMihomoTest(t, filepath.Dir(path))
}

// TestLoadRuleContext 规则上下文：目标/规则集收集与既有规则判重。
func TestLoadRuleContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub.yaml")
	if err := os.WriteFile(path, []byte(providerFixture), 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	ctx, err := LoadRuleContext(path)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if !ctx.Env.ContainsTarget("🚀 节点选择") {
		t.Fatal("应收集到订阅自带的代理组")
	}
	if _, ok := ctx.Env.Providers["ads"]; !ok {
		t.Fatal("应收集到订阅自带的规则集")
	}
	if !ctx.HasRuleLine("DOMAIN-SUFFIX,example.org,🚀 节点选择") {
		t.Fatal("应识别出订阅自带的既有规则")
	}
	if ctx.HasRuleLine("DOMAIN-SUFFIX,new.example.com,🚀 节点选择") {
		t.Fatal("不应把新规则误判为已存在")
	}
}

// TestLoadRuleContextRejectsNonClash 非 Clash 文件必须报错。
func TestLoadRuleContextRejectsNonClash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub.yaml")
	if err := os.WriteFile(path, []byte("c3M6Ly9ub3QtYS1jbGFzaC1jb25maWc="), 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if _, err := LoadRuleContext(path); err == nil {
		t.Fatal("非 Clash 内容必须报错")
	}
}

// ruleLines 取出文档中 rules 序列的文本列表。
func ruleLines(t *testing.T, doc *configcheck.Doc) []string {
	t.Helper()
	seq := doc.Get("rules")
	if seq == nil {
		t.Fatal("文档中没有 rules")
	}
	lines := make([]string, 0, len(seq.Content))
	for _, item := range seq.Content {
		lines = append(lines, item.Value)
	}
	return lines
}

// runMihomoTest 用真实内核二进制校验生成的配置。
//
// 仓库根目录存在 mihomo 时执行；缺失（例如精简的 CI 环境）则跳过。
func runMihomoTest(t *testing.T, dir string) {
	t.Helper()
	bin := filepath.Join("..", "..", "..", "mihomo")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("未找到内核二进制 %s，跳过端到端校验", bin)
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatalf("解析路径失败: %v", err)
	}
	out, err := exec.Command(abs, "-t", "-d", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("内核拒绝加载生成的配置: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "test is successful") {
		t.Fatalf("内核未确认配置可用:\n%s", out)
	}
	t.Logf("内核校验通过: %s", strings.TrimSpace(lastLine(string(out))))
}

// lastLine 返回输出中最后一个非空行，便于在测试日志中定位内核结论。
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return s
}

// TestCustomModeRuleTargets 自定义模式下把手工节点名也算作规则目标（仅该模式）。
//
// 背景：自定义模式的节点写死在 config.yaml 的 proxies 里，规则指向节点名内核能解析；
// 融合模式的节点来自 proxy-providers、运行时才加载，静态校验看不到，因此两者的
// 目标集合按模式区分——这条差异就是「目标下拉在自定义模式下多出节点」的由来。
func TestCustomModeRuleTargets(t *testing.T) {
	customCfg := config.SubscribeConfig{
		Mode: config.ModeCustom,
		CustomNodes: []config.CustomNode{
			{ID: "n1", Name: "自建 香港"},
			{ID: "n2", Name: "自建 日本"},
		},
	}

	ctx, err := CustomModeRuleContext(customCfg)
	if err != nil {
		t.Fatalf("构建自定义模式上下文失败: %v", err)
	}

	// 节点是合法目标（否则规则会在生成时被静默跳过）
	for _, name := range []string{"自建 香港", "自建 日本"} {
		if !ctx.Env.ContainsTarget(name) {
			t.Fatalf("自定义模式下节点 %s 应可作为规则目标", name)
		}
	}
	// 下拉里单独成组：GroupNames 不含节点，NodeNames 才给节点
	if strings.Contains(strings.Join(ctx.GroupNames(), "|"), "自建") {
		t.Fatalf("节点不应混进代理组列表: %v", ctx.GroupNames())
	}
	if got := strings.Join(ctx.NodeNames(), "|"); got != "自建 日本|自建 香港" {
		t.Fatalf("节点列表应为两个手工节点，实际: %s", got)
	}
	// 指向节点的规则能通过校验
	rule := config.CustomRule{Type: "DOMAIN-SUFFIX", Payload: "example.org", Target: "自建 香港", Position: config.RulePositionBefore}
	if _, err := ValidateCustomRule(rule, ctx.Env); err != nil {
		t.Fatalf("指向节点的规则应通过校验: %v", err)
	}
	if skipped := RuleSkips([]config.CustomRule{rule}, ctx); len(skipped) != 0 {
		t.Fatalf("自定义模式下不应把节点目标判为失效: %v", skipped)
	}

	// 融合模式的上下文里没有节点：同一份规则在那里不合法，也不会出现在下拉里
	for _, tier := range MergeRuleSetNames() {
		other, err := MergeRuleSetContext(tier)
		if err != nil {
			t.Fatalf("构建 %s 档位上下文失败: %v", tier, err)
		}
		if len(other.NodeNames()) != 0 {
			t.Fatalf("融合模式 %s 档位不应给出节点目标: %v", tier, other.NodeNames())
		}
		if _, err := ValidateCustomRule(rule, other.Env); err == nil {
			t.Fatalf("融合模式 %s 档位下指向节点的规则应判为失效（该模式没有静态节点）", tier)
		}
	}
}

// TestGroupNamesExcludesNodes 目标下拉只列代理组，不暴露代理节点。
//
// 校验集合（RuleEnv.Targets）仍包含节点：内核确实接受指向节点的规则，
// 若在展示层以外的判断里排除节点，已有规则会被判为失效而静默跳过。
func TestGroupNamesExcludesNodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub.yaml")
	if err := os.WriteFile(path, []byte(providerFixture), 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	ctx, err := LoadRuleContext(path)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}

	groups := strings.Join(ctx.GroupNames(), "|")
	if groups != "🚀 节点选择|🛑 广告拦截" {
		t.Fatalf("目标下拉应恰好包含订阅自带的两个代理组，实际: %s", groups)
	}
	for _, name := range ctx.GroupNames() {
		if name == "香港 01" {
			t.Fatal("代理节点不应出现在目标下拉中")
		}
	}
	if !ctx.Env.ContainsTarget("香港 01") {
		t.Fatal("校验集合仍应包含代理节点，避免既有规则被误判失效")
	}
}

// TestApplyCustomRulesDefaultPositionIsBefore 位置留空时默认插到最前。
func TestApplyCustomRulesDefaultPositionIsBefore(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(providerFixture))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 刻意不设置 Position，模拟老客户端/手改 fluxor.json
	if _, err := ApplyCustomRules(doc, []config.CustomRule{
		{Type: "DOMAIN", Payload: "a.com", Target: "🚀 节点选择"},
	}); err != nil {
		t.Fatalf("注入失败: %v", err)
	}
	lines := ruleLines(t, doc)
	if lines[0] != "DOMAIN,a.com,🚀 节点选择" {
		t.Fatalf("默认应插到最前，实际首条为 %s", lines[0])
	}
	if lines[len(lines)-1] != "MATCH,🚀 节点选择" {
		t.Fatalf("MATCH 仍应在末位，实际末条为 %s", lines[len(lines)-1])
	}
}

// TestMergeRuleSetEnvFromTemplates 档位环境直接来自生成用的模板常量。
//
// 断言的是「模板里确实存在这些组」这一事实关系，而不是把 36 个组名抄一遍：
// 模板增删代理组时这里不需要改，界面与生成也就不会错配。
func TestMergeRuleSetEnvFromTemplates(t *testing.T) {
	baseEnv, err := MergeRuleSetEnv(RuleGroupBase)
	if err != nil {
		t.Fatalf("base 档位环境构建失败: %v", err)
	}
	if !baseEnv.ContainsTarget("🚀 节点选择") || !baseEnv.ContainsTarget("🎯 全球直连") {
		t.Fatal("base 档位应包含模板中的代理组")
	}
	if !baseEnv.ContainsTarget("DIRECT") {
		t.Fatal("内置目标应始终可用")
	}
	if _, ok := baseEnv.Providers["ads"]; ok {
		t.Fatal("base 档位没有 rule-providers，不应出现规则集")
	}

	fullEnv, err := MergeRuleSetEnv(RuleGroupFull)
	if err != nil {
		t.Fatalf("full 档位环境构建失败: %v", err)
	}
	for _, name := range []string{"🚀 节点选择", "🇭🇰 香港节点", "🛑 广告域名", "🔴 全球拦截"} {
		if !fullEnv.ContainsTarget(name) {
			t.Fatalf("full 档位应包含代理组 %s", name)
		}
	}
	for _, name := range []string{"ads", "cn", "gfw", "mediaip"} {
		if _, ok := fullEnv.Providers[name]; !ok {
			t.Fatalf("full 档位应包含规则集 %s", name)
		}
	}

	// 融合模式的节点来自 proxy-providers、运行时才加载，绝不能出现在目标集合里
	for _, env := range []configcheck.RuleEnv{baseEnv, fullEnv} {
		for name := range env.Targets {
			if strings.Contains(name, "节点") && !strings.Contains(name, "节点选择") && !strings.Contains(name, "香港节点") &&
				!strings.Contains(name, "台湾节点") && !strings.Contains(name, "日本节点") &&
				!strings.Contains(name, "新加坡节点") && !strings.Contains(name, "美国节点") {
				t.Fatalf("目标集合不应出现代理节点: %s", name)
			}
		}
	}

	if _, err := MergeRuleSetEnv("nope"); err == nil {
		t.Fatal("未知档位必须报错")
	}
	if !IsValidMergeRuleSet("base") || !IsValidMergeRuleSet("full") || IsValidMergeRuleSet("lite") {
		t.Fatal("IsValidMergeRuleSet 判定有误")
	}
}

// TestMergeRuleSetContextRejectsInvalid 档位内置规则参与判重，且目标按档位校验。
func TestMergeRuleSetContextRejectsInvalid(t *testing.T) {
	baseCtx, err := MergeRuleSetContext(RuleGroupBase)
	if err != nil {
		t.Fatalf("构建 base 上下文失败: %v", err)
	}
	// base 模板自带 GEOSITE,github,🚀 节点选择 等规则
	if !baseCtx.HasRuleLine("GEOSITE,github,🚀 节点选择") {
		t.Fatal("应识别出 base 模板的内置规则")
	}
	// base 档位没有 rule-providers，引用规则集必须失败
	if _, err := ValidateCustomRule(config.CustomRule{
		Type: "RULE-SET", Payload: "ads", Target: "🚀 节点选择",
	}, baseCtx.Env); err == nil {
		t.Fatal("base 档位引用 RULE-SET 应被拒绝")
	}
	// full 档位独有的代理组在 base 下不存在
	if _, err := ValidateCustomRule(config.CustomRule{
		Type: "DOMAIN", Payload: "a.com", Target: "🇭🇰 香港节点",
	}, baseCtx.Env); err == nil {
		t.Fatal("base 档位引用 full 独有代理组应被拒绝")
	}
}

// TestGenerateConfigInjectsMergeCustomRules 融合模式生成时按当前档位注入自定义规则，
// 且只注入当前档位的那一份（另一档位的规则必须保持惰性）。
func TestGenerateConfigInjectsMergeCustomRules(t *testing.T) {
	doc, err := configcheck.ParseDoc([]byte(configTemplate))
	if err != nil {
		t.Fatalf("解析模板失败: %v", err)
	}
	cfg := config.SubscribeConfig{
		RuleGroup: RuleGroupBase,
		MergeCustomRules: map[string][]config.CustomRule{
			RuleGroupBase: {{
				ID: "base-rule", Type: "DOMAIN", Payload: "base.example.com",
				Target: "🎯 全球直连", Position: config.RulePositionBefore,
			}},
			RuleGroupFull: {{
				ID: "full-rule", Type: "DOMAIN", Payload: "full.example.com",
				Target: "🚀 节点选择", Position: config.RulePositionBefore,
			}},
		},
	}
	if err := appendRuleSet(doc, cfg, cfg.RuleGroup); err != nil {
		t.Fatalf("追加规则集失败: %v", err)
	}
	if err := applyCustomRules(doc, cfg.RuleGroup, cfg.MergeCustomRulesFor(cfg.RuleGroup)); err != nil {
		t.Fatalf("注入自定义规则失败: %v", err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, "base.example.com") {
		t.Fatalf("当前档位的规则应被注入:\n%s", text)
	}
	if strings.Contains(text, "full.example.com") {
		t.Fatalf("非当前档位的规则不得进入配置:\n%s", text)
	}
	// base 的规则应插在模板规则之前（默认最前）
	lines := ruleLines(t, doc)
	if lines[0] != "DOMAIN,base.example.com,🎯 全球直连" {
		t.Fatalf("默认应插到最前，实际首条为 %s", lines[0])
	}
}

// TestRuleSkips 目标失效的规则被如实统计（供接口回报）。
func TestRuleSkips(t *testing.T) {
	cfg := config.SubscribeConfig{
		RuleGroup: RuleGroupBase,
		MergeCustomRules: map[string][]config.CustomRule{
			RuleGroupBase: {
				{ID: "ok", Type: "DOMAIN", Payload: "a.com", Target: "🎯 全球直连"},
				{ID: "bad", Type: "DOMAIN", Payload: "b.com", Target: "🇭🇰 香港节点"},
			},
		},
	}
	ctx, err := MergeRuleSetContext(RuleGroupBase)
	if err != nil {
		t.Fatalf("构建上下文失败: %v", err)
	}
	skipped := RuleSkips(cfg.MergeCustomRulesFor(RuleGroupBase), ctx)
	if len(skipped) != 1 || !strings.Contains(skipped[0], "b.com") {
		t.Fatalf("应恰好统计出 1 条失效规则，实际: %v", skipped)
	}
}
