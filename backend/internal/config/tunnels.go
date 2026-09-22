package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// 隧道支持的网络类型（对应内核 `tunnels[].network` 的取值）。
const (
	TunnelNetworkTCP = "tcp"
	TunnelNetworkUDP = "udp"
)

// Tunnel 描述一条流量隧道，对应 config.yaml 顶层 `tunnels` 块中的一项。
//
// 与 CustomRule 一样存结构化字段而非拼好的文本：写入 YAML 的值由 configgen 逐项编码，
// 用户输入里的逗号/冒号不会破坏配置结构。
//
// 归属作用域而非全局：切换模式挂在订阅上（subscriptions[].tunnels），融合模式按规则集
// 档位存放（merge_tunnels），自定义模式单独一份（custom_mode_tunnels）——与自定义规则
// 的作用域完全一致，因为可用的代理组/节点本来就随模式而变。
type Tunnel struct {
	// ID 由后端生成的稳定标识，供前端编辑/排序/删除/开关单条隧道使用。
	ID string `json:"id"`
	// Network 需要监听的网络类型，取值只能是 tcp / udp（内核逐个启动监听）。
	Network []string `json:"network"`
	// Address 本地监听地址（host:port），如 127.0.0.1:6553。
	Address string `json:"address"`
	// Target 转发的目标地址（host:port），如 8.8.8.8:53。
	Target string `json:"target"`
	// Proxy 可选项：经过某个代理组/代理节点发送流量（必须存在于配置中）。
	//
	// 留空表示不指定 proxy：流量按正常规则匹配选择出口（内核只在 SpecialProxy 非空时
	// 才强制走该代理），因此「关闭」不等于「直连」。
	Proxy string `json:"proxy,omitempty"`
	// Enabled 启停开关。nil（历史数据或请求未带该键）视为启用：隧道是用户主动添加的，
	// 默认就该生效，只有显式关掉才不写进配置。
	Enabled *bool `json:"enabled,omitempty"`
}

// IsEnabled 判定隧道是否启用（未显式设置时视为启用）。
func (t Tunnel) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// EnabledValue 返回显式布尔值，供接口下发（前端因此不必处理「缺键」这第三种状态）。
func (t Tunnel) EnabledValue() bool {
	return t.IsEnabled()
}

// NormalizeTunnelAddress 归一化本地监听地址：必须是 host:port 形式，且端口在合法区间内。
//
// 端口范围必须自己查：net.SplitHostPort 不检查 0 / 65536 这类取值，而内核要到真正起监听时
// 才失败（届时表现为内核启动报错），因此必须在写入前拦住。
func NormalizeTunnelAddress(address string) (string, error) {
	value := strings.TrimSpace(address)
	if value == "" {
		return "", fmt.Errorf("本地监听地址不能为空")
	}
	if strings.ContainsAny(value, ",\r\n") {
		return "", fmt.Errorf("本地监听地址不能包含逗号或换行")
	}
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("本地监听地址应为 host:port 形式（如 127.0.0.1:8888、0.0.0.0:8888）")
	}
	if err := validateTunnelPort(port); err != nil {
		return "", fmt.Errorf("本地监听地址的端口无效，%w", err)
	}
	return value, nil
}

// DefaultTunnelTargetPort 裸域名（只写域名、没写端口）时补的端口。
//
// 只对域名生效：域名在 http 语境下有约定俗成的默认端口，而裸 IP 没有——猜 IP 的端口等于
// 替用户把流量转到别处，因此裸 IP 一律拒绝并提示补端口。
const DefaultTunnelTargetPort = "80"

// NormalizeTunnelTarget 归一化转发目标，返回最终写进配置的 host:port。
//
// 三种写法：
//   - host:port（IP、域名、`[IPv6]:port`）→ 原样采用（仅裁剪首尾空白）；
//   - 纯域名（如 example.com）→ 补默认端口 `:80`（见 DefaultTunnelTargetPort）；
//   - 纯 IP（IPv4 `8.8.8.8` 或未加方括号的 IPv6 `::1`）→ 报错，要求用户补端口。
//
// 为什么不把裸主机原样下发：内核用 socks5.ParseAddr 解析目标，它要求 host:port；裸主机
// **不会**让配置加载失败，而是在起监听那一步打一行日志并跳过该条（实测
// `Start tunnel example.com error: invalid target address example.com`）——配置能加载、
// 内核照常运行，隧道却静默失效（界面与日志都看不出）。因此必须在写入前确定端口：
// 域名的默认端口是 80，IP 由用户写明。
func NormalizeTunnelTarget(target string) (string, error) {
	value := strings.TrimSpace(target)
	if value == "" {
		return "", fmt.Errorf("转发目标地址不能为空")
	}
	if strings.ContainsAny(value, ",\r\n") {
		return "", fmt.Errorf("转发目标地址不能包含逗号或换行")
	}
	if _, port, err := net.SplitHostPort(value); err == nil {
		if err := validateTunnelPort(port); err != nil {
			return "", fmt.Errorf("转发目标地址的端口无效，%w", err)
		}
		return value, nil
	}

	// 剩下的都不是 host:port：含冒号的是裸 IPv6（或写坏了的多段冒号），无冒号的看是不是 IP
	if strings.Contains(value, ":") || net.ParseIP(value) != nil {
		return "", fmt.Errorf("转发目标地址 %q 缺少端口，请写成 host:port（如 %s:53）；IPv6 需加方括号（如 [::1]:53）", value, value)
	}
	// 纯域名：按 http 默认端口补全，域名本身不由我们校验（内核会自己解析）
	return net.JoinHostPort(value, DefaultTunnelTargetPort), nil
}

