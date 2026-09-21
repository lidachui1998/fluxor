package nodespec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"fluxor/internal/config"
)

// Kind 字段取值类型，决定前端控件形态与归一化方式。
type Kind string

const (
	// KindString 单行文本。
	KindString Kind = "string"
	// KindText 多行文本（PEM 证书、静态密钥等）。
	KindText Kind = "text"
	// KindInt 整数。
	KindInt Kind = "int"
	// KindBool 布尔开关。
	KindBool Kind = "bool"
	// KindSelect 下拉选择（Options 为可选值；空值表示「不指定，用内核默认」）。
	KindSelect Kind = "select"
	// KindList 列表（界面按逗号/换行分隔输入，写入 YAML 为字符串序列）。
	KindList Kind = "list"
	// KindMap 键值对（界面按「键: 值」每行一条输入，写入 YAML 为映射）。
	KindMap Kind = "map"
	// KindGroup 嵌套选项块（ws-opts / reality-opts / obfs-opts 等），
	// 子字段由 Children 声明，写入 YAML 为嵌套映射。
	KindGroup Kind = "group"
)

// 界面分区：字段落在哪个折叠区。声明顺序即界面顺序（基础 → 传输层 → TLS → 高级）。
const (
	// SectionBasic 基础项：默认展开，协议连通所必需的字段。
	SectionBasic = ""
	// SectionTransport 传输层配置（network 与 ws-opts / grpc-opts 等）。
	SectionTransport = "transport"
	// SectionTLS TLS 配置（sni / alpn / 证书 / reality-opts / ech-opts 等）。
	SectionTLS = "tls"
	// SectionAdvanced 高级项（通用字段中的 tfo / mptcp / interface-name 等）。
	SectionAdvanced = "advanced"
)

// VisibleWhen 条件显示：只有同层某个字段的取值命中 Values 时才渲染（并提交）。
//
// 典型用途是传输层选项块——ws-opts 只在 network=ws 时有意义，五个选项块
// 同时铺开会淹没表单。比较按字符串进行（bool 取 "true"/"false"）。
type VisibleWhen struct {
	// Key 同层被依赖的字段键，如 network。
	Key string `json:"key"`
	// Values 命中的取值集合。
	Values []string `json:"values"`
}

// Field 描述协议的一个可配置字段。
//
// Default 为空值（""/0/false/空列表/空映射）时表示「不下发，交由内核使用自身默认值」；
// 归一化会剔除与 Default 相同的取值，因此持久化只留差异。
type Field struct {
	// Key 内核配置键（如 server、skip-cert-verify、ws-opts）。
	Key string `json:"key"`
	// Label 兜底展示名（英文）。前端对已知键使用 i18n 文案，未知键回落到此。
	Label string `json:"label"`
	// Kind 取值类型。
	Kind Kind `json:"kind"`
	// Default 默认值：界面预填、归一化时作为「未改动的基准」。
	Default any `json:"default,omitempty"`
	// Options KindSelect 的可选值。
	Options []string `json:"options,omitempty"`
	// Required 必填（归一化时校验；界面标注 *）。
	Required bool `json:"required,omitempty"`
	// Secret 敏感值（界面默认掩码显示）。
	Secret bool `json:"secret,omitempty"`
	// Section 所属折叠区（见 Section* 常量）。
	Section string `json:"section,omitempty"`
	// Children KindGroup 的子字段（子字段自身不再分区：区块内部整块渲染）。
	Children []Field `json:"children,omitempty"`
	// VisibleWhen 条件显示（同层字段满足条件时才渲染）。
	VisibleWhen *VisibleWhen `json:"visible_when,omitempty"`
	// Always 始终下发：内核的解码器要求该键必须存在（struct 上没有 omitempty），
	// 即使取值是零值也要写进 config.yaml（如 vmess 的 alterId）。
	Always bool `json:"always,omitempty"`
}

