package subscription

import (
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"fluxor/internal/nodespec"
	"net/http"
)

// HandleSubscribeConfigAPI 处理 GET /subscribe/config 和 POST /subscribe/config
func HandleSubscribeConfigAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config.Mu.RLock()
		defer config.Mu.RUnlock()

		// 自定义节点在磁盘上只存「与协议默认值不同的字段」，界面需要完整取值才能
		// 正确预填表单，因此在读取路径上补齐（内存与磁盘仍保持精简）。
		// 复制一份结构体再替换切片：不能就地改写 config.Current。
		view := config.Current
		view.CustomNodes = nodespec.MaterializeAll(view.CustomNodes)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(view); err != nil {
			logx.Error(logx.ModuleConfig, "encoding subscription config failed: %v", err)
		}

	case http.MethodPost:
		var newConfig config.SubscribeConfig
		if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
			httpx.WriteJSONError(w, http.StatusBadRequest, "无效的请求格式: "+err.Error())
			return
		}
		if newConfig.MetaBackendURL != "" && !httpx.BackendURLRegex.MatchString(newConfig.MetaBackendURL) {
			httpx.WriteJSONError(w, http.StatusBadRequest, "外部面板后端地址格式不正确")
			return
		}
		// 订阅名会用作节点文件名与 provider 键，必须在此拦截非法字符
		if err := config.ValidateSubscriptionNames(newConfig.Subscriptions); err != nil {
			httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 请求体里的自定义规则与流量隧道一律忽略：它们存在 rules.json / tunnels.json 里，
		// 由各自的专用接口维护；SaveSettings 只取设置与订阅注册表，因此请求体里的过期快照
		// 无法覆盖服务端（这正是旧 AdoptServerOwnedFields 补丁要解决的问题）。

		// 自定义节点列表与订阅列表一样由本接口整体覆盖：落库前先校验并归一化
		// （节点名、字段类型与必填、与模板组名冲突），否则内核会拒绝加载整份配置。
		nodes, err := normalizeCustomNodes(newConfig.CustomNodes)
		if err != nil {
			httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		newConfig.CustomNodes = nodes

		// 落库与重组装 Current 由 SaveSettings 一并完成（内部取 config.Mu，故此处不得持锁）
		if err := config.SaveSettings(newConfig); err != nil {
			logx.Error(logx.ModuleConfig, "saving settings failed: %v", err)
			httpx.WriteJSONError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
			return
		}

		// 重置定时器（先停止再启动）
		StopAllTimers()
		StartAllTimers()

		httpx.RespondJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"message": "配置已保存",
		})

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
