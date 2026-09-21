package subscription

import (
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"fluxor/internal/configgen"
	"fluxor/internal/core"
	"fluxor/internal/httpx"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// mergeCustomRulesRoutePath 是融合模式自定义规则接口的路径前缀。
const mergeCustomRulesRoutePath = "/subscribe/merge-custom-rules/"

// HandleMergeCustomRulesAPI 处理模板级（按规则集档位存放）的自定义规则：
//
//	GET    /subscribe/merge-custom-rules/{ruleGroup}        查询该规则集的规则与可选目标
//	POST   /subscribe/merge-custom-rules/{ruleGroup}        新增一条规则
//	PUT    /subscribe/merge-custom-rules/{ruleGroup}        修改一条已存在的规则（body 带 id）
//	PATCH  /subscribe/merge-custom-rules/{ruleGroup}        调整顺序（{id, direction}）
//	DELETE /subscribe/merge-custom-rules/{ruleGroup}?id=xx  删除一条规则
//
// {ruleGroup} 取 base（标准）或 full（详细）——两档的代理组、规则集与内置规则都不同，
// 因此规则分别存放在 cfg.MergeCustomRules[ruleGroup]，互不影响。
//
// 可用模式：融合模式（两档皆可）与自定义模式（仅标准档位，其模板固定使用标准规则集，
// 见 mergeRuleGroupAllowed）。
//
// 所有写操作即时持久化到 fluxor.json；若该档位正是当前生效的 rule_group，
// 则重新生成 config.yaml 并热重载内核。弹窗本身不需要「保存」动作。
func HandleMergeCustomRulesAPI(w http.ResponseWriter, r *http.Request) {
	ruleGroup, ok := mergeRuleGroupParam(r)
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少规则集参数")
		return
	}
	if !configgen.IsValidMergeRuleSet(ruleGroup) {
		httpx.WriteJSONError(w, http.StatusBadRequest, "未知规则集: "+ruleGroup)
		return
	}

	switch r.Method {
	case http.MethodGet:
		httpx.RespondJSON(w, http.StatusOK, buildMergeRulesPayload(mergeConfigSnapshot(), ruleGroup))

	case http.MethodPost:
		handleAddMergeRule(w, r, ruleGroup)

	case http.MethodPut:
		handleUpdateMergeRule(w, r, ruleGroup)

	case http.MethodPatch:
		handleMoveMergeRule(w, r, ruleGroup)

	case http.MethodDelete:
		handleDeleteMergeRule(w, r, ruleGroup)

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleAddMergeRule 新增一条规则，追加到该档位列表末尾（同分组内优先级最低）。
func handleAddMergeRule(w http.ResponseWriter, r *http.Request, ruleGroup string) {
	cfg := mergeConfigSnapshot()
	if !mergeRuleGroupAllowed(cfg, ruleGroup) {
		httpx.WriteJSONError(w, http.StatusBadRequest, "该规则集在当前模式下不可编辑（切换模式请用订阅卡片上的入口）")
		return
	}

	var input config.CustomRule
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
		return
	}
	input.ID = newCustomRuleID()

	ctx, err := configgen.MergeRuleSetContext(ruleGroup)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := prepareCustomRule(input, ctx, cfg.MergeCustomRulesFor(ruleGroup), "")
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	config.Mu.Lock()
	ensureMergeRulesMap()
	config.Current.MergeCustomRules[ruleGroup] =
		config.SortCustomRulesForDisplay(append(config.Current.MergeCustomRules[ruleGroup], rule))
	config.Mu.Unlock()

	respondAfterMergeMutation(w, ruleGroup)
}

// handleUpdateMergeRule 修改一条已存在的规则（就地替换，保持其在列表中的位置）。
func handleUpdateMergeRule(w http.ResponseWriter, r *http.Request, ruleGroup string) {
	cfg := mergeConfigSnapshot()
	if !mergeRuleGroupAllowed(cfg, ruleGroup) {
		httpx.WriteJSONError(w, http.StatusBadRequest, "该规则集在当前模式下不可编辑（切换模式请用订阅卡片上的入口）")
		return
	}

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
	if !containsRuleID(cfg.MergeCustomRulesFor(ruleGroup), input.ID) {
		httpx.WriteJSONError(w, http.StatusNotFound, "规则不存在: "+input.ID)
		return
	}

	ctx, err := configgen.MergeRuleSetContext(ruleGroup)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 判重时排除自身：未改动的规则行必然与自身相同
	rule, err := prepareCustomRule(input, ctx, cfg.MergeCustomRulesFor(ruleGroup), input.ID)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	config.Mu.Lock()
	ensureMergeRulesMap()
	rules := config.Current.MergeCustomRules[ruleGroup]
	for i := range rules {
		if rules[i].ID == rule.ID {
			rules[i] = rule
			break
		}
	}
	// 插入位置可能被改动过（最前 <-> 末尾），重排以保持「持久化顺序 = 展示顺序」
	config.Current.MergeCustomRules[ruleGroup] = config.SortCustomRulesForDisplay(rules)
	config.Mu.Unlock()

	respondAfterMergeMutation(w, ruleGroup)
}

// handleMoveMergeRule 在列表内上移/下移一条规则（只在同一插入位置分组内交换）。
func handleMoveMergeRule(w http.ResponseWriter, r *http.Request, ruleGroup string) {
	cfg := mergeConfigSnapshot()
	if !mergeRuleGroupAllowed(cfg, ruleGroup) {
		httpx.WriteJSONError(w, http.StatusBadRequest, "该规则集在当前模式下不可编辑（切换模式请用订阅卡片上的入口）")
		return
	}

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

	moved := false
	config.Mu.Lock()
	ensureMergeRulesMap()
	if rules, ok := config.MoveCustomRule(config.Current.MergeCustomRules[ruleGroup], input.ID, direction); ok {
		config.Current.MergeCustomRules[ruleGroup] = rules
		moved = true
	}
	config.Mu.Unlock()

	if !moved {
		// 边界或 id 不存在：不改变任何状态，如实告知而不是假装成功
		httpx.WriteJSONError(w, http.StatusBadRequest, "规则已在该分组的最前/最后，无法继续移动")
		return
	}

	respondAfterMergeMutation(w, ruleGroup)
}

// handleDeleteMergeRule 删除一条规则。
func handleDeleteMergeRule(w http.ResponseWriter, r *http.Request, ruleGroup string) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少规则 id")
		return
	}
	removed := false
	config.Mu.Lock()
	ensureMergeRulesMap()
	rules := config.Current.MergeCustomRules[ruleGroup]
	for i := range rules {
		if rules[i].ID == id {
			config.Current.MergeCustomRules[ruleGroup] = append(rules[:i:i], rules[i+1:]...)
			removed = true
			break
		}
	}
	config.Mu.Unlock()

	if !removed {
		httpx.WriteJSONError(w, http.StatusNotFound, "规则不存在: "+id)
		return
	}

	respondAfterMergeMutation(w, ruleGroup)
}

