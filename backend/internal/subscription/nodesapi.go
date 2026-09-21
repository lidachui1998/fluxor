package subscription

import (
	"fluxor/internal/httpx"
	"fluxor/internal/nodespec"
	"net/http"
)

// HandleNodeProtocolsAPI 处理 GET /subscribe/node-protocols：
// 返回自定义模式下可添加的协议及其字段表（类型、默认值、可选值、是否必填）。
//
// 字段表由后端单点维护（nodespec）：前端只按声明渲染表单，不在前端复刻一份协议
// 清单，因此新增协议或调整默认值不需要发前端版本。
func HandleNodeProtocolsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	httpx.RespondJSON(w, http.StatusOK, map[string]any{
		"protocols": nodespec.Protocols(),
	})
}
