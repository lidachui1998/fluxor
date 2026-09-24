package subscription

import (
	"bytes"
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/configgen"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件守住「保存并应用」最重要的一条契约：
//
//	请求体只提供设置类字段，生成配置的输入一律是**落库后**的 config.Current 快照。
//
// 回归背景：按写入者拆分存储的那次提交删掉了 config.AdoptServerOwnedFields，理由是
// 「SaveSettings 只取设置与订阅注册表，请求体里的过期快照不会覆盖服务端」。这个判断对
// **持久化**成立，却忽略了同一个 cfg 还是**生成输入**；而前端此时已经改成只提交设置类
// 字段（buildSettingsPayload）。两者叠加的结果是：每次点「保存并应用」都会生成一份
// 不含任何自定义规则与隧道的 config.yaml——rules.json 里数据还在、界面也还在显示，
// 只是不再生效，用户没有任何提示（隧道被抹掉则表现为该监听端口不再转发流量）。
//
// 这个用例模拟前端的请求体（只有设置字段），断言生成结果里仍有自定义规则。

// setupGenerateTestEnv 把数据目录、内核工作目录与配置输出路径都指到临时目录。
func setupGenerateTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	prevTarget, prevWorkDir := config.ConfigTarget, config.CoreWorkDir
	prevSocket := config.CoreSocket
	config.SetDataDir(dir)
	config.CoreWorkDir = dir
	config.ConfigTarget = filepath.Join(dir, "config.yaml")
	// 指向一个不存在的 socket：重载必然失败，从而走「status: warning」分支，
	// 也就不会去抓订阅元数据（本用例不涉及网络）。
	config.CoreSocket = filepath.Join(dir, "nonexistent-core.sock")
	t.Cleanup(func() {
		config.ConfigTarget, config.CoreWorkDir = prevTarget, prevWorkDir
		config.CoreSocket = prevSocket
	})

	config.LoadAll()
	return dir
}

// settingsPayload 构造与前端 buildSettingsPayload 等价的请求体。
//
// 关键点是**不含** merge_custom_rules / custom_mode_rules / merge_tunnels /
// subscriptions[].custom_rules|tunnels——这正是线上真实请求的形态。
func settingsPayload(cfg config.SubscribeConfig) []byte {
	subs := make([]map[string]any, 0, len(cfg.Subscriptions))
	for _, s := range cfg.Subscriptions {
		subs = append(subs, map[string]any{
			"name":            s.Name,
			"url":             s.URL,
			"update_interval": s.UpdateInterval,
			"health_interval": s.HealthInterval,
			"prefix":          s.Prefix,
		})
	}
	// 手工节点要带上：该接口按契约「整体覆盖」节点列表，前端也确实回传完整节点
	// （nodespec 补齐后的形态；diff 形态也能被 normalizeCustomNodes 接受）
	nodes := make([]map[string]any, 0, len(cfg.CustomNodes))
	for _, n := range cfg.CustomNodes {
		nodes = append(nodes, map[string]any{
			"id":     n.ID,
			"name":   n.Name,
			"type":   n.Type,
			"config": n.Config,
		})
	}
	body := map[string]any{
		"proxy_port":          cfg.ProxyPort,
		"panel_port":          cfg.PanelPort,
		"tproxy_port":         cfg.TproxyPort,
		"panel_secret":        cfg.PanelSecret,
		"rule_group":          cfg.RuleGroup,
		"ui_panel":            cfg.UIPanel,
		"meta_backend_url":    cfg.MetaBackendURL,
		"mode":                cfg.Mode,
		"active_subscription": cfg.ActiveSubscription,
		"custom_nodes":        nodes,
		"subscriptions":       subs,
	}
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return data
}

// postGenerate 以「只含设置字段」的请求体调用 /subscribe/generate。
func postGenerate(t *testing.T, cfg config.SubscribeConfig) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/subscribe/generate", bytes.NewReader(settingsPayload(cfg)))
	rec := httptest.NewRecorder()
	HandleGenerateConfig(rec, req)
	return rec
}

// baseSeedConfig 一份融合模式 + base 档位 + 单个订阅的种子配置。
func baseSeedConfig() config.SubscribeConfig {
	return config.SubscribeConfig{
		ProxyPort:  7890,
		PanelPort:  9090,
		TproxyPort: 7898,
		Mode:       config.ModeMerge,
		RuleGroup:  config.RuleGroupBase,
		UIPanel:    "metacubexd",
		Subscriptions: []config.Subscription{
			{Name: "机场A", URL: "https://example.invalid/sub"},
		},
	}
}

// addMergeRule 往 base 档位里落一条真实可用的自定义规则，返回其规则行。
func addMergeRule(t *testing.T, payload string) string {
	t.Helper()
	ctx, err := configgen.MergeRuleSetContext(config.RuleGroupBase)
	if err != nil {
		t.Fatalf("取 base 档位规则上下文失败: %v", err)
	}
	groups := ctx.GroupNames()
	if len(groups) == 0 {
		t.Fatal("base 模板应至少有一个代理组，否则本用例没有可用目标")
	}
	rule, err := prepareCustomRule(config.CustomRule{
		Type:     "DOMAIN-SUFFIX",
		Payload:  payload,
		Target:   groups[0],
		Position: config.RulePositionBefore,
	}, ctx, nil, "")
	if err != nil {
		t.Fatalf("规则校验失败: %v", err)
	}
	if err := config.UpdateTemplateRules(config.RuleGroupBase,
		func(cur []config.CustomRule) ([]config.CustomRule, error) {
			return config.SortCustomRulesForDisplay(append(cur, rule)), nil
		}); err != nil {
		t.Fatalf("落库规则失败: %v", err)
	}
	line, err := configgen.ValidateCustomRule(rule, ctx.Env)
	if err != nil {
		t.Fatalf("规则行组装失败: %v", err)
	}
	return line
}

