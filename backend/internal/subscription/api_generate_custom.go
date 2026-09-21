package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/configgen"
	"fluxor/internal/core"
	"fluxor/internal/httpx"
	"log"
	"net/http"
)

// generateCustomConfig 处理自定义模式的「保存并应用」：
// 归一化节点 → 落库 → 生成 config.yaml → 重载内核。
//
// 与其它两种模式的差异：
//   - 不确保/不下载订阅文件（订阅不参与生成，定时器也要停掉）；
//   - 不抓取订阅元数据（没有 provider 可查）；
//   - 生成走 configgen.GenerateCustomConfig：模板骨架 + 标准规则集 + proxies 块。
func generateCustomConfig(w http.ResponseWriter, cfg config.SubscribeConfig) {
	nodes, err := normalizeCustomNodes(cfg.CustomNodes)
	if err != nil {
		httpx.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.CustomNodes = nodes

	config.Mu.Lock()
	config.Current = cfg
	config.Mu.Unlock()
	if err := config.SaveSubscribeConfig(); err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}

	// 重置定时器：自定义模式不使用订阅，StartAllTimers 会因 mode != switch 而全部不启动
	StopAllTimers()
	StartAllTimers()

	if err := configgen.GenerateCustomConfig(cfg); err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "生成配置文件失败: "+err.Error())
		return
	}

	if cfg.MetaBackendURL != "" {
		if err := modifyMetaConfig(cfg.MetaBackendURL); err != nil {
			log.Printf("[WARN] 修改 MetaCubeXD 后端地址失败: %v", err)
		}
	}

	if err := core.ReloadCore(); err != nil {
		httpx.RespondJSON(w, http.StatusOK, map[string]string{
			"status":  "warning",
			"message": "配置文件已生成，但重载内核失败: " + err.Error(),
		})
		return
	}

	httpx.RespondJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"message": "自定义节点配置已生成并成功重载内核",
	})
}
