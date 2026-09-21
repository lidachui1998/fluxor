package subscription

import (
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/httpx"
	"fluxor/internal/nodespec"
	"log"
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
			log.Printf("编码订阅配置失败: %v", err)
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
		// 与 /subscribe/generate 同一道理：自定义规则由规则接口维护，
		// 这个整体覆盖写接口若在请求体缺少这些字段时把它们清空，
		// 会导致运行配置与磁盘状态双双丢规则（详见 config.InheritRuleOwnedFields）。
		config.Mu.RLock()
		prev := config.Current
		config.Mu.RUnlock()
		newConfig.InheritRuleOwnedFields(prev)

		// 自定义节点列表与订阅列表一样由本接口整体覆盖：落库前先校验并归一化
		// （节点名、字段类型与必填、与模板组名冲突），否则内核会拒绝加载整份配置。
		nodes, err := normalizeCustomNodes(newConfig.CustomNodes)
		if err != nil {
			httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		newConfig.CustomNodes = nodes

		config.Mu.Lock()
		config.Current = newConfig
		config.Mu.Unlock()

		if err := config.SaveSubscribeConfig(); err != nil {
			log.Printf("保存订阅配置失败: %v", err)
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
