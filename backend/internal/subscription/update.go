package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/logx"
	"fluxor/internal/subscription/download"
	"fmt"
	"os"
	"path/filepath"
)

// subscriptionSnapshot 参数，用于在锁外执行下载
type subscriptionSnapshot struct {
	sub        config.Subscription
	idx        int
	mode       string
	isActive   bool
	proxiesDir string
	// customRules / tunnels 是该订阅自定义规则与流量隧道的锁内副本。
	//
	// 必须在锁内复制：config.Current.Subscriptions 的底层数组由全局锁保护，
	// 若在锁外直接遍历该切片读取规则/隧道，会与并发的增删构成数据竞争。
	customRules []config.CustomRule
	tunnels     []config.Tunnel
	// cfg 是锁内对 config.Current 的值拷贝，供锁外打补丁时读取标量字段
	// （端口/密钥/面板等）。注意其 Subscriptions 与全局共享同一底层数组，
	// 因此锁外只允许读取标量字段，不得遍历该切片。
	cfg config.SubscribeConfig
}

// takeSubscriptionSnapshot 在锁内取一份订阅快照（供锁外下载使用）。
//
// 锁只用于读取内存字段，不覆盖任何网络 IO。
func takeSubscriptionSnapshot(subName string) (subscriptionSnapshot, bool) {
	config.Mu.RLock()
	defer config.Mu.RUnlock()

	snap := subscriptionSnapshot{
		idx:        -1,
		mode:       config.Current.Mode,
		proxiesDir: filepath.Join(config.CoreWorkDir, "proxies"),
		cfg:        config.Current,
	}
	for i := range config.Current.Subscriptions {
		if config.Current.Subscriptions[i].Name == subName {
			snap.idx = i
			snap.sub = config.Current.Subscriptions[i]
			break
		}
	}
	if snap.idx == -1 {
		return snap, false
	}
	snap.isActive = snap.mode == "switch" && config.Current.ActiveSubscription == subName
	snap.customRules = copyCustomRules(config.Current, subName)
	snap.tunnels = copyCustomTunnels(config.Current, subName)
	return snap, true
}

// applySubscriptionMetadata 落库下载得到的元数据。
//
// 只写 subscription-meta.json（config.SaveSubscriptionMeta 内部会重新组装 Current），
// 因此不再需要 config.Mu，也不会碰订阅注册表——定时更新只重写这一份小文件。
// 元数据写失败不影响本次更新的结果，如实记日志即可。
func applySubscriptionMetadata(subName, updatedAt string, subInfo map[string]interface{}) {
	if err := config.SaveSubscriptionMeta(subName, updatedAt, subInfo); err != nil {
		logx.Error(logx.ModuleSub, "saving metadata for subscription %q failed: %v", subName, err)
	}
}

// fetchAndPatchSubscription 在【锁外】执行下载与打补丁，返回元数据。
//
// 下载链路为「直连 → 失败则回退临时内核」，两阶段各以 15 秒为上限且不重试，
// 即单次更新最长约 30 秒。此前调用方是「持 config.Mu 写锁」调用本流程，
// 而该锁被 core.CoreRequest、wsproxy、quality、tproxy、healthcheck 等 10 处
// 读取点共用——等于一次订阅更新就会阻塞整个面板。故拆分为：
// 锁内取快照 → 锁外下载 → 锁内写回。
func fetchAndPatchSubscription(snap subscriptionSnapshot, subName string) (updatedAt string, subInfo map[string]interface{}, targetFile string, err error) {
	targetFile = filepath.Join(snap.proxiesDir, config.SanitizeSubscriptionFileName(subName))

	// 强制删除已有文件（确保重新下载）
	if err := os.Remove(targetFile); err != nil && !os.IsNotExist(err) {
		return "", nil, targetFile, fmt.Errorf("删除旧文件失败: %w", err)
	}

	logx.Debug(logx.ModuleSub, "downloading subscription %q to %s", subName, targetFile)

	updatedAt, subInfo, err = download.DownloadSubscriptionFile(snap.sub, snap.idx, targetFile)
	if err != nil {
		logx.Error(logx.ModuleSub, "download of subscription %q failed: %v", subName, err)
		return "", nil, targetFile, fmt.Errorf("下载失败: %w", err)
	}
	logx.Debug(logx.ModuleSub, "metadata updated: updated_at=%s", updatedAt)

	// 打补丁。补丁只依赖快照中的标量配置，无需（也不应）持有全局锁。
	logx.Debug(logx.ModuleSub, "patching subscription file: %s", targetFile)
	if err := patchSubscriptionFile(targetFile, snap.cfg); err != nil {
		logx.Error(logx.ModuleSub, "patching subscription file %s failed: %v", targetFile, err)
		return "", nil, targetFile, fmt.Errorf("打补丁失败: %w", err)
	}
	logx.Debug(logx.ModuleSub, "subscription file patched")
	return updatedAt, subInfo, targetFile, nil
}

// updateSubscriptionInSwitchMode 切换模式下的订阅更新逻辑，返回 needsReload 表示是否需要重载内核。
//
// 注意：本函数会执行网络下载，调用方【不得】持有 config.Mu。
// 内部自行在锁内取快照、锁外下载、锁内写回元数据。
func updateSubscriptionInSwitchMode(subName string) (needsReload bool, err error) {
	snap, ok := takeSubscriptionSnapshot(subName)
	if !ok {
		return false, fmt.Errorf("订阅 %s 不存在", subName)
	}

	updatedAt, subInfo, _, err := fetchAndPatchSubscription(snap, subName)
	if err != nil {
		return false, err
	}
	applySubscriptionMetadata(subName, updatedAt, subInfo)

	// 如果该订阅是当前激活的订阅，则复制到 configTarget，并标记需要重载
	if snap.isActive {
		logx.Debug(logx.ModuleSub, "subscription %q is active, writing runtime config %s", subName, config.ConfigTarget)
		// 自定义规则与隧道取锁内快照（snap.customRules / snap.tunnels），避免在锁外引用全局切片
		result, err := writeRuntimeConfig(subName, snap.customRules, snap.tunnels)
		if err != nil {
			logx.Error(logx.ModuleSub, "writing runtime config failed: %v", err)
			return false, err
		}
		logx.Info(logx.ModuleSub, "runtime config written: custom_rules_applied=%d custom_rules_skipped=%d",
			result.Applied, len(result.Skipped))
		return true, nil // 需要重载
	}

	logx.Debug(logx.ModuleSub, "subscription %q is not active, skipping runtime config copy and reload", subName)
	return false, nil
}
