package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/logx"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ensureSubscriptionFiles 在切换模式下，确保每个订阅的本地节点文件可用。
//
// 该函数由「保存并应用」在**落库之后**调用，入参是落库后的权威快照
// （config.CurrentSnapshot），因此这里的订阅列表与元数据都来自 store。
//
// 两个刻意的设计：
//
//  1. **判据只看文件本身**（是否存在、是否非空），不再看 subscription_info。
//     元数据现在只存在 subscription-meta.json，请求体里根本没有这个字段——此前拿请求体
//     判断等于恒判「元数据缺失」，于是每次「保存并应用」都把全部订阅先删再重下：用户
//     只想改个端口，却要等所有订阅重新下载（每订阅最长 30s），任一机场不可达就整次保存
//     失败。而生成 config.yaml 只需要节点文件，元数据仅供界面展示，不该成为下载的理由。
//
//  2. **下载走原子替换**（downloadToFile），不再「先删后下」。失败时本地副本原样保留。
//
// 单个订阅失败不再让整次保存失败：调用方只在「激活订阅的文件确实不可用」时才阻断，
// 其余情况把失败降级为提示——用户改的是设置，不该因为某个平时不用的订阅而保存不了。
func ensureSubscriptionFiles(cfg *config.SubscribeConfig) error {
	if cfg.Mode != config.ModeSwitch {
		return nil
	}
	if len(cfg.Subscriptions) == 0 {
		return nil
	}

	proxiesDir := filepath.Join(config.CoreWorkDir, "proxies")
	if err := os.MkdirAll(proxiesDir, 0755); err != nil {
		return fmt.Errorf("创建 proxies 目录失败: %w", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(cfg.Subscriptions))
	// 并发上限 5：与内核 provider 的并发量级一致，避免同时发起十几个下载把链路压满
	sem := make(chan struct{}, 5)

	for i := range cfg.Subscriptions {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// 只读取本元素，不写回：cfg 是快照的本地副本，但多个 goroutine 同时写
			// 同一个切片的元素没有意义（结果只用于本函数内部判断），元数据一律经
			// applySubscriptionMetadata 落库，由 store 负责一致性。
			s := cfg.Subscriptions[idx]
			targetFile := filepath.Join(proxiesDir, config.SanitizeSubscriptionFileName(s.Name))

			if info, err := os.Stat(targetFile); err != nil || info.Size() == 0 {
				updatedAt, subInfo, err := downloadToFile(s, idx, targetFile)
				if err != nil {
					errCh <- fmt.Errorf("订阅 %s 下载失败: %w", s.Name, err)
					return
				}
				// 抓到元数据就落库（只写 subscription-meta.json）。
				// 此前 ensure 路径抓到元数据后直接丢掉，卡片要等到下一次手动/定时更新
				// 才有流量与到期信息，与 AGENTS 3.5 的说明不符。
				applySubscriptionMetadata(s.Name, updatedAt, subInfo)
			}

			// 无论新下载还是沿用本地副本，都补齐 Fluxor 必需字段（端口 / 密钥 / DNS）。
			// 这一步是「改了端口立刻生效」的关键，必须每次执行——文件内容可能是很久
			// 以前下载的，里面的端口未必与当前设置一致。
			if err := patchSubscriptionFile(targetFile, *cfg); err != nil {
				errCh <- fmt.Errorf("订阅 %s 打补丁失败: %w", s.Name, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("部分操作失败: %v", errs)
	}
	return nil
}

// CleanupStaleDownloads 删除 proxies 目录下遗留的下载临时文件。
//
// 下载中途被杀（SIGKILL / 断电 / 面板升级）会在目录里留下 *.downloading.yaml。
// 它们不参与任何流程（生成按订阅名取正式文件名），但会占空间并让目录看起来有异常，
// 因此在启动时清理一次。
func CleanupStaleDownloads() {
	proxiesDir := filepath.Join(config.CoreWorkDir, "proxies")
	entries, err := os.ReadDir(proxiesDir)
	if err != nil {
		return // 目录不存在属正常（还没下过任何订阅）
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isDownloadingFileName(name) {
			continue
		}
		if err := os.Remove(filepath.Join(proxiesDir, name)); err != nil {
			logx.Warn(logx.ModuleSub, "failed to remove stale download temp file %s: %v", name, err)
			continue
		}
		removed++
	}
	if removed > 0 {
		logx.Info(logx.ModuleSub, "removed %d stale subscription download temp file(s) from %s", removed, proxiesDir)
	}
}

// isDownloadingFileName 判定文件名是否为下载临时文件（形如 foo.downloading.yaml）。
func isDownloadingFileName(name string) bool {
	return strings.Contains(name, downloadTempSuffix+".")
}