// Protocol 描述一个出站代理协议。
type Protocol struct {
	// Type 内核 type 取值（如 ss、hysteria2）。
	Type string `json:"type"`
	// Name 展示名（专有名词，不做翻译）。
	Name string `json:"name"`
	// Fields 协议字段（不含 name / type，二者由界面单独处理）。
	Fields []Field `json:"fields"`
	// RequireAny 备用填写组合：外层是「或」、内层是「与」，即至少满足其中一组
	// （组内字段需同时填写）。用于表达内核的真实约束，例如
	//   - hysteria：{{port}, {ports}}——端口或端口段二选一；
	//   - wireguard / masque：{{ip}, {ipv6}}——本机地址 IPv4 或 IPv6 至少一个；
	//   - openvpn：{{cert, key}, {username}}——证书认证或用户名认证二选一。
	RequireAny [][]string `json:"require_any,omitempty"`
	// Tunnel 隧道类协议（WireGuard / MASQUE / TrustTunnel / Tailscale / ZeroTier /
	// EasyTier / OpenVPN）：界面在协议选择框下给出「建议搭配自定义规则」的灰色小字提示。
	//
	// 与 Deprecated 一样只是**界面提示**，不参与归一化与生成。之所以要提示：隧道类节点
	// 接入的是对端虚拟网络（能到的地址是对端网段），而生成配置的模板规则第一条
	// `GEOIP,lan,→ 当前代理组...` 实为直连私网，因此「只想访问内网」时必须自己写
	// IP-CIDR 规则把这些网段引流到该节点，否则内网地址会被直连规则抢走。
	Tunnel bool `json:"tunnel,omitempty"`
	// Deprecated 已过时的协议：界面在协议选择框下给出提示，**不阻止保存**。
	//
	// 只作为提示信息，不参与归一化与生成——用户的既有节点、机场仍在用的协议
	// 都不该因为一句"过时"就存不进去（见 TestDeprecatedProtocolStillAccepted）。
	Deprecated bool `json:"deprecated,omitempty"`
}

// KV 字段名与取值的有序对，用于按声明顺序写 YAML。
type KV struct {
	Key   string
	Value any
	// Kind 取值类型：KindGroup 时 Children 有效，KindMap 时 Value 为 map[string]string。
	Kind Kind
	// Children KindGroup 的子字段（有序）。
	Children []KV
	// Default 该字段声明的默认值（仅用于判断「块内子项是否等于默认值」，见 pruneEmpty）。
	Default any
	// Always 见 Field.Always：内核要求必须存在的键不能因为取值为零而被略过。
	Always bool
}

// 常用取值集合（多处复用，避免各协议各写一份而漂移）。
var (
	ipVersionOptions = []string{"dual", "ipv4", "ipv6", "ipv4-prefer", "ipv6-prefer"}

	// ssCiphers Shadowsocks（含 SSR）支持的加密方法（对照 wiki Cipher 小节）。
	ssCiphers = []string{
		"aes-128-ctr", "aes-192-ctr", "aes-256-ctr",
		"aes-128-cfb", "aes-192-cfb", "aes-256-cfb",
		"aes-128-gcm", "aes-192-gcm", "aes-256-gcm",
		"aes-128-ccm", "aes-192-ccm", "aes-256-ccm",
		"aes-128-gcm-siv", "aes-256-gcm-siv",
		"chacha20-ietf", "chacha20", "xchacha20",
		"chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
		"chacha8-ietf-poly1305", "xchacha8-ietf-poly1305",
		"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
		"lea-128-gcm", "lea-192-gcm", "lea-256-gcm",
		"rabbit128-poly1305", "aegis-128l", "aegis-256", "aez-384", "deoxys-ii-256-128",
		"rc4-md5", "none",
	}

	// ssrObfs SSR 混淆（transport/ssr/obfs 注册项）。
	ssrObfs = []string{"plain", "http_simple", "http_post", "random_head", "tls1.2_ticket_auth", "tls1.2_ticket_fastauth"}
	// ssrProtocols SSR 协议插件（transport/ssr/protocol 注册项）。
	ssrProtocols = []string{"origin", "auth_sha1_v4", "auth_aes128_md5", "auth_aes128_sha1", "auth_chain_a", "auth_chain_b"}

	congestionControllers = []string{"cubic", "new_reno", "bbr"}
	bbrProfiles           = []string{"standard", "conservative", "aggressive"}

	// ssPlugins Shadowsocks 插件（wiki ss 页「插件」小节）。
	ssPlugins = []string{"obfs", "v2ray-plugin", "gost-plugin", "shadow-tls", "restls", "kcptun", "jls"}

	// snellObfsModes Snell 混淆模式（内核 transport 包的 Mode 常量）。
	snellObfsModes = []string{"tls", "http", "shadow-tls", "restls", "jls"}

	// tlsFingerprints TLS 指纹（可自由填写，这里给出常用值）。
	tlsFingerprints = []string{"chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random"}
)