// TestGenerateKeepsMergeCustomRules 融合模式：请求体不带规则时，生成结果仍必须含规则。
func TestGenerateKeepsMergeCustomRules(t *testing.T) {
	setupGenerateTestEnv(t)

	seed := baseSeedConfig()
	if err := config.SaveSettings(seed); err != nil {
		t.Fatalf("播种设置失败: %v", err)
	}
	wantLine := addMergeRule(t, "keep-rule-test.example")

	rec := postGenerate(t, seed)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存并应用应返回 200，实际 %d，body=%s", rec.Code, rec.Body.String())
	}

	generated, err := os.ReadFile(config.ConfigTarget)
	if err != nil {
		t.Fatalf("应生成 config.yaml: %v", err)
	}
	if !strings.Contains(string(generated), wantLine) {
		t.Fatalf("生成结果丢失了自定义规则（期望含 %q）：\n%s", wantLine, generated)
	}
}

// TestGenerateKeepsMergeCustomTunnels 隧道同理：它被抹掉意味着该监听端口不再转发流量。
func TestGenerateKeepsMergeCustomTunnels(t *testing.T) {
	setupGenerateTestEnv(t)

	seed := baseSeedConfig()
	if err := config.SaveSettings(seed); err != nil {
		t.Fatalf("播种设置失败: %v", err)
	}

	ctx, err := configgen.MergeRuleSetContext(config.RuleGroupBase)
	if err != nil {
		t.Fatalf("取规则上下文失败: %v", err)
	}
	groups := ctx.GroupNames()
	if len(groups) == 0 {
		t.Fatal("base 模板应至少有一个代理组")
	}
	const listenAddr = "127.0.0.1:17890"
	const targetAddr = "1.1.1.1:53"
	tunnel := config.Tunnel{
		ID:      "tunnel-test-1",
		Network: []string{config.TunnelNetworkTCP},
		Address: listenAddr,
		Target:  targetAddr,
		Proxy:   groups[0],
		Enabled: boolPtr(true),
	}
	if err := config.UpdateTemplateTunnels(config.RuleGroupBase,
		func(cur []config.Tunnel) ([]config.Tunnel, error) {
			return append(cur, tunnel), nil
		}); err != nil {
		t.Fatalf("落库隧道失败: %v", err)
	}

	rec := postGenerate(t, seed)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存并应用应返回 200，实际 %d，body=%s", rec.Code, rec.Body.String())
	}

	generated, err := os.ReadFile(config.ConfigTarget)
	if err != nil {
		t.Fatalf("应生成 config.yaml: %v", err)
	}
	if !strings.Contains(string(generated), listenAddr) || !strings.Contains(string(generated), targetAddr) {
		t.Fatalf("生成结果丢失了流量隧道（期望含 %s → %s）：\n%s", listenAddr, targetAddr, generated)
	}
}

// TestGenerateKeepsCustomModeRules 自定义模式同样从落库快照取规则。
func TestGenerateKeepsCustomModeRules(t *testing.T) {
	setupGenerateTestEnv(t)

	seed := config.SubscribeConfig{
		ProxyPort:  7890,
		PanelPort:  9090,
		TproxyPort: 7898,
		Mode:       config.ModeCustom,
		RuleGroup:  config.RuleGroupBase,
		UIPanel:    "metacubexd",
		CustomNodes: []config.CustomNode{
			{ID: "node-1", Name: "手工节点A", Type: "ss", Config: map[string]any{
				"server": "127.0.0.1", "port": 8388, "cipher": "aes-128-gcm", "password": "p",
			}},
		},
	}
	if err := config.SaveSettings(seed); err != nil {
		t.Fatalf("播种设置失败: %v", err)
	}

	// 自定义模式的规则可以指向手工节点名，这里直接指向它，顺带覆盖目标集合
	ctx, err := configgen.CustomModeRuleContext(config.CurrentSnapshot())
	if err != nil {
		t.Fatalf("取自定义模式规则上下文失败: %v", err)
	}
	rule, err := prepareCustomRule(config.CustomRule{
		Type:     "DOMAIN-SUFFIX",
		Payload:  "custom-mode-rule.example",
		Target:   "手工节点A",
		Position: config.RulePositionBefore,
	}, ctx, nil, "")
	if err != nil {
		t.Fatalf("规则校验失败: %v", err)
	}
	if err := config.UpdateTemplateRules(config.RuleScopeCustom,
		func(cur []config.CustomRule) ([]config.CustomRule, error) {
			return config.SortCustomRulesForDisplay(append(cur, rule)), nil
		}); err != nil {
		t.Fatalf("落库规则失败: %v", err)
	}
	wantLine, err := configgen.ValidateCustomRule(rule, ctx.Env)
	if err != nil {
		t.Fatalf("规则行组装失败: %v", err)
	}

	rec := postGenerate(t, seed)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存并应用应返回 200，实际 %d，body=%s", rec.Code, rec.Body.String())
	}

	generated, err := os.ReadFile(config.ConfigTarget)
	if err != nil {
		t.Fatalf("应生成 config.yaml: %v", err)
	}
	if !strings.Contains(string(generated), wantLine) {
		t.Fatalf("生成结果丢失了自定义模式的规则（期望含 %q）：\n%s", wantLine, generated)
	}
	if !strings.Contains(string(generated), "手工节点A") {
		t.Fatalf("生成结果丢失了手工节点：\n%s", generated)
	}
}

func boolPtr(v bool) *bool { return &v }
