package appupdate

import (
	"encoding/json"
	"fluxor/internal/buildinfo"
	"fluxor/internal/httpx"
	"net/http"
	"strings"
)

// HandleCheckUpdate 检查 Fluxor 自身是否有新版本。
//
// 当前版本取自编译期注入的 buildinfo.Version，前端无需再通过 ?current= 传入。
// 版本未知（本地未注入时）不以「有新版本」误导用户，而是明确回报可比较状态。
//
// 带 ?force=1（弹窗里手动点「检查更新」）时无视缓存冷却直接回源查询，
// 成功后把缓存与冷却时间一并续期；不带该参数（页面加载时的自动检查）沿用缓存。
func HandleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	force := wantsForceCheck(r)
	current := stripVersionSuffix(buildinfo.Name())

	// 本地未注入版本号（如直接 go run）：无法与远端 tag 做有意义比较，
	// 如实返回 versionUnknown，避免前端弹出「发现新版本」。
	if !buildinfo.IsKnown() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"hasUpdate":      false,
			"versionUnknown": true,
			"current":        current,
		})
		return
	}

	// 尝试获取完整 Release 信息（含更新日志）
	rel, err := getLatestReleaseInfo(force)
	if err != nil {
		// 降级方案：仅获取版本号（不返回 releaseNotes）
		latest, err2 := getLatestVersion(force)
		if err2 != nil {
			httpx.WriteJSONError(w, http.StatusServiceUnavailable, "failed to check update: "+err2.Error())
			return
		}
		hasUpdate := compareVersions(latest, current) > 0
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"hasUpdate": hasUpdate,
			"latest":    latest,
			"current":   current,
			// 无 releaseNotes 字段
		})
		return
	}

	hasUpdate := compareVersions(rel.TagName, current) > 0
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"hasUpdate":    hasUpdate,
		"latest":       rel.TagName,
		"current":      current,
		"releaseNotes": rel.Body,
	})
}

// wantsForceCheck 判断本次检查是否要求跳过缓存冷却（?force=1 / ?force=true）。
//
// 只有弹窗里的「检查更新」按钮会带上它：用户手动点按钮就是要一个「此刻的答案」，
// 不能被后端为自动检查设的 10 分钟冷却挡住。检查成功后冷却重新起算，
// 随后的自动检查因此不会立刻再打一次 GitHub。
func wantsForceCheck(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("force"))) {
	case "1", "true":
		return true
	}
	return false
}
