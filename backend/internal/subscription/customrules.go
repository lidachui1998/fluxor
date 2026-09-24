package subscription

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"fluxor/internal/configgen"
	"fluxor/internal/core"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// customRulesRoutePath 是自定义规则接口的路径前缀（挂在 BASE_URL 之下）。
const customRulesRoutePath = "/subscribe/custom-rules/"

// customRuleView 是下发前端的单条规则视图。
//
// 除规则本体外附带组装好的规则行与合法性判定：合法性必须在后端判定
// （目标是否存在、RULE-SET 是否存在于该订阅的 rule-providers），前端不复刻这套逻辑。
type customRuleView struct {
	config.CustomRule
	Line   string `json:"line"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// customRulesPayload 是自定义规则接口的统一响应体。
//
// 查/增/改/排序/删共用同一结构：前端每次操作后直接用响应刷新列表，无需再发一次查询。
type customRulesPayload struct {
	// FileReady 该订阅的原始节点文件是否已下载（未下载则无法校验规则）。
	FileReady bool `json:"file_ready"`
	// Rules 该订阅已配置的自定义规则（含合法性判定），顺序即生效顺序。
	Rules []customRuleView `json:"rules"`
	// Tunnels 该作用域的流量隧道（含启停开关与合法性判定），顺序即写入配置的顺序。
	//
	// 与规则放在同一个响应体里：规则接口与隧道接口服务的是同一个作用域（同一个弹窗），
	// 前端拿到任一响应都会整份刷新状态，两者必须同构。
	Tunnels []tunnelView `json:"tunnels"`
	// Groups 可选目标：代理组（订阅自带或档位模板），**不含**代理节点。
	Groups []string `json:"groups"`
	// Nodes 可选目标：自定义模式下的手工节点名（其余模式为空）。
	//
	// 与代理组一样是合法目标——自定义模式的节点写死在 config.yaml 的 proxies 里，
	// 规则指向节点名内核能解析；融合/切换模式的节点来自 provider 或订阅文件，
	// 静态校验看不到，因此只在自定义模式下给出（见 CustomModeRuleContext）。
	Nodes []string `json:"nodes"`
	// Builtins 可选的内置目标：DIRECT / REJECT / PASS。
	Builtins []string `json:"builtins"`
	// Providers 可选规则集：该订阅 rule-providers 的键，供 RULE-SET 使用。
	Providers []string `json:"providers"`
	// RuleTypes 支持的规则类型白名单及载荷示例。
	RuleTypes []configcheck.RuleSpec `json:"rule_types"`
	// Status / Message 仅在有话要说时返回（ok / warning），前端据此提示。
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

// moveRuleRequest 是 PATCH 的请求体：在列表内上移/下移一条规则。
type moveRuleRequest struct {
	ID        string `json:"id"`
	Direction string `json:"direction"`
}

// HandleCustomRulesAPI 处理切换模式下的订阅自定义规则：
//
//	GET    /subscribe/custom-rules/{name}        查询规则与可选目标
//	POST   /subscribe/custom-rules/{name}        新增一条规则（写一条保存一条）
//	PUT    /subscribe/custom-rules/{name}        修改一条已存在的规则（body 带 id）
//	PATCH  /subscribe/custom-rules/{name}        调整顺序（body: {id, direction: up|down}）
//	DELETE /subscribe/custom-rules/{name}?id=xx  删除一条规则
//
// 所有写操作都立即持久化到 rules.json（config.UpdateSubscriptionRules 锁内读—改—写）；
// 若该订阅正是切换模式下的激活订阅，
// 则同时重写 config.yaml 并热重载内核。弹窗本身不需要「保存」动作。
func HandleCustomRulesAPI(w http.ResponseWriter, r *http.Request) {
	name, ok := customRulesSubscriptionName(r)
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少订阅名称")
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg, sub, found := findSubscription(name)
		if !found {
			httpx.WriteJSONError(w, http.StatusNotFound, "订阅不存在: "+name)
			return
		}
		// 查询也要过模式校验：该入口只服务切换模式（切换模式的规则挂在订阅上，
		// 其可选目标来自订阅文件本身；融合/自定义模式走模板级入口）。漏掉这一步会
		// 出现「GET 能打开、第一次写就被 400 拦下」的不一致，也违背「跨模式访问统一
		// 回 400」的约定。
		if cfg.Mode != "switch" {
			httpx.WriteJSONError(w, http.StatusBadRequest,
				modeMismatchHint("自定义规则仅在切换模式下可用", cfg.Mode))
			return
		}
		httpx.RespondJSON(w, http.StatusOK, buildCustomRulesPayload(name, cfg, sub))

	case http.MethodPost:
		handleAddCustomRule(w, r, name)

	case http.MethodPut:
		handleUpdateCustomRule(w, r, name)

	case http.MethodPatch:
		handleMoveCustomRule(w, r, name)

	case http.MethodDelete:
		handleDeleteCustomRule(w, r, name)

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleAddCustomRule 新增一条自定义规则（追加到列表末尾，即同分组内优先级最低）。
func handleAddCustomRule(w http.ResponseWriter, r *http.Request, name string) {
	cfg, sub, found := findSubscription(name)
	if !found {
		httpx.WriteJSONError(w, http.StatusNotFound, "订阅不存在: "+name)
		return
	}
	// 自定义规则只在切换模式下有意义：融合模式的规则由模板生成，且订阅文件
	// 不会被下载（ensureSubscriptionFiles 在 merge 下直接返回），无从校验目标。
	if cfg.Mode != "switch" {
		httpx.WriteJSONError(w, http.StatusBadRequest,
			modeMismatchHint("自定义规则仅在切换模式下可用", cfg.Mode))
		return
	}

	var input config.CustomRule
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
		return
	}
	input.ID = newCustomRuleID()

	ctx, err := loadRuleContextForMutation(name)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := prepareCustomRule(input, ctx, sub.CustomRules, "")
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 「追加到列表」这一步在写锁内完成：闭包拿到的 cur 是**此刻**存储里的列表，
	// 因此同一订阅的并发改动不会互相覆盖（此前用「请求快照 + 整份落盘」存在丢失更新的窗口）。
	// 持久化顺序 = 展示顺序 = 生效顺序：before 组在前，after 组在后。
	err = config.UpdateSubscriptionRules(name, func(cur []config.CustomRule) ([]config.CustomRule, error) {
		return config.SortCustomRulesForDisplay(appendCopied(cur, rule)), nil
	})
	if err != nil {
		logx.Error(logx.ModuleRule, "saving custom rules failed: subscription=%q: %v", name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}

	respondAfterRuleMutation(w, name, "ok", "")
}

// handleUpdateCustomRule 修改一条已存在的规则。
//
// 就地替换、保持其在列表中的位置：用户点「保存」的意图是改内容，
// 而不是把它挪到列表末尾（排序另有上下移动按钮）。
func handleUpdateCustomRule(w http.ResponseWriter, r *http.Request, name string) {
	cfg, sub, found := findSubscription(name)
	if !found {
		httpx.WriteJSONError(w, http.StatusNotFound, "订阅不存在: "+name)
		return
	}
	if cfg.Mode != "switch" {
		httpx.WriteJSONError(w, http.StatusBadRequest,
			modeMismatchHint("自定义规则仅在切换模式下可用", cfg.Mode))
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

	existing := false
	for _, rule := range sub.CustomRules {
		if rule.ID == input.ID {
			existing = true
			break
		}
	}
	if !existing {
		httpx.WriteJSONError(w, http.StatusNotFound, "规则不存在: "+input.ID)
		return
	}

	ctx, err := loadRuleContextForMutation(name)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 判重时排除自身：未改动的规则行必然与自身相同，不能因此报「已存在」
	rule, err := prepareCustomRule(input, ctx, sub.CustomRules, input.ID)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 就地替换在写锁内完成：cur 是此刻的列表，并发改动不会互相覆盖
	var notFound bool
	err = config.UpdateSubscriptionRules(name, func(cur []config.CustomRule) ([]config.CustomRule, error) {
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
		// 位置可能被改动过（before <-> after），重排以保持「持久化顺序 = 展示顺序」
		return config.SortCustomRulesForDisplay(out), nil
	})
	if err != nil {
		logx.Error(logx.ModuleRule, "saving custom rules failed: subscription=%q: %v", name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}
	if notFound {
		httpx.WriteJSONError(w, http.StatusNotFound, "规则不存在: "+input.ID)
		return
	}

	respondAfterRuleMutation(w, name, "ok", "")
}

// handleMoveCustomRule 在列表内上移/下移一条规则。
//
// 只在同一插入位置分组内移动：before 与 after 的规则在 config.yaml 中落点不同，
// 跨分组交换会让界面顺序与生效顺序不一致。
func handleMoveCustomRule(w http.ResponseWriter, r *http.Request, name string) {
	cfg, _, found := findSubscription(name)
	if !found {
		httpx.WriteJSONError(w, http.StatusNotFound, "订阅不存在: "+name)
		return
	}
	if cfg.Mode != "switch" {
		httpx.WriteJSONError(w, http.StatusBadRequest,
			modeMismatchHint("自定义规则仅在切换模式下可用", cfg.Mode))
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

	// 组内相邻交换在写锁内完成：moved 的语义由 config.MoveCustomRule 给出（边界返回 false）
	var moved bool
	err := config.UpdateSubscriptionRules(name, func(cur []config.CustomRule) ([]config.CustomRule, error) {
		out, ok := config.MoveCustomRule(cur, input.ID, direction)
		moved = ok
		return out, nil
	})
	if err != nil {
		logx.Error(logx.ModuleRule, "saving custom rules failed: subscription=%q: %v", name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}
	if !moved {
		// 边界或 id 不存在：都不改变任何状态，如实告知而不是假装成功
		httpx.WriteJSONError(w, http.StatusBadRequest, "规则已在该分组的最前/最后，无法继续移动")
		return
	}

	respondAfterRuleMutation(w, name, "ok", "")
}

// handleDeleteCustomRule 删除一条自定义规则。
func handleDeleteCustomRule(w http.ResponseWriter, r *http.Request, name string) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少规则 id")
		return
	}
	_, _, found := findSubscription(name)
	if !found {
		httpx.WriteJSONError(w, http.StatusNotFound, "订阅不存在: "+name)
		return
	}

	// 过滤在写锁内完成：found 由闭包给出，删除不存在的 id 仍按 404 如实告知
	var removed bool
	err := config.UpdateSubscriptionRules(name, func(cur []config.CustomRule) ([]config.CustomRule, error) {
		kept := make([]config.CustomRule, 0, len(cur))
		for _, rule := range cur {
			if rule.ID == id {
				removed = true
				continue
			}
			kept = append(kept, rule)
		}
		return kept, nil
	})
	if err != nil {
		logx.Error(logx.ModuleRule, "saving custom rules failed: subscription=%q: %v", name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存规则失败: "+err.Error())
		return
	}
	if !removed {
		httpx.WriteJSONError(w, http.StatusNotFound, "规则不存在: "+id)
		return
	}

	respondAfterRuleMutation(w, name, "ok", "")
}

// loadRuleContextForMutation 载入订阅文件构建规则上下文；文件未就绪时给出可操作提示。
func loadRuleContextForMutation(name string) (*configgen.RuleContext, error) {
	ctx, err := configgen.LoadRuleContext(subscriptionFilePath(name))
	if err != nil {
		return nil, fmt.Errorf("订阅文件尚未就绪，请先保存并应用: %w", err)
	}
	return ctx, nil
}

// prepareCustomRule 归一化并校验一条待写入的规则。
//
// excludeID 用于「修改」场景排除自身，避免未改动的规则被自己判为重复。
func prepareCustomRule(input config.CustomRule, ctx *configgen.RuleContext, rules []config.CustomRule, excludeID string) (config.CustomRule, error) {
	rule := config.CustomRule{
		ID:        input.ID,
		Type:      strings.ToUpper(strings.TrimSpace(input.Type)),
		Payload:   strings.TrimSpace(input.Payload),
		Target:    strings.TrimSpace(input.Target),
		Position:  config.NormalizeRulePosition(input.Position),
		NoResolve: input.NoResolve,
	}
	line, err := configgen.ValidateCustomRule(rule, ctx.Env)
	if err != nil {
		return rule, err
	}
	if ctx.HasRuleLine(line) {
		return rule, fmt.Errorf("该规则已存在于订阅规则中，无需重复添加")
	}
	for _, other := range rules {
		if other.ID == excludeID {
			continue
		}
		if displayRuleLine(other) == line {
			return rule, fmt.Errorf("与已有自定义规则重复（%s）", line)
		}
	}
	return rule, nil
}

// respondAfterRuleMutation 落盘之后统一收尾：按需同步运行配置并返回最新列表。
func respondAfterRuleMutation(w http.ResponseWriter, name, status, message string) {
	syncStatus, syncMessage := applyCustomRulesToActiveSubscription(name)
	if syncStatus == "warning" {
		status = syncStatus
		message = joinMessage(message, syncMessage)
	}

	cfg, sub, _ := findSubscription(name)
	payload := buildCustomRulesPayload(name, cfg, sub)
	payload.Status = status
	payload.Message = message
	httpx.RespondJSON(w, http.StatusOK, payload)
}

// applyCustomRulesToActiveSubscription 在改动规则后同步运行配置。
//
// 仅当该订阅是切换模式下的激活订阅时才需要动作：config.yaml 是它的副本，
// 其余订阅只是本地缓存文件，改动规则不影响到运行中的内核。
//
// 返回 (status, message)：status 为 "ok" 或 "warning"，后者表示规则已持久化，
// 但运行配置未能同步（文件缺失/内核重载失败），需要如实告知前端。
func applyCustomRulesToActiveSubscription(name string) (string, string) {
	config.Mu.RLock()
	cfg := config.Current
	active := cfg.Mode == "switch" && cfg.ActiveSubscription == name
	// 锁内复制规则与隧道：锁外不得引用 config.Current.Subscriptions 的底层数组
	var rules []config.CustomRule
	var tunnels []config.Tunnel
	if active {
		rules = copyCustomRules(cfg, name)
		tunnels = copyCustomTunnels(cfg, name)
	}
	config.Mu.RUnlock()

	if !active {
		return "ok", ""
	}

	result, err := writeRuntimeConfig(name, rules, tunnels)
	if err != nil {
		logx.Error(logx.ModuleRule, "sync runtime config failed: subscription=%q: %v", name, err)
		return "warning", "规则已保存，但同步运行配置失败: " + err.Error()
	}

	warning := ""
	if len(result.Skipped) > 0 {
		warning = "有 " + strconv.Itoa(len(result.Skipped)) + " 条规则因目标不存在未写入配置"
		logx.Warn(logx.ModuleRule, "custom rules not written to runtime config: subscription=%q count=%d", name, len(result.Skipped))
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

// buildCustomRulesPayload 组装查询与所有写操作的统一响应。
//
// 规则按「before 组在前、after 组在后」下发：这就是它们在 config.yaml 中的
// 相对生效顺序，前端据此渲染，上/下移动的边界也按同分组判定。
func buildCustomRulesPayload(name string, cfg config.SubscribeConfig, sub config.Subscription) customRulesPayload {
	payload := customRulesPayload{
		Rules:     []customRuleView{},
		Tunnels:   []tunnelView{},
		Groups:    []string{},
		Builtins:  configcheck.BuiltinRuleTargets(),
		Providers: []string{},
		RuleTypes: configcheck.RuleSpecs(),
	}

	ctx, err := configgen.LoadRuleContext(subscriptionFilePath(name))
	if err != nil {
		// 订阅文件未就绪：规则与隧道都无从校验（可选目标就来自那份文件），
		// 但已保存的内容仍要展示，供用户删除或等文件就绪后再改
		reason := "订阅文件尚未就绪，请先保存并应用"
		for _, rule := range config.SortCustomRulesForDisplay(sub.CustomRules) {
			payload.Rules = append(payload.Rules, customRuleView{
				CustomRule: rule,
				Line:       displayRuleLine(rule),
				Valid:      false,
				Reason:     reason,
			})
		}
		payload.Tunnels = buildTunnelViews(sub.Tunnels, nil, reason)
		return payload
	}

	payload.FileReady = true
	payload.Groups = ctx.GroupNames()
	payload.Providers = ctx.ProviderNames()
	payload.Tunnels = buildTunnelViews(sub.Tunnels, ctx, "")
	for _, rule := range config.SortCustomRulesForDisplay(sub.CustomRules) {
		view := customRuleView{CustomRule: rule, Line: displayRuleLine(rule)}
		if line, err := configgen.ValidateCustomRule(rule, ctx.Env); err == nil {
			view.Line = line
			view.Valid = true
		} else {
			view.Reason = err.Error()
		}
		payload.Rules = append(payload.Rules, view)
	}
	return payload
}

// displayRuleLine 生成规则的展示文本。
//
// 非法规则也要展示：用户需要看到自己写了什么才能修正或删除，故类型未知时
// 退化为按原字段拼接。
func displayRuleLine(rule config.CustomRule) string {
	if spec, ok := configcheck.LookupRuleSpec(rule.Type); ok {
		return configcheck.BuildRuleLine(spec, rule.Payload, rule.Target, rule.NoResolve)
	}
	parts := make([]string, 0, 3)
	for _, part := range []string{rule.Type, rule.Payload, rule.Target} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ",")
}

// findSubscription 取订阅配置快照与目标订阅。
//
// cfg 是值拷贝：其 Subscriptions 与全局共享底层数组，因此调用方只能在锁内读取
// 标量字段；返回的 sub.CustomRules 已复制为独立切片，可安全在锁外使用。
func findSubscription(name string) (config.SubscribeConfig, config.Subscription, bool) {
	config.Mu.RLock()
	defer config.Mu.RUnlock()

	cfg := config.Current
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Name == name {
			sub := cfg.Subscriptions[i]
			sub.CustomRules = copyCustomRules(cfg, name)
			sub.Tunnels = copyCustomTunnels(cfg, name)
			return cfg, sub, true
		}
	}
	return cfg, config.Subscription{}, false
}

// customRulesSubscriptionName 从 URL 路径中解析订阅名。
func customRulesSubscriptionName(r *http.Request) (string, bool) {
	raw := strings.TrimPrefix(r.URL.Path, config.BaseURL+customRulesRoutePath)
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", false
	}
	name, err := url.QueryUnescape(raw)
	if err != nil || name == "" {
		return "", false
	}
	return name, true
}

// newCustomRuleID 生成规则标识（前端编辑/排序/删除单条规则使用）。
func newCustomRuleID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

// joinMessage 拼接两条可选提示，忽略空段。
func joinMessage(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "；")
}