// basicAdvanced 通用字段中的高级项（BasicOption 除 dialer-proxy 外的全部内容）。
//
// dialer-proxy 按要求不下发；smux 与 ip-stack 是另外的嵌套块，同样不在本表内。
var basicAdvanced = []Field{
	{Key: "tfo", Label: "TCP Fast Open", Kind: KindBool},
	{Key: "mptcp", Label: "TCP Multi Path", Kind: KindBool},
	{Key: "interface-name", Label: "Interface", Kind: KindString},
	{Key: "routing-mark", Label: "Routing Mark", Kind: KindInt},
	{Key: "ip-version", Label: "IP Version", Kind: KindSelect, Default: "dual", Options: ipVersionOptions},
}

// protocols 全部协议，顺序与官方 wiki 侧栏（HTTP → OpenVPN）一致，
// 前端下拉按此顺序展示：常用协议（HTTP / SOCKS / SS / VMess / VLESS / Trojan）
// 靠前，后段为特定场景协议。
var protocols = []Protocol{
	httpProtocol, socks5Protocol, ssProtocol, ssrProtocol, snellProtocol,
	vmessProtocol, vlessProtocol, trojanProtocol, anytlsProtocol, sshProtocol,
	mieruProtocol, sudokuProtocol, hysteriaProtocol, hysteria2Protocol, tuicProtocol, shadowquicProtocol,
	wireguardProtocol, masqueProtocol, trusttunnelProtocol,
	tailscaleProtocol, zerotierProtocol, easytierProtocol, openvpnProtocol,
}

// Protocols 返回全部协议（含字段表与默认值）。
//
// 返回的是内部切片的浅拷贝：调用方只读，不得改写（Fields 与 Options 亦为共享）。
func Protocols() []Protocol {
	out := make([]Protocol, len(protocols))
	copy(out, protocols)
	return out
}

// Lookup 按内核 type 查找协议。
func Lookup(protoType string) (Protocol, bool) {
	key := strings.TrimSpace(protoType)
	for _, p := range protocols {
		if p.Type == key {
			return p, true
		}
	}
	return Protocol{}, false
}

// Materialize 补齐协议默认值，返回完整字段表（接口下发与界面展示使用）。
//
// 嵌套块以嵌套映射形式返回；未知键（旧数据里已被移除的字段）被忽略，不阻断读取。
func Materialize(node config.CustomNode) (map[string]any, error) {
	kvs, err := materializeOrdered(node)
	if err != nil {
		return nil, err
	}
	return kvToMap(kvs), nil
}

// MaterializeOrdered 同 Materialize，但按字段声明顺序返回有序对（供写 YAML 用）。
func MaterializeOrdered(node config.CustomNode) ([]KV, error) {
	return materializeOrdered(node)
}

// WritableOrdered 返回「可直接写进 config.yaml」的有序字段表：
// 在补齐默认值的基础上剔除空值（含全空的嵌套块），只留下内核真正需要的键。
func WritableOrdered(node config.CustomNode) ([]KV, error) {
	kvs, err := materializeOrdered(node)
	if err != nil {
		return nil, err
	}
	return pruneEmpty(kvs), nil
}

// MaterializeAll 返回一份「补齐默认值」的节点副本，供接口下发（不改动入参）。
//
// 磁盘与内存只保留用户改过的字段，界面却需要看到完整取值才能正确预填表单，
// 因此在读取路径上补齐。协议未知（历史数据里已被移除的协议）时保留原字段表，
// 不因为一个坏节点让整个配置接口失败。
func MaterializeAll(nodes []config.CustomNode) []config.CustomNode {
	if len(nodes) == 0 {
		return nodes
	}
	out := make([]config.CustomNode, 0, len(nodes))
	for _, node := range nodes {
		item := config.CustomNode{ID: node.ID, Name: node.Name, Type: node.Type}
		full, err := Materialize(node)
		if err != nil {
			item.Config = node.Config
		} else {
			item.Config = full
		}
		out = append(out, item)
	}
	return out
}

// materializeOrdered 是 Materialize / MaterializeOrdered / WritableOrdered 的公共实现。
func materializeOrdered(node config.CustomNode) ([]KV, error) {
	proto, ok := Lookup(node.Type)
	if !ok {
		return nil, fmt.Errorf("不支持的协议类型: %s", node.Type)
	}
	return materializeFields(proto.Fields, node.Config), nil
}

