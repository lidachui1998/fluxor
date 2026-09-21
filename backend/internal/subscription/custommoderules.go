package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/httpx"
	"net/http"
	"net/url"
	"strings"
)

// customModeRulesRoutePath 是自定义模式自定义规则接口的路径前缀。
const customModeRulesRoutePath = "/subscribe/custom-mode-rules/"

// HandleCustomModeRulesAPI 处理自定义模式的自定义规则（与融合模式完全独立的一份）：
//
//	GET    /subscribe/custom-mode-rules/{scope}        查询规则与可选目标
//	POST   /subscribe/custom-mode-rules/{scope}        新增一条规则
//	PUT    /subscribe/custom-mode-rules/{scope}        修改一条已存在的规则（body 带 id）
//	PATCH  /subscribe/custom-mode-rules/{scope}        调整顺序（{id, direction}）
//	DELETE /subscribe/custom-mode-rules/{scope}?id=xx  删除一条规则
//
// {scope} 恒为 custom：自定义模式只有这一份规则（存在 cfg.CustomModeRules，不按档位分表），
// 保留该段是为了让前端那套「endpoint + 作用域」的调用方式对三种入口完全一致。
//
// 与融合模式的两点差异：
//  1. 规则单独存放，两边各改各的，切模式不会看到另一边的规则；
//  2. 可选目标里包含手工节点名——自定义模式的节点写死在 config.yaml 的 proxies 里，
//     规则指向节点名内核能解析（融合模式的节点来自 provider，静态校验看不到）。
//
// 写操作即时持久化；自定义模式下规则恒生效，因此每次改动都会重新生成 config.yaml
// 并热重载内核。
func HandleCustomModeRulesAPI(w http.ResponseWriter, r *http.Request) {
	scope, ok := customModeScopeParam(r)
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "未知作用域（应为 "+config.RuleScopeCustom+"）")
		return
	}
	serveRuleRequest(w, r, scope)
}

// customModeScopeParam 解析并校验路径里的作用域段。
func customModeScopeParam(r *http.Request) (ruleScope, bool) {
	raw := strings.TrimPrefix(r.URL.Path, config.BaseURL+customModeRulesRoutePath)
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return ruleScope{}, false
	}
	name, err := url.QueryUnescape(raw)
	if err != nil || name != config.RuleScopeCustom {
		return ruleScope{}, false
	}
	return customModeRuleScope(), true
}
