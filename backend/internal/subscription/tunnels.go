package subscription

import (
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
)

// 流量隧道接口的路径前缀。
//
// 三个作用域各一条，与自定义规则的三个入口（custom-rules / merge-custom-rules /
// custom-mode-rules）一一对应：作用域语义完全相同，只是管的是隧道列表而不是规则列表。
// 之所以单独开入口而不是在规则接口上加 kind 参数：两条链路的写操作虽然流程一致，
// 但校验与产物完全不同，混在一起的接口没法用一句话说清它在改什么。
const (
	// subscriptionTunnelsRoutePath 切换模式：作用域是该订阅（隧道的 proxy 取订阅自带代理组）。
	subscriptionTunnelsRoutePath = "/subscribe/custom-tunnels/"
	// mergeTunnelsRoutePath 融合模式：作用域是规则集档位 base / full。
	mergeTunnelsRoutePath = "/subscribe/merge-custom-tunnels/"
	// customModeTunnelsRoutePath 自定义模式：作用域恒为 custom（不按档位分表）。
	customModeTunnelsRoutePath = "/subscribe/custom-mode-tunnels/"
)

// tunnelView 是下发前端的单条隧道视图。
//
// 不直接嵌 config.Tunnel：存储里的 Enabled 是 *bool（nil 表示历史数据未设置，视为启用），
// 下发时统一折算成显式布尔，前端不必处理「缺键」这第三种状态。
type tunnelView struct {
	// 结构字段与 config.Tunnel 一一对应，前端按此渲染表单与列表。
	ID      string   `json:"id"`
	Network []string `json:"network"`
	Address string   `json:"address"`
	Target  string   `json:"target"`
	Proxy   string   `json:"proxy,omitempty"`
	// Enabled 启停开关：关闭的隧道不写进 config.yaml。
	Enabled bool `json:"enabled"`
	// Line 单行展示形式（network/address/target/proxy），与 wiki 的单行写法一致。
	Line string `json:"line"`
	// Valid / Reason 合法性由后端判定（地址格式、proxy 是否存在、是否与另一条冲突），
	// 前端不复刻这套逻辑：与规则列表的处理方式保持一致。
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

// tunnelScope 描述一个流量隧道作用域。
//
// 与规则作用域（ruleScope）刻意分开：规则作用域是「增/改/排序/删一条规则」的抽象，
// 隧道没有插入位置分组、多出启停开关，两者的校验与产物也不同。共用的只有作用域语义
// （可编辑性、生效判定、重新生成）——融合/自定义两个作用域直接复用 mergeRuleScope /
// customModeRuleScope 的这几个闭包，因此不存在「两份作用域判定迟早不一致」的问题。
type tunnelScope struct {
	// name 作用域标识（订阅名 / base / full / custom）：接口路径与日志都用它。
	name string
	// tunnels 取该作用域当前的隧道（快照与锁内读都走它）。
	tunnels func(cfg config.SubscribeConfig) []config.Tunnel
	// update 在本作用域的 tunnels.json 写锁内完成「读—改—写」：闭包拿到的 current 是
	// **此刻**存储里的列表，返回的列表被原样落盘，因此同一作用域的并发改动不会互相覆盖。
	//
	// 三个作用域各绑定自己的写 API（模板作用域 UpdateTemplateTunnels、订阅作用域
	// UpdateSubscriptionTunnels），四个写入口因此只需写一遍。
	update func(mutate func(current []config.Tunnel) ([]config.Tunnel, error)) error
	// editable 判定「当前模式下该作用域是否可编辑」。
	editable func(cfg config.SubscribeConfig) bool
	// disabledHint 不可编辑时的提示（各入口指向对方入口，用户才知道该去哪儿改）。
	disabledHint string
	// active 判定该作用域当前是否生效（决定改动后要不要重新生成配置）。
	active func(cfg config.SubscribeConfig) bool
	// regenerate 重新生成运行配置（切换模式 writeRuntimeConfig、融合 GenerateConfig、
	// 自定义 GenerateCustomConfig）。
	regenerate func(cfg config.SubscribeConfig) error
	// context 该作用域的规则上下文（合法 proxy 名与界面上的代理组/节点列表同源）。
	context func(cfg config.SubscribeConfig) (*configgen.RuleContext, error)
	// payload 组装该接口的统一响应：规则 + 隧道 + 可选目标，一次刷新整个弹窗。
	payload func(cfg config.SubscribeConfig) customRulesPayload
}

// mergeTunnelScope 返回融合模式某规则集档位的隧道作用域；档位非法时返回 false。
//
// 可编辑性/生效判定/重新生成/上下文全部取自该档位的规则作用域：融合模式的隧道与规则
// 同存同生效，两处各写一份判定迟早会漂移（例如某次改动只更新了一边）。
func mergeTunnelScope(ruleGroup string) (tunnelScope, bool) {
	rules, ok := mergeRuleScope(ruleGroup)
	if !ok {
		return tunnelScope{}, false
	}
	return tunnelScope{
		name:    ruleGroup,
		tunnels: rules.tunnels,
		update: func(mutate func([]config.Tunnel) ([]config.Tunnel, error)) error {
			return config.UpdateTemplateTunnels(ruleGroup, mutate)
		},
		editable:     rules.editable,
		disabledHint: "流量隧道仅在融合模式下可用（切换模式请用订阅卡片上的入口，自定义模式请切到自定义模式）",
		active:       rules.active,
		regenerate:   rules.regenerate,
		context:      rules.context,
		payload: func(cfg config.SubscribeConfig) customRulesPayload {
			return buildRulesPayload(cfg, rules)
		},
	}, true
}

// customModeTunnelScope 返回自定义模式的隧道作用域。
//
// 与融合模式档位的差别与规则一致：隧道单独存在 custom_mode_tunnels（不按档位分表），
// 可选的 proxy 里额外包含手工节点名。
func customModeTunnelScope() tunnelScope {
	rules := customModeRuleScope()
	return tunnelScope{
		name:    config.RuleScopeCustom,
		tunnels: rules.tunnels,
		update: func(mutate func([]config.Tunnel) ([]config.Tunnel, error)) error {
			return config.UpdateTemplateTunnels(config.RuleScopeCustom, mutate)
		},
		editable:     rules.editable,
		disabledHint: "流量隧道仅在自定义模式下可用（融合模式请用标题行的入口，切换模式请用订阅卡片上的入口）",
		active:       rules.active,
		regenerate:   rules.regenerate,
		context:      rules.context,
		payload: func(cfg config.SubscribeConfig) customRulesPayload {
			return buildRulesPayload(cfg, rules)
		},
	}
}

// subscriptionTunnelScope 返回切换模式下某个订阅的隧道作用域。
//
// 与模板级作用域不同，这里的一切都以「该订阅」为准：隧道存放在
// subscriptions[].tunnels，重新生成即把该订阅的节点文件复制为 config.yaml 并叠加
// 规则与隧道，可校验的 proxy 来自订阅文件本身的代理组/节点。
func subscriptionTunnelScope(name string) tunnelScope {
	return tunnelScope{
		name: name,
		tunnels: func(cfg config.SubscribeConfig) []config.Tunnel {
			return cfg.SubscriptionTunnelsFor(name)
		},
		update: func(mutate func([]config.Tunnel) ([]config.Tunnel, error)) error {
			return config.UpdateSubscriptionTunnels(name, mutate)
		},
		editable: func(cfg config.SubscribeConfig) bool {
			return cfg.Mode == config.ModeSwitch
		},
		disabledHint: "流量隧道仅在切换模式下可用（融合模式请用标题行的入口，自定义模式请切到自定义模式）",
		active: func(cfg config.SubscribeConfig) bool {
			return cfg.Mode == config.ModeSwitch && cfg.ActiveSubscription == name
		},
		regenerate: func(cfg config.SubscribeConfig) error {
			_, err := writeRuntimeConfig(name, copyCustomRules(cfg, name), copyCustomTunnels(cfg, name))
			return err
		},
		context: func(config.SubscribeConfig) (*configgen.RuleContext, error) {
			return configgen.LoadRuleContext(subscriptionFilePath(name))
		},
		payload: func(cfg config.SubscribeConfig) customRulesPayload {
			// 复用切换模式规则接口的响应组装：同一个作用域的两种接口必须给出同构的响应，
			// 否则前端在两条链路的返回值之间来回覆盖时会互相抹掉对方的列表
			_, sub, found := findSubscription(name)
			if !found {
				return emptyScopePayload()
			}
			return buildCustomRulesPayload(name, cfg, sub)
		},
	}
}

// HandleSubscriptionTunnelsAPI 处理切换模式下某订阅的流量隧道：
//
//	GET    /subscribe/custom-tunnels/{name}        查询隧道与可选目标
//	POST   /subscribe/custom-tunnels/{name}        新增一条隧道（追加到列表末尾）
//	PUT    /subscribe/custom-tunnels/{name}        修改一条已存在的隧道（含启停开关）
//	PATCH  /subscribe/custom-tunnels/{name}        调整顺序（body: {id, direction: up|down}）
//	DELETE /subscribe/custom-tunnels/{name}?id=xx  删除一条隧道
//
// 所有写操作都立即持久化到 tunnels.json（锁内读—改—写）；若该订阅正是切换模式下的激活订阅，
// 则同时重写 config.yaml 并热重载内核。
func HandleSubscriptionTunnelsAPI(w http.ResponseWriter, r *http.Request) {
	name, ok := tunnelScopeParam(r, subscriptionTunnelsRoutePath)
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少订阅名称")
		return
	}
	if _, _, found := findSubscription(name); !found {
		httpx.WriteJSONError(w, http.StatusNotFound, "订阅不存在: "+name)
		return
	}
	serveTunnelRequest(w, r, subscriptionTunnelScope(name))
}