// validateTunnelPort 校验端口取值区间。
func validateTunnelPort(port string) error {
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("需在 1-65535 之间")
	}
	return nil
}

// NormalizeTunnelNetworks 归一化网络类型：只接受 tcp / udp，去重后按 tcp、udp 的固定
// 顺序返回，保证同一组取值写出稳定的字节序。
func NormalizeTunnelNetworks(networks []string) ([]string, error) {
	seen := make(map[string]struct{}, len(networks))
	for _, network := range networks {
		value := strings.ToLower(strings.TrimSpace(network))
		switch value {
		case TunnelNetworkTCP, TunnelNetworkUDP:
			seen[value] = struct{}{}
		case "":
			continue
		default:
			return nil, fmt.Errorf("监听类型只能是 tcp 或 udp，收到 %q", network)
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("监听类型不能为空")
	}

	out := make([]string, 0, len(seen))
	// 固定顺序（tcp 在前）：内核不关心顺序，但产物要能逐字节比对
	if _, ok := seen[TunnelNetworkTCP]; ok {
		out = append(out, TunnelNetworkTCP)
	}
	if _, ok := seen[TunnelNetworkUDP]; ok {
		out = append(out, TunnelNetworkUDP)
	}
	return out, nil
}

// TunnelBindingsOverlap 判定两条隧道是否监听同一个地址与重叠的网络类型。
//
// 内核会为每个 network 各起一个监听器，地址与网络类型都相同的两条隧道必然有一方
// 绑定失败（`address already in use`），因此必须在保存前拦住。
func TunnelBindingsOverlap(a, b Tunnel) bool {
	if strings.TrimSpace(a.Address) != strings.TrimSpace(b.Address) {
		return false
	}
	for _, network := range a.Network {
		for _, other := range b.Network {
			if strings.EqualFold(strings.TrimSpace(network), strings.TrimSpace(other)) {
				return true
			}
		}
	}
	return false
}

// MoveTunnel 在隧道列表内上移/下移一条隧道。
//
// 返回新切片与是否发生了移动。隧道没有插入位置的分组概念（它们同属 config.yaml 的
// 一个 tunnels 块），因此整份列表就是一组，边界处返回 moved=false 由调用方提示。
func MoveTunnel(tunnels []Tunnel, id, direction string) ([]Tunnel, bool) {
	idx := -1
	for i := range tunnels {
		if tunnels[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return tunnels, false
	}

	neighbor := -1
	switch direction {
	case RuleMoveUp:
		if idx > 0 {
			neighbor = idx - 1
		}
	case RuleMoveDown:
		if idx < len(tunnels)-1 {
			neighbor = idx + 1
		}
	default:
		return tunnels, false
	}
	if neighbor < 0 {
		return tunnels, false
	}

	// 复制后交换：调用方持有的旧切片（如请求级快照）不应被就地改动
	out := make([]Tunnel, len(tunnels))
	copy(out, tunnels)
	out[idx], out[neighbor] = out[neighbor], out[idx]
	return out, true
}

// MergeTunnelsFor 返回某个规则集档位的融合模式隧道（无则返回 nil）。
func (c SubscribeConfig) MergeTunnelsFor(ruleGroup string) []Tunnel {
	if c.MergeTunnels == nil {
		return nil
	}
	return c.MergeTunnels[ruleGroup]
}

// TemplateTunnelsFor 返回某个模板级作用域的隧道（融合档位读 MergeTunnels，
// 自定义模式读 CustomModeTunnels），无则返回 nil。
func (c SubscribeConfig) TemplateTunnelsFor(scope string) []Tunnel {
	if scope == RuleScopeCustom {
		return c.CustomModeTunnels
	}
	return c.MergeTunnelsFor(scope)
}

// SubscriptionTunnelsFor 返回某个订阅的隧道（切换模式），无则返回 nil。
func (c SubscribeConfig) SubscriptionTunnelsFor(name string) []Tunnel {
	for i := range c.Subscriptions {
		if c.Subscriptions[i].Name == name {
			return c.Subscriptions[i].Tunnels
		}
	}
	return nil
}

// CopyTunnels 深拷贝隧道切片：nil 保持 nil（避免把「没有隧道」写成显式空切片），
// 其余情况返回独立底层数组，防止与全局状态共享后被就地改写。
func CopyTunnels(tunnels []Tunnel) []Tunnel {
	if tunnels == nil {
		return nil
	}
	out := make([]Tunnel, len(tunnels))
	copy(out, tunnels)
	// Network 是切片字段：逐项复制，否则副本仍与全局共享底层数组
	for i := range out {
		if out[i].Network == nil {
			continue
		}
		out[i].Network = append([]string(nil), out[i].Network...)
	}
	return out
}
