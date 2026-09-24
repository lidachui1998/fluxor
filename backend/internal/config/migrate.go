package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"fluxor/internal/logx"
)

// 本文件负责「旧版单文件 → 新版分文件」的一次性迁移。
//
// 旧布局：全部状态挤在 fluxor.json（订阅配置 + 规则 + 隧道 + 元数据 + TProxy 字段）。
// 新布局：settings.json / rules.json / tunnels.json / subscription-meta.json / tproxy.json。
//
// 迁移原则：
//   - **幂等**：settings.json 已存在即视为新布局在用，直接跳过；
//   - **可回滚**：旧文件不删除，改名为 fluxor.json.migrated-<时间戳>；
//   - **要么全成功、要么不改名**：任一文件写入失败就保留旧文件，下次启动重试，
//     避免出现「旧文件已改名、新文件只写了一半」的不可恢复状态；
//   - **损坏不阻断启动**：旧文件解析失败时备份为 .corrupt-<时间戳> 后按全新安装处理。

// legacyConfig 旧 fluxor.json 的完整形状：订阅配置 + TProxy 旁路字段。
//
// 这里刻意内嵌 SubscribeConfig（而不是新布局的类型）：迁移读的是**旧格式**，
// 其字段划分（规则/隧道/元数据都在订阅里）与新布局不同。
type legacyConfig struct {
	SubscribeConfig
	TproxyEnabled       *bool    `json:"tproxy_enabled"`
	TproxyProxyLocal    *bool    `json:"tproxy_proxy_local"`
	TproxyIPv6          *bool    `json:"tproxy_ipv6"`
	TproxyDstExceptions []string `json:"tproxy_dst_exceptions"`
	TproxySrcExceptions []string `json:"tproxy_src_exceptions"`
	// TproxyExceptionsOld 更早版本的合并字段（只有目的绕过），读取时迁移。
	TproxyExceptionsOld []string `json:"tproxy_exceptions"`
}

// MigrateLegacyFiles 在启动载入前完成迁移（幂等，可反复调用）。
func MigrateLegacyFiles() {
	legacyPath := FluxorConfigFile

	if _, err := os.Stat(FluxorSettingsFile); err == nil {
		// 新布局已在用：旧文件若还在，说明是上一次迁移后的残留（或用户手工放回的），
		// 只能忽略——反过来读它会与新文件产生两套真相。
		if _, err := os.Stat(legacyPath); err == nil {
			logx.Warn(logx.ModuleConfig, "legacy config %s is ignored because %s already exists",
				legacyPath, FluxorSettingsFile)
		}
		return
	}

	data, err := os.ReadFile(legacyPath)
	if err != nil {
		// 没有旧文件：全新安装，各 store 首次写入时自然生成
		return
	}

	var legacy legacyConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%s", legacyPath, time.Now().Format("20060102-150405"))
		if werr := os.WriteFile(backup, data, 0644); werr != nil {
			logx.Error(logx.ModuleConfig, "failed to back up corrupt legacy config to %s: %v", backup, werr)
		}
		logx.Error(logx.ModuleConfig, "legacy config %s is not valid JSON, starting from defaults (backup: %s): %v",
			legacyPath, backup, err)
		return
	}

	settings, rules, tunnels, meta, tproxyState := splitLegacy(legacy)

	files := []struct {
		name string
		path string
		v    any
	}{
		{settingsStore.Name, FluxorSettingsFile, settings},
		{rulesStore.Name, FluxorRulesFile, rules},
		{tunnelsStore.Name, FluxorTunnelsFile, tunnels},
		{metaStore.Name, FluxorMetaFile, meta},
		{"tproxy.json", FluxorTproxyFile, tproxyState},
	}
	for _, f := range files {
		out, err := json.MarshalIndent(f.v, "", "  ")
		if err != nil {
			logx.Error(logx.ModuleConfig, "failed to encode %s during migration: %v", f.name, err)
			return // 保留旧文件，下次启动重试
		}
		if err := writeFileAtomic(f.path, append(out, '\n')); err != nil {
			logx.Error(logx.ModuleConfig, "failed to write %s during migration, keeping %s for retry: %v",
				f.name, legacyPath, err)
			return
		}
	}

	archived := fmt.Sprintf("%s.migrated-%s", legacyPath, time.Now().Format("20060102-150405"))
	if err := os.Rename(legacyPath, archived); err != nil {
		// 新文件已就绪，仅归档失败：记日志即可，下次启动因 settings.json 存在而跳过
		logx.Warn(logx.ModuleConfig, "config split done but archiving %s failed: %v", legacyPath, err)
	} else {
		logx.Info(logx.ModuleConfig, "config split done: %s archived as %s (subscriptions=%d)",
			legacyPath, archived, len(settings.Subscriptions))
	}
}