// HandleMergeTunnelsAPI 处理融合模式按规则集档位存放的流量隧道。
//
// {ruleGroup} 取 base / full：两档的代理组不同，隧道分别存放在 cfg.MergeTunnels[档位]，
// 生成配置时只取当前生效档位那一份（另一档保持惰性）。
func HandleMergeTunnelsAPI(w http.ResponseWriter, r *http.Request) {
	ruleGroup, ok := tunnelScopeParam(r, mergeTunnelsRoutePath)
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少规则集参数")
		return
	}
	scope, ok := mergeTunnelScope(ruleGroup)
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "未知规则集: "+ruleGroup)
		return
	}
	serveTunnelRequest(w, r, scope)
}

// HandleCustomModeTunnelsAPI 处理自定义模式的流量隧道（与融合模式完全独立的一份）。
//
// {scope} 恒为 custom：该模式只有这一份隧道（cfg.CustomModeTunnels），保留该段是为了
// 让前端那套「endpoint + 作用域」的调用方式对三个入口完全一致。
func HandleCustomModeTunnelsAPI(w http.ResponseWriter, r *http.Request) {
	scope, ok := tunnelScopeParam(r, customModeTunnelsRoutePath)
	if !ok || scope != config.RuleScopeCustom {
		httpx.WriteJSONError(w, http.StatusBadRequest, "未知作用域（应为 "+config.RuleScopeCustom+"）")
		return
	}
	serveTunnelRequest(w, r, customModeTunnelScope())
}

