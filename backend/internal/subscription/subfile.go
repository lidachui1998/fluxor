package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/subscription/download"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 本文件承载「订阅节点文件怎么落到磁盘上」这一件事，供 ensure 与 update 两条链路共用。
//
// 两条链路都要下载订阅，而下载是本项目里最可能失败的一步（直连 15s + 临时内核 15s，
// 机场域名不可达、被墙、返回非 Clash 格式都会失败）。因此「下载失败时本地那份可用
// 副本必须原样保留」必须由这一层保证，而不是靠每个调用方记得别先删文件。

// downloadTempSuffix 下载中的临时文件后缀。
//
// 刻意保留 .yaml 扩展名（foo.downloading.yaml 而不是 foo.yaml.downloading）：
// 临时内核路径会把该路径交给 mihomo 作为 proxy-provider 的 path，任何按扩展名推断
// 格式的环节都不该被一个临时名影响。
const downloadTempSuffix = ".downloading"

// downloadToFile 下载订阅并**原子替换**目标文件。
//
// 流程：写入同目录的临时文件 → 成功后 rename 覆盖目标。据此：
//   - 下载失败（超时 / 非 200 / 不是 Clash 格式）时目标文件原封不动，上一次抓到的
//     节点仍然可用，config.yaml 的生成与内核重启都不受影响；
//   - rename 是原子操作，因此不存在「读到一个写了一半的 yaml」的窗口。
//
// 此前的实现是「先 os.Remove(targetFile) 再下载」，机场不可达时会把本地唯一一份
// 可用副本删掉——用户只是想改个端口，却把订阅文件弄丢了，只能等机场恢复。
func downloadToFile(sub config.Subscription, idx int, targetFile string) (string, map[string]any, error) {
	tmpFile := downloadingPath(targetFile)
	// 清理上一次中断留下的残留（同名文件会影响 mihomo 的覆盖写）
	if err := os.Remove(tmpFile); err != nil && !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("清理临时订阅文件失败: %w", err)
	}

	updatedAt, subInfo, err := download.DownloadSubscriptionFile(sub, idx, tmpFile)
	if err != nil {
		_ = os.Remove(tmpFile)
		return "", nil, err
	}

	// 空文件同样视为失败：有些失败路径（上游返回 200 + 空体）不会报错，
	// 但一个 0 字节的节点文件会让内核加载后没有任何节点。
	if info, statErr := os.Stat(tmpFile); statErr != nil || info.Size() == 0 {
		_ = os.Remove(tmpFile)
		if statErr != nil {
			return "", nil, fmt.Errorf("下载结果不可读: %w", statErr)
		}
		return "", nil, fmt.Errorf("下载结果为空文件")
	}

	if err := os.Rename(tmpFile, targetFile); err != nil {
		_ = os.Remove(tmpFile)
		return "", nil, fmt.Errorf("替换订阅文件失败: %w", err)
	}
	return updatedAt, subInfo, nil
}

// downloadingPath 返回目标文件对应的下载中临时文件路径（同目录，保证 rename 不跨设备）。
func downloadingPath(targetFile string) string {
	ext := filepath.Ext(targetFile)
	return strings.TrimSuffix(targetFile, ext) + downloadTempSuffix + ext
}
