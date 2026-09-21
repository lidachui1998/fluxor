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

// 绕过列表的预填内容（fluxor.json 字段缺失时写入，也供前端「恢复默认」按钮取用）。
//
// 只预填可核实的条目：公共 DNS 的 IPv4/IPv6 地址取自厂商官方页面
// （阿里 223.5.5.5 / 223.6.6.6 / 2400:3200::1 / 2400:3200:baba::1，
// 百度 180.76.76.76 / 2400:da00::6666）。微信 / 支付宝 / 抖音等服务的接入 IP 由 CDN
// 动态分配、没有官方公布的固定网段，因此**不预填具体 IP**，只在注释里给出自行查证
// 的方式——填错网段会把本该走代理的流量静默放行，比留空更危险。
//
// 注释（`#` 之后的内容）解析时会被剥离（stripComment），整行注释等价于空行，
// 因此下面的文字不会进入任何 nft 规则；但界面与文档共用这份内容，需保持可读。
//
// 分隔行刻意用 "#" 而不是空字符串：前端保存时会丢弃空行（见 Config.vue 的
// saveTproxyExceptions），只有这样「预填 → 保存 → 再打开」才能逐行一致。
func defaultDstExceptions() []string {
	return []string{
		"# 目的绕过：命中这些目标的流量不进代理，直接在系统侧出站",
		"#",
		"# 公共 DNS（IPv4）：让解析请求直连，避免 DNS 被劫持",
		"223.5.5.5 # 阿里公共 DNS",
		"223.6.6.6 # 阿里公共 DNS",
		"180.76.76.76 # 百度公共 DNS",
		"119.29.29.29 # 腾讯 DNSPod 公共 DNS",
		"1.12.12.12 # 腾讯 DNSPod 公共 DNS，DoH 入口",
		"#",
		"# 公共 DNS（IPv6）：需先开启「接管 IPv6 流量」，否则不会下发",
		"2400:3200::1 # 阿里公共 DNS",
		"2400:3200:baba::1 # 阿里公共 DNS",
		"2400:da00::6666 # 百度公共 DNS",
		"#",
		"# STUN 与内网穿透：穿透通道必须能直连，否则开启后可能连不上中转节点",
		"141.101.90.1 # 示例：请替换为你实际使用的 STUN 或穿透服务地址",
		"#",
		"# 国内常见服务：微信、支付宝、抖音等没有官方公布的固定网段，接入 IP 由 CDN 动态分配，",
		"# 故不预填。确需按服务绕过时先查当前 IP 再填，例如 dig +short short.weixin.qq.com。",
		"# 多数国内目标已由内核的 GEOIP 与 GEOSITE 规则判为直连，这里只用于完全不进内核的场景。",
		"#",
		"# 常用设备的 IPv6 网段：用 IPv6 全局地址访问 NAS、摄像头等设备时，其前缀不在内置保留",
		"# 网段内，需按设备前缀整段放行。前缀随重新拨号变化，按 /64 整段填比填单个地址稳定",
		"# （用 ip -6 addr 查看）。ULA 与链路本地已由内置规则绕过，无需重复填写。例如：",
		"# 240e:3b3:xxxx:xxxx::/64",
	}
}

// defaultSrcExceptions 源绕过的预填内容：命中这些来源的设备、容器完全不经代理。
func defaultSrcExceptions() []string {
	return []string{
		"# 源绕过：命中这些来源的设备、容器完全不经过代理，含 DNS",
		"# 仅支持 IP 与 CIDR，端口写法在这里无效",
		"#",
		"# Docker 默认 bridge 网段：填在这里意味着 Docker 容器的出站流量默认不代理，",
		"# 容器仍按宿主机原有网络直连。要让容器也走代理，删掉本行即可；",
		"# 自定义过 docker 网络的话，用 docker network inspect 查看实际网段后替换本行。",
		"172.17.0.0/16",
		"#",
		"# 常用设备的 IPv6 网段：设备的 IPv6 全局地址由运营商前缀加设备后缀组成，前缀会随",
		"# 重新拨号变化，按 /64 整段填比填单个地址稳定（用 ip -6 addr 查看）。例如：",
		"# 240e:3b3:xxxx:xxxx::/64",
		"# 注意：ULA 与链路本地已由内置保留网段绕过，无需重复填写；未开启「接管 IPv6 流量」",
		"# 时这些 IPv6 条目不会下发。",
	}
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

// LoadTproxyDstExceptions 加载目的绕过，字段不存在时写入默认值
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
				log.Printf("[TProxy] 迁移目的绕过失败: %v", err)
			}
			return dst
		}
	}

	// 文件缺失、损坏或字段不存在：落盘默认值，并同步缓存
	// （此前该分支只 return 默认值而不设置缓存，会让面板读到空列表）
	dst := defaultDstExceptions()
	tproxyDstExceptionsCache = dst
	if err := saveDstExceptions(dst); err != nil {
		log.Printf("[TProxy] 写入默认目的绕过失败: %v", err)
	}
	return dst
}

// saveDstExceptions 保存目的绕过并移除旧字段。
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

// LoadTproxySrcExceptions 加载源绕过，字段不存在时写入默认值
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
		log.Printf("[TProxy] 写入默认源绕过失败: %v", err)
	}
	return src
}

func saveSrcExceptions(src []string) error {
	return config.UpdateConfigFile(func(full map[string]any) {
		full[keyTproxySrcExceptions] = src
	})
}

// SaveTproxySrcExceptions 保存源绕过列表（加锁），由 HTTP 层调用。
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
