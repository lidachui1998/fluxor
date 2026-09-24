package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/configgen"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"net/http"
)

// generateCustomConfig 处理自定义模式的「保存并应用」：
// 归一化节点 → 落库 → 用落库后的快照生成 config.yaml → 重载内核。
//
// 与其它两种模式的差异：
//   - 不确保/不下载订阅文件（订阅不参与生成，定时器也不会启动）；
//   - 不抓取订阅元数据（没有 provider 可查）；
//   - 生成走 configgen.GenerateCustomConfig：模板骨架 + 标准规则集 + proxies 块。
//
// 与另两条链路一致，生成输入是**落库后**的快照而不是请求体：手工节点与自定义规则
// 都要从快照里取（请求体里没有自定义规则），否则每次保存都会把该模式的规则与隧道
// 从 config.yaml 里抹掉。
func generateCustomConfig(w http.ResponseWriter, req config.SubscribeConfig) {
	// 手工节点已在 decodeSettingsRequest 里归一化过，这里直接用
	if err := config.SaveSettings(req); err != nil {
		logx.Error(logx.ModuleSub, "saving settings failed: mode=custom err=%v", err)
		httpx.WriteJSONError(w, http.StatusInternalServerError, "保存配置失败: "+err.Error())
		return
	}

	// 重置定时器：自定义模式不使用订阅，StartAllTimers 会因 mode != switch 而全部不启动
	resetTimers()

	snap := config.CurrentSnapshot()

	if err := configgen.GenerateCustomConfig(snap); err != nil {
		httpx.WriteJSONError(w, http.StatusInternalServerError, "生成配置文件失败: "+err.Error())
		return
	}

	if snap.MetaBackendURL != "" {
		if err := modifyMetaConfig(snap.MetaBackendURL); err != nil {
			logx.Warn(logx.ModuleSub, "updating MetaCubeXD backend URL failed: %v", err)
		}
	}

	finishGenerate(w, "自定义节点配置已生成并成功重载内核", "")
}
