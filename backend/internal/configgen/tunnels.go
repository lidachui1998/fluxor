package configgen

import (
	"fluxor/internal/config"
	"fluxor/internal/configcheck"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// TunnelSkip 描述一条被跳过的隧道及原因。
type TunnelSkip struct {
	Tunnel config.Tunnel
	Reason string
}

// TunnelApplyResult 汇总一次隧道注入的结果。
type TunnelApplyResult struct {
	// Applied 真正写入配置的隧道条数。
	Applied int
	// Disabled 因开关关闭而未写入的条数（用户主动关掉，不是错误，无需提示原因）。
	Disabled int
	// Skipped 未写入的隧道（地址非法、proxy 已不存在、与其它隧道监听同一地址）。
	//
	// 与自定义规则同理：内核实测在加载配置时就校验 tunnels 的 proxy
	// （`tunnel proxy X not found`）并直接拒绝整份配置，因此「少一条隧道」远优于
	// 「配置完全无法加载、面板与内核一起不可用」。地址冲突则会让内核起监听时报错。
	Skipped []TunnelSkip
}

// ValidateTunnel 校验单条隧道是否可被内核接受。
//
// 校验项：监听类型只能是 tcp/udp、监听地址必须是 host:port、转发目标必须是 host:port 或
// 纯主机（域名/IP，端口取监听地址的端口）、proxy 非空时必须存在于该配置的代理组/代理节点/
// 内置目标中。地址与目标的归一化实现放在 config 包（写入配置与校验共用同一处判断）。
func ValidateTunnel(tunnel config.Tunnel, env configcheck.RuleEnv) error {
	if _, err := config.NormalizeTunnelNetworks(tunnel.Network); err != nil {
		return err
	}
	if _, err := config.NormalizeTunnelAddress(tunnel.Address); err != nil {
		return err
	}
	// 目标支持域名（`example.com:443`），但仍须带端口：内核解析不了裸主机
	if _, err := config.NormalizeTunnelTarget(tunnel.Target); err != nil {
		return err
	}
	proxy := strings.TrimSpace(tunnel.Proxy)
	if proxy == "" {
		// 留空表示不指定 proxy：内核按正常规则匹配选择出口，不需要校验目标
		return nil
	}
	if strings.ContainsAny(proxy, ",\r\n") {
		return fmt.Errorf("proxy 不能包含逗号或换行")
	}
	if _, ok := env.Targets[proxy]; !ok {
		return fmt.Errorf("proxy %q 不在当前可选目标里（代理组/节点可能已改名，请重新选择）", proxy)
	}
	return nil
}

// ApplyTunnels 把启用的隧道写入文档的顶层 tunnels 键（插在 rule-providers / rules 之前）。
//
// 无启用隧道时完全不触碰文档：切换模式的 config.yaml 是订阅文件的副本，注入的块在下次
// 复制时自然消失；而「机场自带 tunnels 块」这种少数情况也不该被我们抹掉（少删胜过误删）。
//
// 与 ApplyCustomRules 一样是幂等且「只增不覆盖」的：写完的键在下次生成时被整份替换，
// 所以重复调用产物逐字节一致。
func ApplyTunnels(doc *configcheck.Doc, tunnels []config.Tunnel) (TunnelApplyResult, error) {
	var result TunnelApplyResult
	if len(tunnels) == 0 {
		return result, nil
	}
	env := configcheck.RuleEnvFromDoc(doc)

	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	var accepted tunnelBindings
	for _, tunnel := range tunnels {
		if !tunnel.IsEnabled() {
			// 开关关闭的隧道只是暂时停用，不写进配置即可，不必报告原因
			result.Disabled++
			continue
		}
		if err := ValidateTunnel(tunnel, env); err != nil {
			result.Skipped = append(result.Skipped, TunnelSkip{Tunnel: tunnel, Reason: err.Error()})
			continue
		}
		if conflict, ok := accepted.conflictedWith(tunnel); ok {
			result.Skipped = append(result.Skipped, TunnelSkip{
				Tunnel: tunnel,
				Reason: "与另一条隧道（" + conflict.Address + "）监听同一地址与网络类型",
			})
			continue
		}
		item, err := buildTunnelNode(tunnel)
		if err != nil {
			result.Skipped = append(result.Skipped, TunnelSkip{Tunnel: tunnel, Reason: err.Error()})
			continue
		}
		seq.Content = append(seq.Content, item)
		result.Applied++
	}

	if result.Applied == 0 {
		return result, nil
	}
	// 位置：紧跟代理块之后、rule-providers / rules 之前。生成链路随后还会经
	// OrderTopLevel 按 topBlockRank 重排（结论一致），切换模式则只有这一步定位。
	doc.SetNodeBefore("tunnels", seq, "rule-providers", "rules")
	return result, nil
}

// buildTunnelNode 把一条隧道编码为 YAML 映射节点。
//
// 逐项用 yaml.Node 编码而非拼字符串：network 是序列、proxy 可能含 emoji 与空格，
// 插值会产出内核无法解析的内容。键顺序固定为 network / address / target / proxy，
// 便于产物逐字节比对。
func buildTunnelNode(tunnel config.Tunnel) (*yaml.Node, error) {
	networks, err := config.NormalizeTunnelNetworks(tunnel.Network)
	if err != nil {
		return nil, err
	}
	// 地址与目标都按归一化后的形态落盘（去空白 + 校验）：手改过 fluxor.json 的条目也会
	// 在这里被拦住，而不是产出一条起不来监听却无人报错的隧道
	address, err := config.NormalizeTunnelAddress(tunnel.Address)
	if err != nil {
		return nil, err
	}
	target, err := config.NormalizeTunnelTarget(tunnel.Target)
	if err != nil {
		return nil, err
	}
	item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	networkNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, network := range networks {
		networkNode.Content = append(networkNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: network})
	}
	appendMapNode(item, "network", networkNode)
	appendMap(item, "address", address)
	appendMap(item, "target", target)
	if proxy := strings.TrimSpace(tunnel.Proxy); proxy != "" {
		appendMap(item, "proxy", proxy)
	}
	return item, nil
}

