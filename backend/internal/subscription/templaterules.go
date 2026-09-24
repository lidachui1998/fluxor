package subscription

import (
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"fluxor/internal/configgen"
	"fluxor/internal/core"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"net/http"
	"strconv"
	"strings"
)

// 本文件是「模板级自定义规则」的公共实现：融合模式的 base/full 档位与自定义模式
// 各是一个作用域（ruleScope），共用同一套增/改/排序/删与响应组装逻辑。
//
// 三种作用域（另有切换模式的订阅级规则，见 customrules.go）在存放位置、可用模式、
// 生效判定与目标集合上都不同，但事务顺序完全一致：
//   落盘该作用域的 store → 若作用域当前生效则重新生成 config.yaml → 热重载内核
// 因此把差异收敛进 ruleScope，把流程只写一遍——两份拷贝迟早会在某次改动后不一致。

// ruleScope 描述一个模板级自定义规则作用域。
type ruleScope struct {
	// name 作用域标识（base / full / custom）：接口路径、日志与提示文案都用它。
	name string
	// rules 取该作用域当前的规则（快照与锁内读都走它，避免两处各写一份取法）。
	rules func(cfg config.SubscribeConfig) []config.CustomRule
	// tunnels 取该作用域当前的流量隧道（只读）。
	//
	// 规则接口的响应必须带上隧道：两个接口服务同一个作用域（同一个弹窗），响应体同构，
	// 否则规则接口的返回值会把前端刚加载到的隧道列表覆盖成空。
	tunnels func(cfg config.SubscribeConfig) []config.Tunnel
	// editable 判定「当前模式下该作用域是否可编辑」。
	editable func(cfg config.SubscribeConfig) bool
	// disabledHint 不可编辑时的提示（各入口指向对方入口，用户才知道该去哪儿改）。
	disabledHint string
	// active 判定该作用域当前是否生效（决定改动后要不要重新生成配置）。
	active func(cfg config.SubscribeConfig) bool
	// regenerate 重新生成运行配置（融合模式 GenerateConfig，自定义模式 GenerateCustomConfig）。
	regenerate func(cfg config.SubscribeConfig) error
	// context 该作用域的规则上下文（合法目标、规则集、已存在规则行）。
	context func(cfg config.SubscribeConfig) (*configgen.RuleContext, error)
}

// mergeRuleScope 返回融合模式某规则集档位的作用域；档位非法时返回 false。
func mergeRuleScope(ruleGroup string) (ruleScope, bool) {
	if !configgen.IsValidMergeRuleSet(ruleGroup) {
		return ruleScope{}, false
	}
	return ruleScope{
		name: ruleGroup,
		rules: func(cfg config.SubscribeConfig) []config.CustomRule {
			return cfg.MergeCustomRulesFor(ruleGroup)
		},
		tunnels: func(cfg config.SubscribeConfig) []config.Tunnel {
			return cfg.MergeTunnelsFor(ruleGroup)
		},
		editable: func(cfg config.SubscribeConfig) bool {
			return cfg.Mode == config.ModeMerge
		},
		disabledHint: "自定义规则仅在融合模式下可用（切换模式请用订阅卡片上的入口，自定义模式请切到自定义模式）",
		active: func(cfg config.SubscribeConfig) bool {
			return cfg.Mode == config.ModeMerge && cfg.RuleGroup == ruleGroup
		},
		regenerate: configgen.GenerateConfig,
		context: func(config.SubscribeConfig) (*configgen.RuleContext, error) {
			return configgen.MergeRuleSetContext(ruleGroup)
		},
	}, true
}

