package subscription

import (
	"fluxor/internal/config"
	"fluxor/internal/configgen"
	"fmt"
	"log"
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

// writeRuntimeConfig 把指定订阅的节点文件写为运行配置 config.yaml，并叠加其自定义规则。
//
// 切换模式下 config.yaml 是「订阅文件副本」，因此自定义规则只能在复制之后叠加：
// 订阅文件（proxies/*.yaml）始终保持与机场下发内容一致，便于排查与整体重拉。
//
// rules 由调用方在锁内取好副本后传入，本函数不做全局状态访问。
func writeRuntimeConfig(subName string, rules []config.CustomRule) (configgen.ApplyResult, error) {
	srcFile := subscriptionFilePath(subName)
	if _, err := os.Stat(srcFile); err != nil {
		return configgen.ApplyResult{}, fmt.Errorf("订阅文件不存在: %w", err)
	}
	if err := copyFile(srcFile, config.ConfigTarget); err != nil {
		return configgen.ApplyResult{}, fmt.Errorf("复制配置文件失败: %w", err)
	}

	result, err := configgen.ApplyCustomRulesToFile(config.ConfigTarget, rules)
	if err != nil {
		return result, fmt.Errorf("写入自定义规则失败: %w", err)
	}
	for _, skip := range result.Skipped {
		log.Printf("[CUSTOM-RULE] 跳过订阅 %s 的规则 %s,%s,%s: %s",
			subName, skip.Rule.Type, skip.Rule.Payload, skip.Rule.Target, skip.Reason)
	}
	return result, nil
}
