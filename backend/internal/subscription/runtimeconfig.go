package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/configgen"
	"fluxor/internal/logx"
	"fmt"
	"os"
	"path/filepath"
)

// subscriptionFilePath 返回某个订阅在内核工作目录下的节点文件路径。
//
// 文件名统一经 config.SanitizeSubscriptionFileName 生成，防路径穿越。
func subscriptionFilePath(name string) string {
	return filepath.Join(config.CoreWorkDir, "proxies", config.SanitizeSubscriptionFileName(name))
}

// copyCustomRules 复制一份自定义规则切片。
//
// config.Current.Subscriptions 的底层数组由全局锁保护，调用方在锁外只能持有
// 副本，禁止直接引用该切片（否则与并发的增删规则构成数据竞争）。
func copyCustomRules(cfg config.SubscribeConfig, name string) []config.CustomRule {
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Name == name {
			rules := cfg.Subscriptions[i].CustomRules
			if len(rules) == 0 {
				return nil
			}
			out := make([]config.CustomRule, len(rules))
			copy(out, rules)
			return out
		}
	}
	return nil
}

// copyCustomTunnels 复制一份某个订阅的流量隧道切片（理由同 copyCustomRules）。
//
// config.CopyTunnels 会连 Network 切片一并深拷贝，因此副本可在锁外安全使用。
func copyCustomTunnels(cfg config.SubscribeConfig, name string) []config.Tunnel {
	for i := range cfg.Subscriptions {
		if cfg.Subscriptions[i].Name == name {
			return config.CopyTunnels(cfg.Subscriptions[i].Tunnels)
		}
	}
	return nil
}

// writeRuntimeConfig 把指定订阅的节点文件写为运行配置 config.yaml，并叠加其自定义规则
// 与流量隧道。
//
// 切换模式下 config.yaml 是「订阅文件副本」，因此这两类改写只能在复制之后叠加：
// 订阅文件（proxies/*.yaml）始终保持与机场下发内容一致，便于排查与整体重拉。
//
// rules / tunnels 由调用方在锁内取好副本后传入，本函数不做全局状态访问。
func writeRuntimeConfig(subName string, rules []config.CustomRule, tunnels []config.Tunnel) (configgen.ApplyResult, error) {
	srcFile := subscriptionFilePath(subName)
	if _, err := os.Stat(srcFile); err != nil {
		return configgen.ApplyResult{}, fmt.Errorf("订阅文件不存在: %w", err)
	}
	if err := copyFile(srcFile, config.ConfigTarget); err != nil {
		return configgen.ApplyResult{}, fmt.Errorf("复制配置文件失败: %w", err)
	}

	// 规则与隧道一趟改写完成：分两趟会在中间态留下「有隧道无规则」的 config.yaml，
	// 而内核热重载可能就落在那个中间态上
	result, tunnelResult, err := configgen.ApplyRuntimeOverridesToFile(config.ConfigTarget, rules, tunnels)
	if err != nil {
		return result, fmt.Errorf("写入自定义规则失败: %w", err)
	}
	for _, skip := range result.Skipped {
		logx.Warn(logx.ModuleRule, "custom rule skipped: subscription=%q rule=%s,%s,%s reason=%s",
			subName, skip.Rule.Type, skip.Rule.Payload, skip.Rule.Target, skip.Reason)
	}
	for _, skip := range tunnelResult.Skipped {
		logx.Warn(logx.ModuleTunnel, "tunnel skipped: subscription=%q tunnel=%s -> %s reason=%s",
			subName, skip.Tunnel.Address, skip.Tunnel.Target, skip.Reason)
	}
	return result, nil
}