// customModeRuleScope 返回自定义模式的作用域。
//
// 与融合模式档位的两点不同：规则存在 `custom_mode_rules`（不按档位分表），
// 目标里额外包含手工节点名（该模式的节点是 config.yaml 里静态的 proxies）。
func customModeRuleScope() ruleScope {
	return ruleScope{
		name: config.RuleScopeCustom,
		rules: func(cfg config.SubscribeConfig) []config.CustomRule {
			return cfg.CustomModeRules
		},
		tunnels: func(cfg config.SubscribeConfig) []config.Tunnel {
			return cfg.CustomModeTunnels
		},
		editable: func(cfg config.SubscribeConfig) bool {
			return cfg.Mode == config.ModeCustom
		},
		disabledHint: "自定义规则仅在自定义模式下可用（融合模式请用标题行的入口，切换模式请用订阅卡片上的入口）",
		active: func(cfg config.SubscribeConfig) bool {
			return cfg.Mode == config.ModeCustom
		},
		regenerate: configgen.GenerateCustomConfig,
		context: func(cfg config.SubscribeConfig) (*configgen.RuleContext, error) {
			return configgen.CustomModeRuleContext(cfg)
		},
	}
}

// serveRuleRequest 是模板级规则接口的统一入口：校验可编辑性后按方法分发。
func serveRuleRequest(w http.ResponseWriter, r *http.Request, scope ruleScope) {
	if !scope.editable(ruleConfigSnapshot()) {
		httpx.WriteJSONError(w, http.StatusBadRequest, scope.disabledHint)
		return
	}

	switch r.Method {
	case http.MethodGet:
		httpx.RespondJSON(w, http.StatusOK, buildRulesPayload(ruleConfigSnapshot(), scope))
	case http.MethodPost:
		handleAddRule(w, r, scope)
	case http.MethodPut:
		handleUpdateRule(w, r, scope)
	case http.MethodPatch:
		handleMoveRule(w, r, scope)
	case http.MethodDelete:
		handleDeleteRule(w, r, scope)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleAddRule 新增一条规则，追加到该作用域列表末尾（同分组内优先级最低）。
func handleAddRule(w http.ResponseWriter, r *http.Request, scope ruleScope) {
	cfg := ruleConfigSnapshot()

	var input config.CustomRule
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
		return
	}
	input.ID = newCustomRuleID()

	ctx, err := scope.context(cfg)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := prepareCustomRule(input, ctx, scope.rules(cfg), "")
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 「追加到列表」这一步在写锁内完成：闭包拿到的 cur 是**此刻**存储里的列表，
	// 因此同一作用域的并发新增不会互相覆盖（此前用「请求快照 + 整份落盘」存在丢失更新的窗口）。
	// 判重仍在锁外用快照做：极端竞态下可能漏过一条重复规则，内核能容忍重复规则行，不做额外处理。
	err = config.UpdateTemplateRules(scope.name, func(cur []config.CustomRule) ([]config.CustomRule, error) {
		return config.SortCustomRulesForDisplay(appendCopied(cur, rule)), nil
	})
	if err != nil {
		logx.Error(logx.ModuleRule, "saving custom rules failed: scope=%s: %v", scope.name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}
	respondAfterTemplateRuleMutation(w, scope)
}

// handleUpdateRule 修改一条已存在的规则（就地替换，保持其在列表中的位置）。
func handleUpdateRule(w http.ResponseWriter, r *http.Request, scope ruleScope) {
	cfg := ruleConfigSnapshot()

	var input config.CustomRule
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
		return
	}
	input.ID = strings.TrimSpace(input.ID)
	if input.ID == "" {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少规则 id")
		return
	}
	if !containsRuleID(scope.rules(cfg), input.ID) {
		httpx.WriteJSONError(w, http.StatusNotFound, "规则不存在: "+input.ID)
		return
	}

	ctx, err := scope.context(cfg)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 判重时排除自身：未改动的规则行必然与自身相同
	rule, err := prepareCustomRule(input, ctx, scope.rules(cfg), input.ID)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 就地替换在写锁内完成：cur 是此刻的列表，并发改动不会互相覆盖
	var notFound bool
	err = config.UpdateTemplateRules(scope.name, func(cur []config.CustomRule) ([]config.CustomRule, error) {
		out := copyRules(cur)
		idx := -1
		for i := range out {
			if out[i].ID == rule.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			// 锁外的存在性校验到这里之间被并发删除：不落盘，按 404 如实告知
			notFound = true
			return out, nil
		}
		out[idx] = rule
		// 插入位置可能被改动过（最前 <-> 末尾），重排以保持「持久化顺序 = 展示顺序」
		return config.SortCustomRulesForDisplay(out), nil
	})
	if err != nil {
		logx.Error(logx.ModuleRule, "saving custom rules failed: scope=%s: %v", scope.name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}
	if notFound {
		httpx.WriteJSONError(w, http.StatusNotFound, "规则不存在: "+input.ID)
		return
	}
	respondAfterTemplateRuleMutation(w, scope)
}

// handleMoveRule 在列表内上移/下移一条规则（只在同一插入位置分组内交换）。
func handleMoveRule(w http.ResponseWriter, r *http.Request, scope ruleScope) {
	var input moveRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
		return
	}
	direction := strings.ToLower(strings.TrimSpace(input.Direction))
	if direction != config.RuleMoveUp && direction != config.RuleMoveDown {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的移动方向: "+input.Direction)
		return
	}

	// 组内相邻交换在写锁内完成：moved 的语义由 config.MoveCustomRule 给出（边界返回 false）
	var moved bool
	err := config.UpdateTemplateRules(scope.name, func(cur []config.CustomRule) ([]config.CustomRule, error) {
		out, ok := config.MoveCustomRule(cur, input.ID, direction)
		moved = ok
		return out, nil
	})
	if err != nil {
		logx.Error(logx.ModuleRule, "saving custom rules failed: scope=%s: %v", scope.name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}
	if !moved {
		// 边界或 id 不存在：不改变任何状态，如实告知而不是假装成功
		httpx.WriteJSONError(w, http.StatusBadRequest, "规则已在该分组的最前/最后，无法继续移动")
		return
	}
	respondAfterTemplateRuleMutation(w, scope)
}

// handleDeleteRule 删除一条规则。
func handleDeleteRule(w http.ResponseWriter, r *http.Request, scope ruleScope) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少规则 id")
		return
	}

	// 过滤在写锁内完成：found 由闭包给出，删除不存在的 id 仍按 404 如实告知
	var found bool
	err := config.UpdateTemplateRules(scope.name, func(cur []config.CustomRule) ([]config.CustomRule, error) {
		kept := make([]config.CustomRule, 0, len(cur))
		for _, rule := range cur {
			if rule.ID == id {
				found = true
				continue
			}
			kept = append(kept, rule)
		}
		return kept, nil
	})
	if err != nil {
		logx.Error(logx.ModuleRule, "saving custom rules failed: scope=%s: %v", scope.name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}
	if !found {
		httpx.WriteJSONError(w, http.StatusNotFound, "规则不存在: "+id)
		return
	}
	respondAfterTemplateRuleMutation(w, scope)
}

// respondAfterTemplateRuleMutation 落盘之后统一收尾：按需重生成运行配置并返回最新列表。
func respondAfterTemplateRuleMutation(w http.ResponseWriter, scope ruleScope) {
	status, message := applyRulesToActiveConfig(scope)

	payload := buildRulesPayload(ruleConfigSnapshot(), scope)
	payload.Status = status
	payload.Message = message
	httpx.RespondJSON(w, http.StatusOK, payload)
}

// applyRulesToActiveConfig 在改动规则后同步运行配置。
//
// 仅当该作用域当前生效时才需要动作：
//   - 融合模式：该档位正是当前生效的 rule_group；
//   - 自定义模式：恒生效（该模式只有这一份规则）。
//
// 其余情况（另一档位，或当前模式用不到该作用域）都不会改变当前 config.yaml。
func applyRulesToActiveConfig(scope ruleScope) (string, string) {
	config.Mu.RLock()
	cfg := config.Current
	config.Mu.RUnlock()

	if !scope.active(cfg) {
		return "ok", ""
	}

	if err := scope.regenerate(cfg); err != nil {
		logx.Error(logx.ModuleRule, "regenerate config failed: mode=%s scope=%s: %v", cfg.Mode, scope.name, err)
		return "warning", "规则已保存，但重新生成配置文件失败: " + err.Error()
	}

	warning := ""
	if ctx, err := scope.context(cfg); err == nil {
		if skipped := configgen.RuleSkips(scope.rules(cfg), ctx); len(skipped) > 0 {
			warning = "有 " + strconv.Itoa(len(skipped)) + " 条规则因目标不存在未写入配置"
			logx.Warn(logx.ModuleRule, "custom rules not written to config: mode=%s scope=%s count=%d", cfg.Mode, scope.name, len(skipped))
		}
	}

	// 内核未运行时只更新 config.yaml（下次启动即生效），不做重载
	if !core.IsCoreRunning() {
		return "ok", warning
	}
	if err := core.ReloadCore(); err != nil {
		logx.Warn(logx.ModuleRule, "reload core failed: %v", err)
		return "warning", joinMessage(warning, "内核重载失败: "+err.Error())
	}
	return "ok", warning
}

// buildRulesPayload 组装规则列表响应（三种作用域结构一致，仅可选目标不同）。
//
// 响应同时带上该作用域的流量隧道：规则接口与隧道接口服务的是同一个作用域（同一个弹窗），
// 前端拿到任一响应都会整份刷新，两者必须同构，否则后到的响应会把另一个列表抹成空。
func buildRulesPayload(cfg config.SubscribeConfig, scope ruleScope) customRulesPayload {
	payload := customRulesPayload{
		// 模板级作用域不依赖订阅文件：规则目标来自模板与手工节点，恒定可校验
		FileReady: true,
		Rules:     []customRuleView{},
		Tunnels:   []tunnelView{},
		Groups:    []string{},
		Nodes:     []string{},
		Builtins:  configcheck.BuiltinRuleTargets(),
		Providers: []string{},
		RuleTypes: configcheck.RuleSpecs(),
	}

	ctx, err := scope.context(cfg)
	if err != nil {
		// 模板常量编译在二进制里，正常不会失败；真失败时如实下发 file_ready=false，
		// 让前端禁用表单而不是给出一个「可选目标为空」的假象
		payload.FileReady = false
		return payload
	}

	payload.Groups = ctx.GroupNames()
	payload.Nodes = ctx.NodeNames()
	payload.Providers = ctx.ProviderNames()
	payload.Tunnels = buildTunnelViews(scope.tunnels(cfg), ctx, "")
	for _, rule := range config.SortCustomRulesForDisplay(scope.rules(cfg)) {
		view := customRuleView{CustomRule: rule, Line: displayRuleLine(rule)}
		if line, err := configgen.ValidateCustomRule(rule, ctx.Env); err == nil {
			view.Line = line
			view.Valid = true
		} else {
			// 失效规则（如模板改版后代理组消失）仍要展示，供用户修正或删除
			view.Reason = err.Error()
		}
		payload.Rules = append(payload.Rules, view)
	}
	return payload
}

// ruleConfigSnapshot 取当前配置的值拷贝（Subscriptions / 规则切片与全局共享底层数组，
// 故调用方只应读取标量与本请求内刚复制出的切片，不得在锁外就地改动）。
func ruleConfigSnapshot() config.SubscribeConfig {
	config.Mu.RLock()
	defer config.Mu.RUnlock()
	return config.Current
}

// appendCopied 复制一份规则切片再追加，避免与全局快照共享底层数组。
func appendCopied(rules []config.CustomRule, rule config.CustomRule) []config.CustomRule {
	out := make([]config.CustomRule, 0, len(rules)+1)
	out = append(out, rules...)
	return append(out, rule)
}

// copyRules 复制一份规则切片（写操作前先复制，别就地改全局切片）。
func copyRules(rules []config.CustomRule) []config.CustomRule {
	out := make([]config.CustomRule, len(rules))
	copy(out, rules)
	return out
}

// containsRuleID 判定规则 id 是否存在于给定列表。
func containsRuleID(rules []config.CustomRule, id string) bool {
	for _, rule := range rules {
		if rule.ID == id {
			return true
		}
	}
	return false
}