// materializeFields 补齐一层字段的默认值（嵌套块递归）。
//
// 取值无法转换时回落默认值而不是报错：补齐只服务于「展示与写盘」，
// 真正的取值校验在 Normalize 里做，两者不应互相阻塞。
func materializeFields(fields []Field, raw map[string]any) []KV {
	out := make([]KV, 0, len(fields))
	for _, f := range fields {
		value, present := raw[f.Key]
		if !present || IsEmpty(value) {
			out = append(out, materializeDefault(f))
			continue
		}
		coerced, err := coerce(f, value, f.Key)
		if err != nil {
			out = append(out, materializeDefault(f))
			continue
		}
		if f.Kind == KindGroup {
			// 嵌套块以「子字段有序对」承载（写 YAML 要按声明顺序落键），
			// 因此这里展开子字段，而不是把 map 塞进 Value
			nested, _ := coerced.(map[string]any)
			out = append(out, KV{
				Key:      f.Key,
				Kind:     KindGroup,
				Children: materializeFields(f.Children, nested),
				Always:   f.Always,
			})
			continue
		}
		out = append(out, KV{Key: f.Key, Value: coerced, Kind: f.Kind, Default: defaultOf(f), Always: f.Always})
	}
	return out
}

// materializeDefault 生成字段的默认值 KV（嵌套块递归展开子字段）。
func materializeDefault(f Field) KV {
	if f.Kind == KindGroup {
		children := make([]KV, 0, len(f.Children))
		for _, child := range f.Children {
			children = append(children, materializeDefault(child))
		}
		return KV{Key: f.Key, Kind: KindGroup, Children: children, Always: f.Always}
	}
	return KV{Key: f.Key, Value: defaultOf(f), Kind: f.Kind, Default: defaultOf(f), Always: f.Always}
}