// splitLegacy 把旧格式拆成新布局的五份内容。
func splitLegacy(legacy legacyConfig) (Settings, RulesFile, TunnelsFile, MetaFile, TproxyFile) {
	settings := Settings{
		ProxyPort:          legacy.ProxyPort,
		TproxyPort:         legacy.TproxyPort,
		PanelPort:          legacy.PanelPort,
		PanelSecret:        legacy.PanelSecret,
		RuleGroup:          legacy.RuleGroup,
		UIPanel:            legacy.UIPanel,
		MetaBackendURL:     legacy.MetaBackendURL,
		Mode:               legacy.Mode,
		ActiveSubscription: legacy.ActiveSubscription,
		CustomNodes:        copyCustomNodes(legacy.CustomNodes),
	}
	// 旧文件的默认值语义（键缺失即默认值）在迁移时补一次
	if settings.ProxyPort == 0 {
		settings.ProxyPort = defaultProxyPort
	}
	if settings.PanelPort == 0 {
		settings.PanelPort = defaultPanelPort
	}
	if settings.TproxyPort == 0 {
		settings.TproxyPort = defaultTproxyPort
	}
	if settings.Mode == "" {
		settings.Mode = ModeMerge
	}
	if settings.RuleGroup == "" {
		settings.RuleGroup = RuleGroupBase
	}
	if settings.UIPanel == "" {
		settings.UIPanel = "metacubexd"
	}

	rules := NewRulesFile()
	tunnels := NewTunnelsFile()
	meta := NewMetaFile()

	for _, sub := range legacy.Subscriptions {
		settings.Subscriptions = append(settings.Subscriptions, SubscriptionRef{
			Name:           sub.Name,
			URL:            sub.URL,
			UpdateInterval: sub.UpdateInterval,
			HealthInterval: sub.HealthInterval,
			Prefix:         sub.Prefix,
		})
		if len(sub.CustomRules) > 0 {
			rules.BySub[sub.Name] = copyRules(sub.CustomRules)
		}
		if len(sub.Tunnels) > 0 {
			tunnels.BySub[sub.Name] = CopyTunnels(sub.Tunnels)
		}
		if sub.UpdatedAt != "" || len(sub.SubscriptionInfo) > 0 {
			meta.Subscriptions[sub.Name] = SubscriptionMeta{
				UpdatedAt: sub.UpdatedAt,
				Info:      copyAnyMap(sub.SubscriptionInfo),
			}
		}
	}
	for scope, list := range legacy.MergeCustomRules {
		if len(list) > 0 {
			rules.Merge[scope] = copyRules(list)
		}
	}
	if len(legacy.CustomModeRules) > 0 {
		rules.CustomMode = copyRules(legacy.CustomModeRules)
	}
	for scope, list := range legacy.MergeTunnels {
		if len(list) > 0 {
			tunnels.Merge[scope] = CopyTunnels(list)
		}
	}
	if len(legacy.CustomModeTunnels) > 0 {
		tunnels.CustomMode = CopyTunnels(legacy.CustomModeTunnels)
	}

	// TProxy：开关缺省值与 tproxy 包的既有语义一致（本机接管默认开、IPv6 默认关）
	tproxyState := TproxyFile{ProxyLocal: true}
	if legacy.TproxyEnabled != nil {
		tproxyState.Enabled = *legacy.TproxyEnabled
	}
	if legacy.TproxyProxyLocal != nil {
		tproxyState.ProxyLocal = *legacy.TproxyProxyLocal
	}
	if legacy.TproxyIPv6 != nil {
		tproxyState.IPv6 = *legacy.TproxyIPv6
	}
	tproxyState.DstExceptions = legacy.TproxyDstExceptions
	if len(tproxyState.DstExceptions) == 0 {
		tproxyState.DstExceptions = legacy.TproxyExceptionsOld
	}
	tproxyState.SrcExceptions = legacy.TproxySrcExceptions

	return settings, rules, tunnels, meta, tproxyState
}