// respondAfterMergeMutation 持久化之后统一收尾：按需重生成运行配置并返回最新列表。
func respondAfterMergeMutation(w http.ResponseWriter, ruleGroup string) {
	if err := config.SaveSubscribeConfig(); err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}

	status, message := applyMergeRulesToActiveConfig(ruleGroup)

	payload := buildMergeRulesPayload(mergeConfigSnapshot(), ruleGroup)
	payload.Status = status
	payload.Message = message
	httpx.RespondJSON(w, http.StatusOK, payload)
}

// mergeRuleGroupAllowed 判定当前模式下能否读写某档位的模板级自定义规则。
//
//   - 融合模式：base 与 full 两档都可编辑（各自对应一套模板）；
//   - 自定义模式：只开放标准档位——自定义模式的模板固定使用标准规则集，其自定义规则
//     与「融合模式 + 标准档位」共用同一份列表（代理组与内置规则集合完全相同，
//     规则在两边都成立）；
//   - 切换模式：规则挂在订阅上，走 /subscribe/custom-rules/{name}。
func mergeRuleGroupAllowed(cfg config.SubscribeConfig, ruleGroup string) bool {
	switch cfg.Mode {
	case config.ModeMerge:
		return true
	case config.ModeCustom:
		return ruleGroup == config.RuleGroupBase
	default:
		return false
	}
}

