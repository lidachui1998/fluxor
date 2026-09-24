package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/logx"
	"fmt"
	"path/filepath"
)

// subscriptionSnapshot 参数，用于在锁外执行下载
type subscriptionSnapshot struct {
	sub        config.Subscription
	idx        int
	proxiesDir string
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

	// 不再「先删旧文件再下载」：下载走原子替换（downloadToFile 写入同目录临时文件后
	// rename），失败时本地那份可用副本原样保留。旧实现的 os.Remove 会让一次网络抖动
	// 直接抹掉用户唯一可用的节点文件——而 config.yaml 的生成与内核重启都依赖它。
	logx.Debug(logx.ModuleSub, "downloading subscription %q to %s", subName, targetFile)

	updatedAt, subInfo, err = downloadToFile(snap.sub, snap.idx, targetFile)
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

// runtimeSourceIsActive 复查「该订阅此刻仍是切换模式下的激活订阅」，并返回此刻的
// 规则/隧道副本。
//
// 复查是必需的：下载最长可达约 30 秒，期间用户完全可以切走激活订阅、改模式或删掉
// 这个订阅，而 snap 是下载**开始前**取的快照。若直接按 snap 里的判断写 config.yaml，
// 运行配置会被覆盖成「已经不是激活订阅」的那一份并触发内核重载——界面显示 A、
// 实际生效 B，且没有任何提示。
func runtimeSourceIsActive(subName string) (rules []config.CustomRule, tunnels []config.Tunnel, active bool) {
	config.Mu.RLock()
	defer config.Mu.RUnlock()

	if config.Current.Mode != config.ModeSwitch || config.Current.ActiveSubscription != subName {
		return nil, nil, false
	}
	// 在锁内复制：Current 的切片由全局锁保护，锁外不得引用其底层数组
	return copyCustomRules(config.Current, subName), copyCustomTunnels(config.Current, subName), true
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

	rules, tunnels, stillActive := runtimeSourceIsActive(subName)
	if !stillActive {
		logx.Debug(logx.ModuleSub, "subscription %q is not the active one any more, runtime config left untouched", subName)
		return false, nil
	}

	logx.Debug(logx.ModuleSub, "subscription %q is active, writing runtime config %s", subName, config.ConfigTarget)
	result, err := writeRuntimeConfig(subName, rules, tunnels)
	if err != nil {
		logx.Error(logx.ModuleSub, "writing runtime config failed: %v", err)
		return false, err
	}
	logx.Info(logx.ModuleSub, "runtime config written: custom_rules_applied=%d custom_rules_skipped=%d",
		result.Applied, len(result.Skipped))
	return true, nil // 需要重载
}
