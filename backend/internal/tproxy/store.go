package tproxy

import (
	"encoding/json"
	"fluxor/internal/config"
	"log"
)

// fluxor.json 中由本包负责的字段。
//
// 这些字段与订阅配置共用同一个文件，因此写入一律通过 config.UpdateConfigFile
// 走「读—改—写」，与 config.SaveSubscribeConfig 共用同一把文件锁（config.FileMu）。
// 切勿在此直接 os.WriteFile 整个文件：那会把订阅配置字段抹掉。
const (
	keyTproxyEnabled       = "tproxy_enabled"
	keyTproxyProxyLocal    = "tproxy_proxy_local"
	keyTproxyIPv6          = "tproxy_ipv6"
	keyTproxyDstExceptions = "tproxy_dst_exceptions"
	keyTproxySrcExceptions = "tproxy_src_exceptions"
	keyTproxyExceptionsOld = "tproxy_exceptions" // 旧字段，读取时迁移
)

// 默认例外列表（文件缺失或字段不存在时使用）
func defaultDstExceptions() []string {
	return []string{"# 公共 DNS 服务器", "223.5.5.5 #注释可单独一行也可写在规则后", "1.12.12.12", "# stun服务器", "141.101.90.1"}
}

func defaultSrcExceptions() []string {
	return []string{"# Docker 默认网段", "172.17.0.0/16"}
}