// applyMergeRulesToActiveConfig 在改动规则后同步运行配置。
//
// 仅当该档位的规则真的进入运行配置时才需要动作：
//   - 融合模式：该档位正是当前生效的 rule_group；
//   - 自定义模式：标准档位（自定义模式固定使用标准规则集）。
//
// 其余情况（另一档位，或订阅级规则）都不会改变当前 config.yaml。
func applyMergeRulesToActiveConfig(ruleGroup string) (string, string) {
	config.Mu.RLock()
	cfg := config.Current
	config.Mu.RUnlock()

	custom := cfg.Mode == config.ModeCustom && ruleGroup == config.RuleGroupBase
	active := custom || (cfg.Mode == config.ModeMerge && cfg.RuleGroup == ruleGroup)
	if !active {
		return "ok", ""
	}

	// 两种模式的产物不同：融合模式带 proxy-providers，自定义模式拼 proxies 块
	generate := configgen.GenerateConfig
	if custom {
		generate = configgen.GenerateCustomConfig
	}
	if err := generate(cfg); err != nil {
		log.Printf("[CUSTOM-RULE] %s（%s）重新生成配置失败: %v", cfg.Mode, ruleGroup, err)
		return "warning", "规则已保存，但重新生成配置文件失败: " + err.Error()
	}

	warning := ""
	if skipped := configgen.MergeRuleSkips(cfg, ruleGroup); len(skipped) > 0 {
		warning = "有 " + strconv.Itoa(len(skipped)) + " 条规则因目标不存在未写入配置"
		log.Printf("[CUSTOM-RULE] %s（%s）有 %d 条规则未写入配置", cfg.Mode, ruleGroup, len(skipped))
	}

	// 内核未运行时只更新 config.yaml（下次启动即生效），不做重载
	if !core.IsCoreRunning() {
		return "ok", warning
	}
	if err := core.ReloadCore(); err != nil {
		log.Printf("[CUSTOM-RULE] 重载内核失败: %v", err)
		return "warning", joinMessage(warning, "内核重载失败: "+err.Error())
	}
	return "ok", warning
}

// buildMergeRulesPayload 组装融合模式自定义规则的响应（结构与切换模式一致）。
func buildMergeRulesPayload(cfg config.SubscribeConfig, ruleGroup string) customRulesPayload {
	payload := customRulesPayload{
		// 融合模式不依赖订阅文件：规则目标来自档位模板，恒定可校验
		FileReady: true,
		Rules:     []customRuleView{},
		Groups:    []string{},
		Builtins:  configcheck.BuiltinRuleTargets(),
		Providers: []string{},
		RuleTypes: configcheck.RuleSpecs(),
	}

	ctx, err := configgen.MergeRuleSetContext(ruleGroup)
	if err != nil {
		return payload
	}

	payload.Groups = ctx.GroupNames()
	payload.Providers = ctx.ProviderNames()
	for _, rule := range config.SortCustomRulesForDisplay(cfg.MergeCustomRulesFor(ruleGroup)) {
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

// mergeConfigSnapshot 取当前配置的值拷贝（Subscriptions/MergeCustomRules 与全局共享底层数组，
// 故调用方只应读取标量与本请求内刚复制出的切片，不得在锁外就地改动）。
func mergeConfigSnapshot() config.SubscribeConfig {
	config.Mu.RLock()
	defer config.Mu.RUnlock()
	return config.Current
}

// ensureMergeRulesMap 保证 MergeCustomRules 非 nil；调用方必须持有写锁。
func ensureMergeRulesMap() {
	if config.Current.MergeCustomRules == nil {
		config.Current.MergeCustomRules = map[string][]config.CustomRule{}
	}
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

// mergeRuleGroupParam 从 URL 路径中解析规则集档位。
func mergeRuleGroupParam(r *http.Request) (string, bool) {
	raw := strings.TrimPrefix(r.URL.Path, config.BaseURL+mergeCustomRulesRoutePath)
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", false
	}
	group, err := url.QueryUnescape(raw)
	if err != nil || group == "" {
		return "", false
	}
	return group, true
}
