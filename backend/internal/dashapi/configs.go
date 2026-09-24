package dashapi

import (
	"bytes"
	"fluxor/internal/core"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"fluxor/internal/tproxy"
	"io"
	"net/http"
	"strings"
)

// HandleConfigsAPI 处理配置的获取、修改和重载
func HandleConfigsAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := core.CoreRequest("GET", "/configs", nil)
		if err != nil {
			httpx.WriteJSONError(w, http.StatusBadGateway, "获取配置失败: "+err.Error())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		// 转发内核状态码：恒回 200 会让前端的 resp.ok 形同虚设，
		// 内核的错误响应体会被当成数据解析（例如把 {"message":...} 当规则列表）
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)

	case http.MethodPatch:
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.WriteJSONError(w, http.StatusBadRequest, "读取请求体失败: "+err.Error())
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		// tproxy-port 是唯一一个「面板自己也在用」的内核字段：内核按它监听，nft 规则按它
		// 重定向，两者必须始终是同一个数。因此这里的处理顺序与来源都收敛到 settings.json：
		//
		//   1. 校验取值（0 表示禁用，否则必须落在合法端口区间）；
		//   2. **先落库**——settings.json 是唯一真相。若只改内核，下一次「保存并应用」
		//      会按 settings 里的旧值重新生成 config.yaml，把内核端口改回去；而开关路径
		//      （/config/tproxy）本来就按 settings 重建规则，于是三处口径互相矛盾；
		//   3. 再改内核。**改内核失败就把 settings 回滚**，否则库里的端口与内核实际
		//      监听的端口不一致，下一次生成会以「库里是对的」为由改坏正在运行的内核；
		//   4. 内核接受后才重建防火墙规则，并复用 ReapplyTproxyRules 的回滚语义
		//      （失败即清规则 + 关开关 + 如实回报），绝不出现「面板显示已启用、实际无规则」。
		newPort, portChanged, err := tproxyPortFromPatch(bodyBytes)
		if err != nil {
			httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		// 端口置 0 = 禁用该端口 = 内核不再监听。此时若 TProxy 仍处于启用态，规则还在把
		// 流量导向一个没人监听的端口（断网），而面板显示「已启用」——必须拒绝，并告诉
		// 用户先关掉接管。放在落库之前，保证被拒时状态完全不变。
		if portChanged && newPort == 0 && tproxy.GetTproxyState() {
			httpx.WriteJSONError(w, http.StatusBadRequest,
				"TPROXY 正在接管，不能把端口设为 0（内核不再监听该端口会让流量失去出口）；请先关闭 TPROXY")
			return
		}

		var prevPort int
		if portChanged {
			// persistTproxyPort 会把「与当前取值相同」归一为 changed=false：端口没变就
			// 什么都不做，避免「点一下输入框又点走」白白重建一次防火墙规则（见该函数注释）。
			prevPort, portChanged, err = persistTproxyPort(newPort)
			if err != nil {
				logx.Error(logx.ModuleTproxy, "failed to persist tproxy port %d: %v", newPort, err)
				httpx.WriteJSONError(w, http.StatusInternalServerError, "保存 TPROXY 端口失败: "+err.Error())
				return
			}
		}

		resp, err := core.CoreRequest("PATCH", "/configs", r.Body)
		if err != nil {
			if portChanged {
				rollbackTproxyPort(prevPort)
			}
			httpx.WriteJSONError(w, http.StatusBadGateway, "修改配置失败: "+err.Error())
			return
		}
		defer resp.Body.Close()

		if portChanged {
			switch {
			case resp.StatusCode >= http.StatusBadRequest:
				// 内核拒绝这次修改：把端口改回去。规则与内核此时都还停在旧端口上，
				// 保持「什么都不动」才是自洽的——重建规则反而会指向一个内核没接受的端口。
				rollbackTproxyPort(prevPort)
			case tproxy.GetTproxyState():
				if err := tproxy.ReapplyTproxyRules(); err != nil {
					logx.Error(logx.ModuleTproxy, "failed to apply tproxy rules after port change: %v", err)
					httpx.WriteJSONError(w, http.StatusInternalServerError,
						"TPROXY 端口已更新，但规则重建失败（TProxy 已自动关闭）: "+err.Error())
					return
				}
			default:
				// 开关关闭时不重建规则（端口为 0 或未启用，规则必然无法生效），
				// 但顺手清掉任何残留——与旧实现一致，避免上一次非优雅退出留下的规则
				// 在「端口已改、还没重启面板」的窗口里继续劫持流量。
				tproxy.DisableTProxyRules()
			}
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)

	case http.MethodPut:
		if err := core.ReloadCore(); err != nil {
			httpx.WriteJSONError(w, http.StatusInternalServerError, "重载配置失败: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// HandleRestart 重启内核（POST /restart）
func HandleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSONError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	resp, err := core.CoreRequest("POST", "/restart", strings.NewReader(`{"path": "", "payload": ""}`))
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadGateway, "重启内核失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// HandleConfigsGeo 更新 GEO 数据库（POST /configs/geo）
func HandleConfigsGeo(w http.ResponseWriter, r *http.Request) {
	resp, err := core.CoreRequest("POST", "/configs/geo", nil)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadGateway, "更新 GEO 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
}

// HandleProvidersGeo 更新 GEO 数据库（回退接口，POST /providers/geo）
func HandleProvidersGeo(w http.ResponseWriter, r *http.Request) {
	resp, err := core.CoreRequest("POST", "/providers/geo", nil)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadGateway, "更新 GEO 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
}

// HandleFlushFakeIP 清空 FakeIP 缓存（POST /cache/fakeip/flush）
func HandleFlushFakeIP(w http.ResponseWriter, r *http.Request) {
	resp, err := core.CoreRequest("POST", "/cache/fakeip/flush", nil)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadGateway, "清空 FakeIP 失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(http.StatusOK)
}

// HandleFlushDNS 清空 DNS 缓存（POST /cache/dns/flush）
func HandleFlushDNS(w http.ResponseWriter, r *http.Request) {
	resp, err := core.CoreRequest("POST", "/cache/dns/flush", nil)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadGateway, "清空 DNS 缓存失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(http.StatusOK)
}

// HandleDNSQuery 执行 DNS 查询（代理 /dns/query）
func HandleDNSQuery(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	qtype := r.URL.Query().Get("type")
	path := "/dns/query?name=" + name + "&type=" + qtype
	resp, err := core.CoreRequest("GET", path, nil)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadGateway, "DNS 查询失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	// 转发内核状态码：恒回 200 会让前端的 resp.ok 形同虚设，
	// 内核的错误响应体会被当成数据解析（例如把 {"message":...} 当规则列表）
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
