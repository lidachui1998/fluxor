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