// serveTunnelRequest 隧道接口的统一入口：校验可编辑性后按方法分发。
func serveTunnelRequest(w http.ResponseWriter, r *http.Request, scope tunnelScope) {
	if !scope.editable(ruleConfigSnapshot()) {
		httpx.WriteJSONError(w, http.StatusBadRequest, scope.disabledHint)
		return
	}

	switch r.Method {
	case http.MethodGet:
		httpx.RespondJSON(w, http.StatusOK, scope.payload(ruleConfigSnapshot()))
	case http.MethodPost:
		handleAddTunnel(w, r, scope)
	case http.MethodPut:
		handleUpdateTunnel(w, r, scope)
	case http.MethodPatch:
		handleMoveTunnel(w, r, scope)
	case http.MethodDelete:
		handleDeleteTunnel(w, r, scope)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleAddTunnel 新增一条隧道，追加到该作用域列表末尾。
func handleAddTunnel(w http.ResponseWriter, r *http.Request, scope tunnelScope) {
	cfg := ruleConfigSnapshot()

	var input config.Tunnel
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
		return
	}
	ctx, err := scope.context(cfg)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 新增一律补新 id：客户端带来的 id 不被信任（否则可以借它覆盖已有条目）
	input.ID = ""
	tunnel, err := prepareTunnel(input, ctx, scope.tunnels(cfg), "")
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 「追加到列表末尾」这一步在写锁内完成：cur 是此刻存储里的列表，
	// 因此同一作用域的并发新增不会互相覆盖（此前用「请求快照 + 整份落盘」存在丢失更新的窗口）
	err = scope.update(func(cur []config.Tunnel) ([]config.Tunnel, error) {
		return appendTunnel(cur, tunnel), nil
	})
	if err != nil {
		logx.Error(logx.ModuleTunnel, "saving tunnels failed: scope=%s: %v", scope.name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存隧道失败: "+err.Error())
		return
	}
	respondAfterTunnelMutation(w, scope)
}

// handleUpdateTunnel 修改一条已存在的隧道（就地替换，保持其在列表中的位置）。
//
// 启停开关也走这个接口：开关是隧道的字段之一，前端把该条隧道原样带回、只翻转 enabled。
// 关闭的隧道不做校验（见 prepareTunnel），因此「proxy 已被改名」的隧道仍然关得掉。
func handleUpdateTunnel(w http.ResponseWriter, r *http.Request, scope tunnelScope) {
	cfg := ruleConfigSnapshot()

	var input config.Tunnel
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
		return
	}
	input.ID = strings.TrimSpace(input.ID)
	if input.ID == "" {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少隧道 id")
		return
	}
	if !containsTunnelID(scope.tunnels(cfg), input.ID) {
		httpx.WriteJSONError(w, http.StatusNotFound, "隧道不存在: "+input.ID)
		return
	}

	ctx, err := scope.context(cfg)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 判重时排除自身：未改动的隧道必然与自己同地址同网络
	tunnel, err := prepareTunnel(input, ctx, scope.tunnels(cfg), input.ID)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 就地替换在写锁内完成：cur 是此刻的列表，并发改动不会互相覆盖
	var notFound bool
	err = scope.update(func(cur []config.Tunnel) ([]config.Tunnel, error) {
		out := config.CopyTunnels(cur)
		idx := -1
		for i := range out {
			if out[i].ID == tunnel.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			// 锁外的存在性校验到这里之间被并发删除：不落盘，按 404 如实告知
			notFound = true
			return out, nil
		}
		out[idx] = tunnel
		return out, nil
	})
	if err != nil {
		logx.Error(logx.ModuleTunnel, "saving tunnels failed: scope=%s: %v", scope.name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存隧道失败: "+err.Error())
		return
	}
	if notFound {
		httpx.WriteJSONError(w, http.StatusNotFound, "隧道不存在: "+input.ID)
		return
	}
	respondAfterTunnelMutation(w, scope)
}

// handleMoveTunnel 在列表内上移/下移一条隧道。
//
// 隧道没有插入位置的分组概念（同属 config.yaml 的一个 tunnels 块），整份列表即一组，
// 顺序只影响产物中每条隧道的先后。
func handleMoveTunnel(w http.ResponseWriter, r *http.Request, scope tunnelScope) {
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

	// 相邻交换在写锁内完成：moved 的语义由 config.MoveTunnel 给出（边界或 id 不存在返回 false）
	var moved bool
	err := scope.update(func(cur []config.Tunnel) ([]config.Tunnel, error) {
		out, ok := config.MoveTunnel(cur, strings.TrimSpace(input.ID), direction)
		moved = ok
		return out, nil
	})
	if err != nil {
		logx.Error(logx.ModuleTunnel, "saving tunnels failed: scope=%s: %v", scope.name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存隧道失败: "+err.Error())
		return
	}
	if !moved {
		// 边界或 id 不存在：不改变任何状态，如实告知而不是假装成功
		httpx.WriteJSONError(w, http.StatusBadRequest, "隧道已在列表的最前/最后，无法继续移动")
		return
	}
	respondAfterTunnelMutation(w, scope)
}

// handleDeleteTunnel 删除一条隧道。
func handleDeleteTunnel(w http.ResponseWriter, r *http.Request, scope tunnelScope) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少隧道 id")
		return
	}

	// 过滤在写锁内完成：found 由闭包给出，删除不存在的 id 仍按 404 如实告知
	var found bool
	err := scope.update(func(cur []config.Tunnel) ([]config.Tunnel, error) {
		kept := make([]config.Tunnel, 0, len(cur))
		for _, tunnel := range cur {
			if tunnel.ID == id {
				found = true
				continue
			}
			kept = append(kept, tunnel)
		}
		return kept, nil
	})
	if err != nil {
		logx.Error(logx.ModuleTunnel, "saving tunnels failed: scope=%s: %v", scope.name, err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存隧道失败: "+err.Error())
		return
	}
	if !found {
		httpx.WriteJSONError(w, http.StatusNotFound, "隧道不存在: "+id)
		return
	}
	respondAfterTunnelMutation(w, scope)
}

// prepareTunnel 归一化并校验一条待写入的隧道。
//
// excludeID 用于「修改」场景排除自身，避免未改动的隧道被自己判为重复。
//
// 关闭的隧道只做归一化、不做校验：它不会写进 config.yaml，因此一条 proxy 已被改名
// 的隧道必须仍然关得掉——否则用户唯一的选择是删掉它重建。
func prepareTunnel(input config.Tunnel, ctx *configgen.RuleContext, tunnels []config.Tunnel, excludeID string) (config.Tunnel, error) {
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	tunnel := config.Tunnel{
		ID:      strings.TrimSpace(input.ID),
		Address: strings.TrimSpace(input.Address),
		Target:  strings.TrimSpace(input.Target),
		Proxy:   strings.TrimSpace(input.Proxy),
		// 显式落一个布尔值：磁盘上因此不再出现「缺 enabled 键」的历史形态
		Enabled: &enabled,
	}
	if tunnel.ID == "" {
		tunnel.ID = newCustomTunnelID()
	}
	networks, err := config.NormalizeTunnelNetworks(input.Network)
	if err != nil {
		return tunnel, err
	}
	tunnel.Network = networks

	if !tunnel.IsEnabled() {
		return tunnel, nil
	}
	// 目标在落库前定形：只写域名时补默认端口（:80），这样界面上的单行配置与实际转发目标
	// 一致；裸 IP 会在这里被拒（不猜 IP 的端口），详见 config.NormalizeTunnelTarget
	target, err := config.NormalizeTunnelTarget(tunnel.Target)
	if err != nil {
		return tunnel, err
	}
	tunnel.Target = target
	if err := configgen.ValidateTunnel(tunnel, ctx.Env); err != nil {
		return tunnel, err
	}
	for i := range tunnels {
		other := tunnels[i]
		if other.ID == excludeID || !other.IsEnabled() {
			continue
		}
		if config.TunnelBindingsOverlap(other, tunnel) {
			return tunnel, fmt.Errorf("与已有隧道（%s）的监听地址与网络类型重复，内核会因端口冲突无法启动", other.Address)
		}
	}
	return tunnel, nil
}

// respondAfterTunnelMutation 落盘之后统一收尾：按需重生成运行配置并返回最新列表。
func respondAfterTunnelMutation(w http.ResponseWriter, scope tunnelScope) {
	status, message := applyTunnelsToActiveConfig(scope)

	payload := scope.payload(ruleConfigSnapshot())
	payload.Status = status
	payload.Message = message
	httpx.RespondJSON(w, http.StatusOK, payload)
}

// applyTunnelsToActiveConfig 在改动隧道后同步运行配置。
//
// 仅当该作用域当前生效时才需要动作（切换模式=激活订阅、融合模式=当前档位、
// 自定义模式=恒生效）；其余情况隧道已落库，但要等到该作用域生效时才会写进 config.yaml。
func applyTunnelsToActiveConfig(scope tunnelScope) (string, string) {
	config.Mu.RLock()
	cfg := config.Current
	config.Mu.RUnlock()

	if !scope.active(cfg) {
		return "ok", ""
	}

	if err := scope.regenerate(cfg); err != nil {
		logx.Error(logx.ModuleTunnel, "regenerate config failed: mode=%s scope=%s: %v", cfg.Mode, scope.name, err)
		return "warning", "隧道已保存，但重新生成配置文件失败: " + err.Error()
	}

	warning := ""
	if ctx, err := scope.context(cfg); err == nil {
		if skipped := configgen.TunnelSkips(scope.tunnels(cfg), ctx); len(skipped) > 0 {
			warning = "有 " + strconv.Itoa(len(skipped)) + " 条隧道因配置无效未写入配置"
			logx.Warn(logx.ModuleTunnel, "tunnels not written to config: mode=%s scope=%s count=%d", cfg.Mode, scope.name, len(skipped))
		}
	}

	// 内核未运行时只更新 config.yaml（下次启动即生效），不做重载
	if !core.IsCoreRunning() {
		return "ok", warning
	}
	if err := core.ReloadCore(); err != nil {
		logx.Warn(logx.ModuleTunnel, "reload core failed: %v", err)
		return "warning", joinMessage(warning, "内核重载失败: "+err.Error())
	}
	return "ok", warning
}

// emptyScopePayload 返回一份「什么都没有」的响应（作用域对应的订阅已被删除时）。
//
// 各字段都给出空数组而非 nil：前端拿到 null 会多出一种要处理的形态。
func emptyScopePayload() customRulesPayload {
	return customRulesPayload{
		Rules:     []customRuleView{},
		Tunnels:   []tunnelView{},
		Groups:    []string{},
		Nodes:     []string{},
		Builtins:  []string{},
		Providers: []string{},
		RuleTypes: []configcheck.RuleSpec{},
	}
}

// tunnelScopeParam 从 URL 路径中解析作用域段（订阅名 / 规则集档位 / custom）。
func tunnelScopeParam(r *http.Request, prefix string) (string, bool) {
	raw := strings.TrimPrefix(r.URL.Path, config.BaseURL+prefix)
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return "", false
	}
	value, err := url.QueryUnescape(raw)
	if err != nil || value == "" {
		return "", false
	}
	return value, true
}

// appendTunnel 复制一份隧道切片再追加，避免与全局快照共享底层数组。
func appendTunnel(tunnels []config.Tunnel, tunnel config.Tunnel) []config.Tunnel {
	return append(config.CopyTunnels(tunnels), tunnel)
}

// containsTunnelID 判定隧道 id 是否存在于给定列表。
func containsTunnelID(tunnels []config.Tunnel, id string) bool {
	for _, tunnel := range tunnels {
		if tunnel.ID == id {
			return true
		}
	}
	return false
}

// newCustomTunnelID 生成隧道标识（前端编辑/排序/删除/开关单条隧道使用）。
func newCustomTunnelID() string {
	return newCustomRuleID()
}

// buildTunnelViews 组装下发前端的隧道视图列表。
//
// ctx 为 nil（切换模式的订阅文件尚未就绪）时统一标注不可校验的原因：与规则列表一致，
// 此时界面上仍要能看到已保存的隧道，但必须说明「现在改不了、也校验不了」。
func buildTunnelViews(tunnels []config.Tunnel, ctx *configgen.RuleContext, notReadyReason string) []tunnelView {
	views := make([]tunnelView, 0, len(tunnels))
	if ctx == nil {
		for _, tunnel := range tunnels {
			view := newTunnelView(tunnel)
			view.Reason = notReadyReason
			views = append(views, view)
		}
		return views
	}
	reasons := configgen.TunnelReasons(tunnels, ctx.Env)
	for i, tunnel := range tunnels {
		view := newTunnelView(tunnel)
		if reason := reasons[i]; reason != "" {
			view.Reason = reason
		} else {
			view.Valid = true
		}
		views = append(views, view)
	}
	return views
}

// newTunnelView 把存储模型折算成视图（Enabled 由 *bool 折算为显式布尔）。
func newTunnelView(tunnel config.Tunnel) tunnelView {
	return tunnelView{
		ID:      tunnel.ID,
		Network: tunnel.Network,
		Address: strings.TrimSpace(tunnel.Address),
		Target:  strings.TrimSpace(tunnel.Target),
		Proxy:   strings.TrimSpace(tunnel.Proxy),
		Enabled: tunnel.IsEnabled(),
		Line:    displayTunnelLine(tunnel),
	}
}

// displayTunnelLine 生成隧道的展示文本：`network,address,target[,proxy]`。
//
// 与 wiki 的单行写法一致（network 用 `/` 连接），用户看到的即是他在别处抄得到的形态。
// 非法取值也照常拼接：用户需要看到自己写了什么才能修正或删除。
//
// 目标按**写入配置的形态**展示（只写域名时补上默认端口）：否则界面显示 `example.com`、
// 配置里却是 `example.com:80`，用户无从判断实际转发到了哪里。
func displayTunnelLine(tunnel config.Tunnel) string {
	networks := tunnel.Network
	if normalized, err := config.NormalizeTunnelNetworks(networks); err == nil {
		networks = normalized
	}
	target := strings.TrimSpace(tunnel.Target)
	if normalized, err := config.NormalizeTunnelTarget(target); err == nil {
		target = normalized
	}
	parts := []string{strings.Join(networks, "/"), strings.TrimSpace(tunnel.Address), target}
	if proxy := strings.TrimSpace(tunnel.Proxy); proxy != "" {
		parts = append(parts, proxy)
	}
	return strings.Join(parts, ",")
}