// pruneEmpty 剔除不该写进产物的字段：空值，以及「块内仍等于默认值」的子项。
//
// 两套判据分别对应两种语义：
//   - 顶层字段只按「是否为空」剔除，非空的默认值照写（如 ss 的 cipher、openvpn 的
//     proto）。顶层没有「块是否存在」这层语义，而必填项即使取默认值也必须写出来，
//     因此不做默认值比较——宁可多写几个与内核默认等价的键。
//   - 嵌套块内的子项额外按「是否等于默认值」剔除：块的**存在**本身就是内核眼中的
//     「启用该传输」（vmess 的 ws-opts / h2-opts / grpc-opts …），把未使用的块按
//     默认值materialize 出来会凭空空写一堆用不到的选项块。
//
// 内核要求存在的键（Always）在任何情况下都保留。
func pruneEmpty(kvs []KV) []KV {
	out := make([]KV, 0, len(kvs))
	for _, kv := range kvs {
		if kv.Kind == KindGroup {
			children := pruneEmptyBlockChildren(kv.Children)
			if len(children) == 0 && !kv.Always {
				continue
			}
			out = append(out, KV{Key: kv.Key, Kind: KindGroup, Children: children, Always: kv.Always})
			continue
		}
		if isEmptyKV(kv) && !kv.Always {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// pruneEmptyBlockChildren 清洗嵌套块的子项：空值与默认值都不下发，子块递归处理。
func pruneEmptyBlockChildren(kvs []KV) []KV {
	out := make([]KV, 0, len(kvs))
	for _, kv := range kvs {
		if kv.Kind == KindGroup {
			children := pruneEmptyBlockChildren(kv.Children)
			if len(children) == 0 && !kv.Always {
				continue
			}
			out = append(out, KV{Key: kv.Key, Kind: KindGroup, Children: children, Always: kv.Always})
			continue
		}
		if kv.Always {
			out = append(out, kv)
			continue
		}
		if isEmptyKV(kv) || reflect.DeepEqual(kv.Value, kv.Default) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// IsEmptyKV 判定一个 KV 是否为空（嵌套块为空指子项全空）。
func IsEmptyKV(kv KV) bool {
	if kv.Kind == KindGroup {
		return len(pruneEmpty(kv.Children)) == 0
	}
	return isEmptyKV(kv)
}

// isEmptyKV 判定标量 / 映射取值是否为空。
func isEmptyKV(kv KV) bool {
	if kv.Kind == KindMap {
		m, ok := kv.Value.(map[string]string)
		return !ok || len(m) == 0
	}
	return IsEmpty(kv.Value)
}

// kvToMap 把有序 KV 还原成 map（嵌套块递归）。
func kvToMap(kvs []KV) map[string]any {
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		if kv.Kind == KindGroup {
			out[kv.Key] = kvToMap(kv.Children)
			continue
		}
		out[kv.Key] = kv.Value
	}
	return out
}

// Normalize 校验并归一化一个节点。
//
// 归一化包含四件事：
//  1. 协议类型必须已知，节点名必须合法（非空/长度/无控制字符）；
//  2. 字段取值按声明类型转换（嵌套块递归），非法取值、未知字段、不在 Options
//     内的选值一律报错（不静默丢弃：静默丢弃会让用户以为写进配置了）；
//  3. 必填与 RequireAny 校验：必填项必须填；嵌套块「要么整块不出现、要么填齐必填子项」；
//  4. 落库时剔除与默认值相同的字段——持久化只保留用户改过的那部分。
//
// 节点名与模板代理组名/内置目标（DIRECT/REJECT/PASS）的同名冲突需要模板信息，
// 不在此处校验（见 subscription.normalizeCustomNodes）。
func Normalize(node config.CustomNode) (config.CustomNode, error) {
	proto, ok := Lookup(node.Type)
	if !ok {
		return config.CustomNode{}, fmt.Errorf("不支持的协议类型: %s", node.Type)
	}
	if err := config.ValidateCustomNodeName(node.Name); err != nil {
		return config.CustomNode{}, err
	}

	cfg, err := normalizeFields(proto.Fields, node.Config, "")
	if err != nil {
		return config.CustomNode{}, err
	}

	// RequireAny：外层「或」、内层「与」——满足任意一组即通过。
	// 判据是「有效取值」（默认值也算已填），因此有默认值的字段不会误报。
	if len(proto.RequireAny) > 0 {
		effective := effectiveValues(proto.Fields, node.Config)
		satisfied := false
		for _, group := range proto.RequireAny {
			if len(group) == 0 {
				continue
			}
			all := true
			for _, key := range group {
				if IsEmpty(effective[key]) {
					all = false
					break
				}
			}
			if all {
				satisfied = true
				break
			}
		}
		if !satisfied {
			alts := make([]string, 0, len(proto.RequireAny))
			for _, alt := range proto.RequireAny {
				alts = append(alts, strings.Join(alt, " + "))
			}
			return config.CustomNode{}, fmt.Errorf("以下组合至少需要填写一组: %s", strings.Join(alts, " / "))
		}
	}

	out := config.CustomNode{ID: node.ID, Name: node.Name, Type: proto.Type, Config: cfg}
	if len(out.Config) == 0 {
		out.Config = nil
	}
	return out, nil
}

// normalizeFields 校验并归一化一层字段，返回「只含与默认值不同的取值」。
//
// prefix 用于把嵌套块的错误信息写成 ws-opts.path 这样的路径，
// 用户才能看出是哪一层填错了。
func normalizeFields(fields []Field, raw map[string]any, prefix string) (map[string]any, error) {
	if err := checkUnknownKeys(fields, raw, prefix); err != nil {
		return nil, err
	}

	// 有效取值：用户填的按声明类型转换，没填的用默认值。
	//
	// 嵌套块的判据是「块内是否有子字段真的填了」而不是「请求里有没有这个键」：
	// 读取接口会把块的每个子字段都补齐默认值下发，前端原样带回时整块必然非空，
	// 若按「键存在」判定，块内必填项会对着空气报错（实测会把正常节点拦下来）。
	// 但未知子键无论块是否为空都要拦下——否则写错键名会被静默忽略。
	effective := make(map[string]any, len(fields))
	for _, f := range fields {
		value, present := raw[f.Key]

		if f.Kind == KindGroup {
			if !present || IsEmpty(value) {
				effective[f.Key] = nil
				continue
			}
			nested, ok := asMap(value)
			if !ok {
				return nil, fmt.Errorf("字段 %s%s 应为选项块", prefix, f.Key)
			}
			if err := checkUnknownKeys(f.Children, nested, prefix+f.Key+"."); err != nil {
				return nil, err
			}
			if !blockFilled(f.Children, nested) {
				effective[f.Key] = nil
				continue
			}
			coerced, err := coerce(f, value, prefix+f.Key)
			if err != nil {
				return nil, err
			}
			effective[f.Key] = coerced
			continue
		}

		if !present || IsEmpty(value) {
			effective[f.Key] = defaultOf(f)
			continue
		}
		coerced, err := coerce(f, value, prefix+f.Key)
		if err != nil {
			return nil, err
		}
		effective[f.Key] = coerced
	}

	// 必填：嵌套块为空即整块未填，此时不检查块内必填（块要么不出现、要么填齐）
	for _, f := range fields {
		if f.Kind == KindGroup || !f.Required {
			continue
		}
		if IsEmpty(effective[f.Key]) {
			return nil, fmt.Errorf("缺少必填字段: %s%s（%s）", prefix, f.Key, f.Label)
		}
	}

	out := make(map[string]any, len(fields))
	for _, f := range fields {
		value := effective[f.Key]
		if f.Kind == KindGroup {
			if nested, ok := value.(map[string]any); ok && len(nested) > 0 {
				out[f.Key] = nested
			}
			continue
		}
		if IsEmpty(value) || reflect.DeepEqual(value, defaultOf(f)) {
			continue
		}
		out[f.Key] = value
	}
	return out, nil
}

// checkUnknownKeys 拦下一层字段里的未知键（含嵌套块的子键），报错带上完整路径。
func checkUnknownKeys(fields []Field, raw map[string]any, prefix string) error {
	known := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		known[f.Key] = struct{}{}
	}
	for key := range raw {
		if _, ok := known[key]; !ok {
			return fmt.Errorf("不支持字段: %s%s", prefix, key)
		}
	}
	return nil
}

// blockFilled 判定一个嵌套块是否「被填写」：至少有一个子字段（嵌套块则递归）
// 拿到了非空取值。空块（含「所有子字段都是默认值」的块）视为整块未填。
func blockFilled(fields []Field, raw map[string]any) bool {
	for _, f := range fields {
		value, present := raw[f.Key]
		if !present || IsEmpty(value) {
			continue
		}
		if f.Kind == KindGroup {
			nested, ok := asMap(value)
			if ok && blockFilled(f.Children, nested) {
				return true
			}
			continue
		}
		return true
	}
	return false
}

// effectiveValues 返回「补齐默认值」后的一层取值（用于 RequireAny 判定）。
func effectiveValues(fields []Field, raw map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		value, present := raw[f.Key]
		filled := present && !IsEmpty(value)
		if filled && f.Kind == KindGroup {
			if nested, ok := asMap(value); !ok || !blockFilled(f.Children, nested) {
				filled = false
			}
		}
		if !filled {
			if f.Kind == KindGroup {
				out[f.Key] = nil
				continue
			}
			out[f.Key] = defaultOf(f)
			continue
		}
		coerced, err := coerce(f, value, f.Key)
		if err != nil {
			out[f.Key] = defaultOf(f)
			continue
		}
		out[f.Key] = coerced
	}
	return out
}

// coerce 按字段声明类型转换取值，并校验下拉选项。
//
// path 是错误信息里的字段路径（顶层字段即键名，嵌套子字段形如 ws-opts.path）。
func coerce(f Field, raw any, path string) (any, error) {
	switch f.Kind {
	case KindString, KindText:
		s, ok := asString(raw)
		if !ok {
			return nil, fmt.Errorf("字段 %s 应为文本", path)
		}
		return s, nil

	case KindSelect:
		s, ok := asString(raw)
		if !ok {
			return nil, fmt.Errorf("字段 %s 应为文本", path)
		}
		if len(f.Options) > 0 && !containsString(f.Options, s) {
			return nil, fmt.Errorf("字段 %s 的取值不在可选范围内（%s）", path, strings.Join(f.Options, " / "))
		}
		return s, nil

	case KindInt:
		n, ok := asInt(raw)
		if !ok {
			return nil, fmt.Errorf("字段 %s 应为整数", path)
		}
		return n, nil

	case KindBool:
		b, ok := asBool(raw)
		if !ok {
			return nil, fmt.Errorf("字段 %s 应为布尔值", path)
		}
		return b, nil

	case KindList:
		items, ok := asStringList(raw)
		if !ok {
			return nil, fmt.Errorf("字段 %s 应为列表（可用逗号或换行分隔）", path)
		}
		return items, nil

	case KindMap:
		m, ok := asStringMap(raw)
		if !ok {
			return nil, fmt.Errorf("字段 %s 应为键值对（每行一条「键: 值」）", path)
		}
		return m, nil

	case KindGroup:
		mapping, ok := asMap(raw)
		if !ok {
			return nil, fmt.Errorf("字段 %s 应为选项块", path)
		}
		nested, err := normalizeFields(f.Children, mapping, path+".")
		if err != nil {
			return nil, err
		}
		return nested, nil

	default:
		return nil, fmt.Errorf("字段 %s 的取值类型未知: %s", path, f.Kind)
	}
}

// defaultOf 返回字段的有效默认值：未声明 Default 时按类型给出零值，
// 保证界面上的开关/输入框总有确定取值。
func defaultOf(f Field) any {
	if f.Default != nil {
		return f.Default
	}
	switch f.Kind {
	case KindInt:
		return 0
	case KindBool:
		return false
	case KindList:
		return []string{}
	case KindMap:
		return map[string]string{}
	case KindGroup:
		return nil
	default:
		return ""
	}
}

// IsEmpty 判定取值是否为空（空值一律视为「未填写」→ 用默认值、不落库也不下发）。
//
// 写 YAML 时同样用它过滤：空值的可选项不下发，交由内核使用自身默认值。
func IsEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case bool:
		return !t
	case int:
		return t == 0
	case int64:
		return t == 0
	case float64:
		return t == 0
	case []string:
		return len(t) == 0
	case []any:
		return len(t) == 0
	case map[string]string:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	default:
		return false
	}
}

