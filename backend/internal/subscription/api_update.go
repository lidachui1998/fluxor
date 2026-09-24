package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/core"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"net/http"
	"net/url"
	"strings"
)

// HandleSubscribeUpdate 处理 POST /subscribe/update/{name}
func HandleSubscribeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, config.BaseURL+"/subscribe/update/")
	if path == "" {
		httpx.WriteJSONError(w, http.StatusBadRequest, "缺少订阅名称")
		return
	}
	name, err := url.QueryUnescape(path)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, "无效的订阅名称")
		return
	}

	logx.Debug(logx.ModuleSub, "update request received: subscription=%q", name)

	config.Mu.RLock()
	mode := config.Current.Mode
	config.Mu.RUnlock()

	var targetSub config.Subscription
	var found bool

	if mode == "merge" {
		// 先在主线程快速判断订阅是否存在，保证基本参数合法
		config.Mu.RLock()
		for _, s := range config.Current.Subscriptions {
			if s.Name == name {
				found = true
				break
			}
		}
		config.Mu.RUnlock()

		if !found {
			httpx.WriteJSONError(w, http.StatusNotFound, "未找到该订阅")
			return
		}

		// 启动后台协程异步调用内核更新并拉取元数据，避免阻塞 HTTP 主线程导致 504
		go func(subName string) {
			logx.Info(logx.ModuleSub, "background update started for subscription %q", subName)
			encoded := url.QueryEscape(subName)
			resp, err := core.CoreRequest("PUT", "/providers/proxies/"+encoded, nil)
			if err != nil {
				logx.Error(logx.ModuleSub, "core update of subscription %q failed: %v", subName, err)
				return
			}
			resp.Body.Close()

			// 从主内核拉取最新的元数据
			updatedAt, subInfo, err := fetchSubscriptionMetadataFromCore(subName)
			if err != nil {
				logx.Error(logx.ModuleSub, "fetching metadata for subscription %q failed: %v", subName, err)
				return
			}

			// 内核 provider 更新成功但没带回元数据（机场不下发 subscription-userinfo，
			// 或内核还没来得及重新拉取）时，保留上一次已知值并如实记一条：
			// SaveSubscriptionMeta 对这种空值本就不写，但也不能再报「已保存」。
			if updatedAt == "" && len(subInfo) == 0 {
				logx.Warn(logx.ModuleSub, "core returned no metadata for subscription %q after update, keeping previous", subName)
				return
			}

			// 持久化该订阅的元数据（SaveSubscriptionMeta 只写 subscription-meta.json，
			// 内部落盘并重组装 Current，故不得在持 config.Mu 时调用）
			if err := config.SaveSubscriptionMeta(subName, updatedAt, subInfo); err != nil {
				logx.Error(logx.ModuleSub, "saving metadata for subscription %q failed: %v", subName, err)
			} else {
				logx.Info(logx.ModuleSub, "subscription %q updated in background and metadata saved", subName)
			}
		}(name)

		// 立即向前端回传 processing 状态
		httpx.RespondJSON(w, http.StatusOK, map[string]string{
			"status":  "processing",
			"message": "订阅更新已在后台启动",
		})
		return
	} else {
		// 切换模式：启动HTTP下载或临时内核下载
		//
		// 注意：此处【不能】持有 config.Mu——updateSubscriptionInSwitchMode 内部
		// 会发起最长数十秒的网络下载，而 config.Mu 被 core.CoreRequest、wsproxy、
		// quality、tproxy 等 10 处读取点共用，持锁下载会把整个面板阻塞住。
		needsReload, err2 := updateSubscriptionInSwitchMode(name)

		// 在锁内读取供响应使用的目标订阅元数据
		config.Mu.RLock()
		for _, s := range config.Current.Subscriptions {
			if s.Name == name {
				targetSub = s
				found = true
				break
			}
		}
		config.Mu.RUnlock()

		if err2 != nil {
			httpx.WriteJSONError(w, http.StatusInternalServerError, "更新失败: "+err2.Error())
			return
		}
		if !found {
			httpx.WriteJSONError(w, http.StatusNotFound, "未找到该订阅")
			return
		}

		// 如果需要重载，在锁外调用
		if needsReload {
			logx.Info(logx.ModuleSub, "reloading core")
			if err := core.ReloadCore(); err != nil {
				logx.Error(logx.ModuleSub, "reloading core failed: %v", err)
			}
		}

		// 重置定时器
		StopAllTimers()
		StartAllTimers()

		// 元数据已由 updateSubscriptionInSwitchMode 写入 subscription-meta.json，此处无需再落库
	}

	var info interface{}
	if targetSub.SubscriptionInfo != nil {
		info = map[string]interface{}{
			"upload":    targetSub.SubscriptionInfo["upload"],
			"download":  targetSub.SubscriptionInfo["download"],
			"total":     targetSub.SubscriptionInfo["total"],
			"expire":    targetSub.SubscriptionInfo["expire"],
			"updatedAt": targetSub.UpdatedAt,
		}
	}

	httpx.RespondJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"message": "订阅 " + name + " 更新成功",
		"info":    info,
	})
}