// TunnelSkips 返回一组隧道里因配置无效而无法写入配置的说明。
//
// 与切换模式 writeRuntimeConfig 返回的 TunnelApplyResult.Skipped 对齐：让接口能如实
// 回报「隧道已保存，但有 N 条因配置无效未写入配置」。只统计**启用**的隧道——关闭的
// 隧道本来就不写进配置，报成「未写入」会误导用户。
func TunnelSkips(tunnels []config.Tunnel, ctx *RuleContext) []string {
	if ctx == nil {
		return nil
	}
	var skipped []string
	for i, reason := range TunnelReasons(tunnels, ctx.Env) {
		if reason == "" || !tunnels[i].IsEnabled() {
			continue
		}
		skipped = append(skipped, tunnels[i].Address+"（"+reason+"）")
	}
	return skipped
}

// TunnelReasons 逐条给出隧道不能写入配置的原因（可写入的项为空字符串），顺序与入参一致。
//
// 供两类调用方使用：写入配置的 ApplyTunnels（跳过无效项）与下发前端的视图
// （标明哪一条无效、为什么）。关闭的隧道同样参与校验——界面上要能看出它为什么无效
// （proxy 被改名等），但判重只统计启用的项：关闭的隧道不占端口，不构成冲突。
func TunnelReasons(tunnels []config.Tunnel, env configcheck.RuleEnv) []string {
	reasons := make([]string, len(tunnels))
	// 判重只看启用的隧道：关闭的项不绑定端口，与谁重复都不影响内核启动
	var accepted tunnelBindings
	for i, tunnel := range tunnels {
		if err := ValidateTunnel(tunnel, env); err != nil {
			reasons[i] = err.Error()
			continue
		}
		if !tunnel.IsEnabled() {
			continue
		}
		if conflict, ok := accepted.conflictedWith(tunnel); ok {
			reasons[i] = "与另一条隧道（" + conflict.Address + "）监听同一地址与网络类型"
		}
	}
	return reasons
}

// tunnelBindings 记录已接受写入配置的隧道，用于识别「同地址 + 网络有交集」的冲突项。
//
// 判重不能比字符串键：`[tcp, udp]` 与 `[tcp]` 是两个不同的键，却会抢同一个监听端口。
// 内核会为每个 network 各起一个监听器，冲突的那一条必然绑定失败
// （address already in use），因此必须按网络是否相交来判定。
type tunnelBindings []config.Tunnel

// conflictedWith 返回与之冲突的已接受隧道；无冲突时把它登记下来。
//
// 判重规则只写在这一处：写入侧（ApplyTunnels）与提示侧（TunnelReasons）若各写一份，
// 迟早会出现「提示说没问题、产物里其实少了一条」这类分歧。
func (b *tunnelBindings) conflictedWith(tunnel config.Tunnel) (config.Tunnel, bool) {
	for _, accepted := range *b {
		if config.TunnelBindingsOverlap(accepted, tunnel) {
			return accepted, true
		}
	}
	*b = append(*b, tunnel)
	return config.Tunnel{}, false
}
