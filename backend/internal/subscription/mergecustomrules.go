package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/httpx"
	"net/http"
	"net/url"
	"strings"
)

// mergeCustomRulesRoutePath 是融合模式自定义规则接口的路径前缀。
const mergeCustomRulesRoutePath = "/subscribe/merge-custom-rules/"

// HandleMergeCustomRulesAPI 处理融合模式的模板级自定义规则：
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
// 只服务融合模式：**自定义模式有自己独立的一份规则与入口**（见 custommoderules.go），
// 两者互不影响；增/改/排序/删的公共流程在 templaterules.go。
//
// 所有写操作即时持久化到 fluxor.json；若该档位正是当前生效的 rule_group，
// 则重新生成 config.yaml 并热重载内核。弹窗本身不需要「保存」动作。
func HandleMergeCustomRulesAPI(w http.ResponseWriter, r *http.Request) {
	ruleGroup, ok := mergeRuleGroupParam(r)
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少规则集参数")
		return
	}
	scope, ok := mergeRuleScope(ruleGroup)
	if !ok {
		httpx.WriteJSONError(w, http.StatusBadRequest, "未知规则集: "+ruleGroup)
		return
	}
	serveRuleRequest(w, r, scope)
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