// readStringSliceField 从配置文件的顶层映射中读取一个字符串数组字段。
func readStringSliceField(full map[string]any, key string) ([]string, bool) {
	raw, ok := full[key]
	if !ok {
		return nil, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

// LoadTproxyDstExceptions 加载目的例外，字段不存在时写入默认值
func LoadTproxyDstExceptions() []string {
	exceptionsMu.Lock()
	defer exceptionsMu.Unlock()

	full, err := config.ReadConfigFile()
	if err == nil {
		// 新字段优先
		if dst, ok := readStringSliceField(full, keyTproxyDstExceptions); ok {
			tproxyDstExceptionsCache = dst
			return dst
		}
		// 回退到旧字段并迁移
		if dst, ok := readStringSliceField(full, keyTproxyExceptionsOld); ok {
			tproxyDstExceptionsCache = dst
			if err := saveDstExceptions(dst); err != nil {
				log.Printf("[TProxy] 迁移目的例外失败: %v", err)
			}
			return dst
		}
	}

	// 文件缺失、损坏或字段不存在：落盘默认值，并同步缓存
	// （此前该分支只 return 默认值而不设置缓存，会让面板读到空列表）
	dst := defaultDstExceptions()
	tproxyDstExceptionsCache = dst
	if err := saveDstExceptions(dst); err != nil {
		log.Printf("[TProxy] 写入默认目的例外失败: %v", err)
	}
	return dst
}

// saveDstExceptions 保存目的例外并移除旧字段。
func saveDstExceptions(dst []string) error {
	return config.UpdateConfigFile(func(full map[string]any) {
		full[keyTproxyDstExceptions] = dst
		delete(full, keyTproxyExceptionsOld)
	})
}

// SaveTproxyDstExceptions 供外部调用（加锁）
func SaveTproxyDstExceptions(dst []string) error {
	exceptionsMu.Lock()
	defer exceptionsMu.Unlock()
	tproxyDstExceptionsCache = dst
	return saveDstExceptions(dst)
}

// LoadTproxySrcExceptions 加载源例外，字段不存在时写入默认值
func LoadTproxySrcExceptions() []string {
	exceptionsMu.Lock()
	defer exceptionsMu.Unlock()

	full, err := config.ReadConfigFile()
	if err == nil {
		if src, ok := readStringSliceField(full, keyTproxySrcExceptions); ok {
			tproxySrcExceptionsCache = src
			return src
		}
	}

	src := defaultSrcExceptions()
	tproxySrcExceptionsCache = src
	if err := saveSrcExceptions(src); err != nil {
		log.Printf("[TProxy] 写入默认源例外失败: %v", err)
	}
	return src
}

func saveSrcExceptions(src []string) error {
	return config.UpdateConfigFile(func(full map[string]any) {
		full[keyTproxySrcExceptions] = src
	})
}

// SaveTproxySrcExceptions 保存源例外列表（加锁），由 HTTP 层调用。
func SaveTproxySrcExceptions(src []string) error {
	exceptionsMu.Lock()
	defer exceptionsMu.Unlock()
	tproxySrcExceptionsCache = src
	return saveSrcExceptions(src)
}

// LoadTproxyProxyLocal 读取 tproxy_proxy_local 字段，默认 true
func LoadTproxyProxyLocal() bool {
	exceptionsMu.Lock()
	defer exceptionsMu.Unlock()

	full, err := config.ReadConfigFile()
	if err == nil {
		if raw, ok := full[keyTproxyProxyLocal]; ok {
			if enabled, ok := raw.(bool); ok {
				tproxyProxyLocal = enabled
				return enabled
			}
		}
	}

	// 缺失或类型异常：默认开启并落盘
	tproxyProxyLocal = true
	if err := saveProxyLocal(true); err != nil {
		log.Printf("[TProxy] 写入默认本机代理开关失败: %v", err)
	}
	return true
}

func saveProxyLocal(enabled bool) error {
	return config.UpdateConfigFile(func(full map[string]any) {
		full[keyTproxyProxyLocal] = enabled
	})
}

// SaveTproxyProxyLocal 外部调用，加锁并保存
func SaveTproxyProxyLocal(enabled bool) error {
	exceptionsMu.Lock()
	defer exceptionsMu.Unlock()
	tproxyProxyLocal = enabled
	return saveProxyLocal(enabled)
}

// LoadTproxyIPv6 读取接管 IPv6 开关，默认关闭。
//
// 默认关闭是刻意的：节点侧普遍没有 IPv6 出口，而 IPv6 一旦被接管，原本直连
// 可达的 IPv6 目标会改为经代理出站并可能失败；同时「被劫持 DNS 返回空 AAAA」
// 已让绝大多数域名不会走 IPv6，收益只在「客户端自带解析（DoH/DoT/ISP v6 DNS）」
// 或写入字面量 IPv6 的场景出现。因此交由用户显式开启。
func LoadTproxyIPv6() bool {
	exceptionsMu.Lock()
	defer exceptionsMu.Unlock()

	full, err := config.ReadConfigFile()
	if err == nil {
		if raw, ok := full[keyTproxyIPv6]; ok {
			if enabled, ok := raw.(bool); ok {
				tproxyIPv6 = enabled
				return enabled
			}
		}
	}

	tproxyIPv6 = false
	if err := saveTproxyIPv6(false); err != nil {
		log.Printf("[TProxy] 写入默认 IPv6 接管开关失败: %v", err)
	}
	return false
}

func saveTproxyIPv6(enabled bool) error {
	return config.UpdateConfigFile(func(full map[string]any) {
		full[keyTproxyIPv6] = enabled
	})
}

// SaveTproxyIPv6 外部调用，加锁并保存
func SaveTproxyIPv6(enabled bool) error {
	exceptionsMu.Lock()
	defer exceptionsMu.Unlock()
	tproxyIPv6 = enabled
	return saveTproxyIPv6(enabled)
}

// LoadTproxyEnabled 读取持久化的 TProxy 开关状态。
//
// 仅供诊断/日志使用：实际生效状态由 ResetOnStartup 在冷启动时统一归零，
// 因此本函数不会用于恢复运行状态。
func LoadTproxyEnabled() bool {
	full, err := config.ReadConfigFile()
	if err != nil {
		return false
	}
	raw, ok := full[keyTproxyEnabled]
	if !ok {
		return false
	}
	enabled, _ := raw.(bool)
	return enabled
}

// persistTproxyEnabled 持久化开关状态。
func persistTproxyEnabled(enabled bool) error {
	return config.UpdateConfigFile(func(full map[string]any) {
		full[keyTproxyEnabled] = enabled
	})
}

// SetTproxyEnabled 设置内存中的开关状态并持久化，供 HTTP 层复用。
func SetTproxyEnabled(enabled bool) {
	tproxyMu.Lock()
	tproxyEnableState = enabled
	tproxyMu.Unlock()

	if err := persistTproxyEnabled(enabled); err != nil {
		log.Printf("[TProxy] 持久化开关状态失败: %v", err)
	}
}

// ResetOnStartup 冷启动时的状态收敛。
//
// nftables 规则不跨重启存活，而开关状态是持久化的：若上一次是非优雅退出
// （kill -9 / 崩溃），nft 规则可能仍然存在，此时磁盘上的开关却可能为「已关闭」，
// 出现「面板显示关闭、流量实际仍被劫持」的静默错配。
//
// 因此在冷启动时无条件把状态归零，并清除任何残留规则，让内存态、磁盘态与
// 内核态三者重新一致。用户需要 TProxy 时再手动开启。
func ResetOnStartup() {
	// 先清残留规则（不依赖当前布尔值，确保任何残留都被移除）
	DisableTProxyRules()

	SetTproxyEnabled(false)

	if LoadTproxyEnabled() {
		log.Printf("[TProxy] 上次为启用状态，已在冷启动时重置为关闭并清理残留规则")
	}
}