// asString 把 JSON 里的标量转成字符串（数字与布尔也接受，便于前端简单控件回传）。
func asString(raw any) (string, bool) {
	switch t := raw.(type) {
	case string:
		return strings.TrimSpace(t), true
	case json.Number:
		return t.String(), true
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), true
		}
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case bool:
		return strconv.FormatBool(t), true
	default:
		return "", false
	}
}

// asInt 把 JSON 取值转成整数。
func asInt(raw any) (int, bool) {
	switch t := raw.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		// JSON 解出的数字都是 float64：只接受整数值，避免 1.5 被静默截断
		if t != float64(int64(t)) {
			return 0, false
		}
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// asBool 把 JSON 取值转成布尔。
func asBool(raw any) (bool, bool) {
	switch t := raw.(type) {
	case bool:
		return t, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		if err != nil {
			return false, false
		}
		return b, true
	default:
		return false, false
	}
}

// asStringList 把 JSON 取值转成字符串列表：接受序列，也接受逗号/换行分隔的文本。
func asStringList(raw any) ([]string, bool) {
	switch t := raw.(type) {
	case []string:
		return trimAll(t), true
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := asString(item)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return trimAll(out), true
	case string:
		fields := strings.FieldsFunc(t, func(r rune) bool {
			return r == ',' || r == '\n' || r == '，'
		})
		return trimAll(fields), true
	default:
		return nil, false
	}
}

// asMap 把 JSON 取值转成映射（嵌套块）。
func asMap(raw any) (map[string]any, bool) {
	switch t := raw.(type) {
	case map[string]any:
		return t, true
	case map[string]string:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = v
		}
		return out, true
	default:
		return nil, false
	}
}

// asStringMap 把 JSON 取值转成键值对：接受映射，也接受「每行 键: 值」的文本。
func asStringMap(raw any) (map[string]string, bool) {
	switch t := raw.(type) {
	case map[string]string:
		return trimMap(t), true
	case map[string]any:
		out := make(map[string]string, len(t))
		for k, v := range t {
			s, ok := asString(v)
			if !ok {
				return nil, false
			}
			out[k] = s
		}
		return trimMap(out), true
	case string:
		out := map[string]string{}
		for _, line := range strings.Split(t, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			sep := strings.IndexAny(line, ":=")
			if sep < 0 {
				return nil, false
			}
			key := strings.TrimSpace(line[:sep])
			value := strings.Trim(strings.TrimSpace(line[sep+1:]), `"'`)
			if key == "" {
				continue
			}
			out[key] = value
		}
		return out, true
	default:
		return nil, false
	}
}

// trimAll 逐项去空白并丢弃空项。
func trimAll(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// trimMap 去掉键值对两端的空白，并丢弃空键。
func trimMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(v)
	}
	return out
}

// containsString 判定字符串是否在列表中。
func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// SortedKeys 返回映射的有序键（写 YAML 时保证产物稳定）。
func SortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
