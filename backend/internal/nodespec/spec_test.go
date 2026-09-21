package nodespec

import (
	"reflect"
	"strings"
	"testing"

	"fluxor/internal/config"
)

// TestProtocolTables 校验协议表自身的结构约定：
// 键唯一、类型合法、下拉字段有可选值且默认值在范围内、布尔默认不开启、
// 分组/条件显示的引用有效、分区顺序单调、通用高级项齐备。
func TestProtocolTables(t *testing.T) {
	seenTypes := map[string]bool{}
	for _, p := range Protocols() {
		if p.Type == "" || p.Name == "" {
			t.Fatalf("协议缺少 type/name: %+v", p)
		}
		if p.Type != strings.ToLower(p.Type) {
			t.Errorf("协议 %s 的 type 应为小写", p.Type)
		}
		if seenTypes[p.Type] {
			t.Errorf("协议 type 重复: %s", p.Type)
		}
		seenTypes[p.Type] = true

		if len(p.Fields) == 0 {
			t.Errorf("协议 %s 没有任何字段", p.Type)
		}

		verifyFields(t, p, p.Fields, "", SectionBasic)

		// 通用高级项必须整组出现（全部协议都内嵌 BasicOption）
		if count := countKeys(p.Fields, basicAdvanced); count != len(basicAdvanced) {
			t.Errorf("协议 %s 缺少通用高级字段（%d/%d）", p.Type, count, len(basicAdvanced))
		}

		// 除通用高级项外，每个协议都应声明自己的分区字段（传输层 / TLS / 协议级高级项）
		ownSection := 0
		for _, f := range p.Fields {
			if f.Section == SectionBasic || isCommonAdvanced(f.Key) {
				continue
			}
			ownSection++
		}
		if ownSection == 0 {
			t.Errorf("协议 %s 没有任何分区字段（传输层/TLS/高级）", p.Type)
		}

		for _, group := range p.RequireAny {
			for _, key := range group {
				if !hasKey(p.Fields, key) {
					t.Errorf("协议 %s 的 RequireAny 引用了不存在的字段: %s", p.Type, key)
				}
			}
		}
	}
}

// verifyFields 递归校验一层字段，嵌套块一并检查。
func verifyFields(t *testing.T, p Protocol, fields []Field, prefix, parentSection string) {
	t.Helper()

	keys := map[string]bool{}
	lastRank := -1
	for _, f := range fields {
		if f.Key == "" || f.Label == "" {
			t.Errorf("协议 %s 存在缺少 key/label 的字段: %+v", p.Type, f)
		}
		fullKey := prefix + f.Key
		if f.Key == "name" || f.Key == "type" {
			t.Errorf("协议 %s 不应把 name/type 放进字段表（界面单独处理）", p.Type)
		}
		if keys[f.Key] {
			t.Errorf("协议 %s 的字段键重复: %s", p.Type, fullKey)
		}
		keys[f.Key] = true

		// 分区：顶层字段按声明顺序排布（基础 → 传输层 → TLS → 高级），子字段不分区
		if prefix == "" {
			if f.Section != SectionBasic && f.Section != SectionTransport && f.Section != SectionTLS && f.Section != SectionAdvanced {
				t.Errorf("协议 %s 的字段 %s 分区未知: %q", p.Type, fullKey, f.Section)
			}
			if rank := sectionRank(f.Section); rank < lastRank {
				t.Errorf("协议 %s 的字段 %s 分区顺序错乱（应为 基础 → 传输层 → TLS → 高级）", p.Type, fullKey)
			} else {
				lastRank = rank
			}
		} else if f.Section != SectionBasic {
			t.Errorf("协议 %s 的嵌套子字段 %s 不应自带分区", p.Type, fullKey)
		}

		switch f.Kind {
		case KindString, KindText, KindInt, KindBool, KindList, KindMap:
			if len(f.Children) > 0 {
				t.Errorf("协议 %s 的字段 %s 不是嵌套块却声明了子字段", p.Type, fullKey)
			}
		case KindSelect:
			if len(f.Options) == 0 {
				t.Errorf("协议 %s 的下拉字段 %s 没有可选值", p.Type, fullKey)
			}
			if def, ok := f.Default.(string); ok && def != "" && !containsString(f.Options, def) {
				t.Errorf("协议 %s 的下拉字段 %s 默认值 %q 不在可选值内", p.Type, fullKey, def)
			}
		case KindGroup:
			if len(f.Children) == 0 {
				t.Errorf("协议 %s 的嵌套块 %s 没有子字段", p.Type, fullKey)
			}
			verifyFields(t, p, f.Children, fullKey+".", f.Section)
		default:
			t.Errorf("协议 %s 的字段 %s 取值类型未知: %s", p.Type, fullKey, f.Kind)
		}

		if f.Kind == KindBool {
			if def, ok := f.Default.(bool); ok && def {
				// 界面上「关」等于用默认值、不落库；若默认是 true，用户关闭后会被当成默认值丢弃
				t.Errorf("协议 %s 的布尔字段 %s 不应默认开启（关闭会被误判为默认值）", p.Type, fullKey)
			}
		}
		if f.Required && f.Default != nil && IsEmpty(f.Default) {
			t.Errorf("协议 %s 的必填字段 %s 的默认值为空值", p.Type, fullKey)
		}

		// 条件显示只能引用同层字段，且必须给出命中取值
		if f.VisibleWhen != nil {
			if len(f.VisibleWhen.Values) == 0 {
				t.Errorf("协议 %s 的字段 %s 的条件显示没有取值", p.Type, fullKey)
			}
			if !keys[f.VisibleWhen.Key] {
				t.Errorf("协议 %s 的字段 %s 的条件显示引用了不存在（或尚未声明）的同层字段: %s",
					p.Type, fullKey, f.VisibleWhen.Key)
			}
		}
	}
}

// TestProtocolCount 记录协议覆盖范围：通用字段 + HTTP → OpenVPN 共 23 个协议。
func TestProtocolCount(t *testing.T) {
	if got := len(Protocols()); got != 23 {
		t.Fatalf("协议数量应为 23，实际 %d", got)
	}
}

// TestTransportBlocksConditional 传输层选项块必须按 network 取值条件显示，
// 否则五个块（含几十个子字段）会同时铺满表单。
func TestTransportBlocksConditional(t *testing.T) {
	cases := map[string]map[string]string{
		"vmess":  {"ws-opts": "ws", "h2-opts": "h2", "grpc-opts": "grpc", "http-opts": "http", "mkcp-opts": "mkcp"},
		"vless":  {"ws-opts": "ws", "h2-opts": "h2", "grpc-opts": "grpc", "http-opts": "http", "xhttp-opts": "xhttp"},
		"trojan": {"ws-opts": "ws", "grpc-opts": "grpc"},
	}
	for protoType, blocks := range cases {
		p, ok := Lookup(protoType)
		if !ok {
			t.Fatalf("找不到协议 %s", protoType)
		}
		if !hasKey(p.Fields, "network") {
			t.Fatalf("协议 %s 应声明 network 选择器", protoType)
		}
		for blockKey, networkValue := range blocks {
			field, ok := findFieldInList(p.Fields, blockKey)
			if !ok {
				t.Fatalf("协议 %s 缺少传输块 %s", protoType, blockKey)
			}
			if field.Kind != KindGroup || field.Section != SectionTransport {
				t.Fatalf("协议 %s 的 %s 应为传输层嵌套块，实际 kind=%s section=%q", protoType, blockKey, field.Kind, field.Section)
			}
			if field.VisibleWhen == nil || field.VisibleWhen.Key != "network" ||
				!containsString(field.VisibleWhen.Values, networkValue) {
				t.Fatalf("协议 %s 的 %s 条件显示应为 network=%s，实际 %+v", protoType, blockKey, networkValue, field.VisibleWhen)
			}
		}
	}
}

// TestNormalizeStripsDefaults 与默认值相同的取值不落库（含嵌套块整块剔除）。
func TestNormalizeStripsDefaults(t *testing.T) {
	node := config.CustomNode{
		Name: "香港 01",
		Type: "ss",
		Config: map[string]any{
			"server":   "example.com",
			"port":     float64(8388), // JSON 解出的数字是 float64
			"password": "secret",
			"cipher":   "aes-256-gcm", // 等于默认值 → 应被剔除
			"udp":      false,         // 等于默认值 → 应被剔除
			"tfo":      false,         // 通用高级项默认值 → 应被剔除
		},
	}
	out, err := Normalize(node)
	if err != nil {
		t.Fatalf("归一化失败: %v", err)
	}
	want := map[string]any{"server": "example.com", "port": 8388, "password": "secret"}
	if !reflect.DeepEqual(out.Config, want) {
		t.Fatalf("落库字段应为 %+v，实际 %+v", want, out.Config)
	}

	// 补齐后应恢复出完整取值（界面展示与写 YAML 都依赖它）
	full, err := Materialize(out)
	if err != nil {
		t.Fatalf("补齐失败: %v", err)
	}
	if full["cipher"] != "aes-256-gcm" || full["port"] != 8388 || full["ip-version"] != "dual" {
		t.Fatalf("补齐结果不正确: %+v", full)
	}
}

// TestNestedGroupRoundTrip 嵌套块（传输层 / TLS）必须能原样往返：
// 落库为嵌套映射、补齐后仍在、写盘时只保留非空子项。
func TestNestedGroupRoundTrip(t *testing.T) {
	node := config.CustomNode{
		Name: "ws 节点",
		Type: "vmess",
		Config: map[string]any{
			"server":       "example.com",
			"port":         443,
			"uuid":         "b831381d-6324-4d53-ad4f-8cda48b30811",
			"tls":          true,
			"network":      "ws",
			"ws-opts":      map[string]any{"path": "/video", "headers": "Host: cdn.example.com"},
			"reality-opts": map[string]any{"public-key": "abc", "short-id": "0123"},
			"h2-opts":      map[string]any{"path": "/h2"}, // network != h2：值仍保留（不静默丢弃）
		},
	}
	out, err := Normalize(node)
	if err != nil {
		t.Fatalf("归一化失败: %v", err)
	}

	ws, ok := out.Config["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("ws-opts 应为嵌套映射，实际 %#v", out.Config["ws-opts"])
	}
	if ws["path"] != "/video" {
		t.Fatalf("ws-opts.path 未保留: %#v", ws)
	}
	headers, ok := ws["headers"].(map[string]string)
	if !ok || headers["Host"] != "cdn.example.com" {
		t.Fatalf("ws-opts.headers 应解析为键值对: %#v", ws["headers"])
	}
	if reality, ok := out.Config["reality-opts"].(map[string]any); !ok || reality["public-key"] != "abc" {
		t.Fatalf("reality-opts 未保留: %#v", out.Config["reality-opts"])
	}

	// 幂等：再归一化一次结果必须一致
	again, err := Normalize(out)
	if err != nil {
		t.Fatalf("二次归一化失败: %v", err)
	}
	if !reflect.DeepEqual(out.Config, again.Config) {
		t.Fatalf("嵌套块归一化不幂等:\n第一次 %#v\n第二次 %#v", out.Config, again.Config)
	}

	// 写盘：空块（未填的 h2-opts）应被剔除，非空块保留
	kvs, err := WritableOrdered(again)
	if err != nil {
		t.Fatalf("写盘取值失败: %v", err)
	}
	written := kvToMap(kvs)
	// 从未填过的块整块不下发（全空）
	if _, ok := written["grpc-opts"]; ok {
		t.Fatalf("全空的嵌套块不应下发: %#v", written["grpc-opts"])
	}
	// 填过的块即使当前 network 不用也要保留（不静默丢用户输入）
	if h2, ok := written["h2-opts"].(map[string]any); !ok || h2["path"] != "/h2" {
		t.Fatalf("已填写的嵌套块应保留: %#v", written["h2-opts"])
	}
	writtenWS, ok := written["ws-opts"].(map[string]any)
	if !ok || writtenWS["path"] != "/video" {
		t.Fatalf("非空嵌套块应下发: %#v", written)
	}
	if _, ok := writtenWS["v2ray-http-upgrade"]; ok {
		// 取默认值（false）的子项不下发：内核缺省即同值，写出来只是噪音
		t.Fatalf("取默认值的子项不应下发: %#v", writtenWS)
	}
	// 内核要求存在的键（vmess.alterId）即使为 0 也要下发
	if value, ok := written["alterId"]; !ok || value != 0 {
		t.Fatalf("alterId 必须下发且为 0，实际 %#v", written["alterId"])
	}
}

// TestMaterializedRoundTrip 读取接口下发的「补齐默认值」结果必须能原样存回。
//
// 这是界面最容易踩的一条链路（GET → 表单 → 保存）：空块会被补齐成
// 「所有子字段的默认值」，若把「键存在」当成「块被填写」，块内必填项就会对着
// 空气报错——实测曾把正常节点的保存拦下来。
func TestMaterializedRoundTrip(t *testing.T) {
	for _, p := range Protocols() {
		base := config.CustomNode{Name: "n", Type: p.Type, Config: sampleConfig(p)}
		stored, err := Normalize(base)
		if err != nil {
			t.Fatalf("协议 %s 归一化失败: %v", p.Type, err)
		}
		materialized, err := Materialize(stored)
		if err != nil {
			t.Fatalf("协议 %s 补齐失败: %v", p.Type, err)
		}
		again, err := Normalize(config.CustomNode{Name: "n", Type: p.Type, Config: materialized})
		if err != nil {
			t.Fatalf("协议 %s 回存失败（补齐结果无法再次归一化）: %v", p.Type, err)
		}
		if !reflect.DeepEqual(stored.Config, again.Config) {
			t.Fatalf("协议 %s 回存后取值漂移:\n原存 %#v\n回存 %#v", p.Type, stored.Config, again.Config)
		}
	}
}

// TestEmptyGroupIsAbsent 全空的嵌套块视为未填：不触发块内必填，也不进产物。
func TestEmptyGroupIsAbsent(t *testing.T) {
	// reality-opts.public-key 必填，但整块未填时不应报错
	node := config.CustomNode{
		Name: "n", Type: "vless",
		Config: map[string]any{
			"server": "x", "port": 443, "uuid": "u",
			// 读取接口下发的形态：块的每个子字段都在，但都是默认值
			"reality-opts": map[string]any{"public-key": "", "short-id": "", "support-x25519mlkem768": false},
			"ws-opts":      map[string]any{"path": "", "headers": map[string]any{}, "max-early-data": 0},
		},
	}
	out, err := Normalize(node)
	if err != nil {
		t.Fatalf("全空嵌套块不应触发必填校验: %v", err)
	}
	if len(out.Config) != 3 {
		t.Fatalf("全空嵌套块不应落库: %#v", out.Config)
	}

	// 块内有值时才要求块内必填
	node.Config["reality-opts"] = map[string]any{"public-key": "", "short-id": "0123"}
	if _, err := Normalize(node); err == nil || !strings.Contains(err.Error(), "reality-opts.public-key") {
		t.Fatalf("填了子项后应要求块内必填，实际: %v", err)
	}
}

// TestNormalizeErrors 归一化必须拒绝会让内核拒绝整份配置的输入。
func TestNormalizeErrors(t *testing.T) {
	cases := []struct {
		name string
		node config.CustomNode
		want string
	}{
		{
			name: "未知协议",
			node: config.CustomNode{Name: "a", Type: "wireguard2"},
			want: "不支持的协议类型",
		},
		{
			name: "缺少必填",
			node: config.CustomNode{Name: "a", Type: "vless", Config: map[string]any{"server": "x", "port": 1}},
			want: "缺少必填字段: uuid",
		},
		{
			name: "未知字段",
			node: config.CustomNode{Name: "a", Type: "ss", Config: map[string]any{
				"server": "x", "port": 1, "cipher": "aes-256-gcm", "password": "p", "ws-path": "/x",
			}},
			want: "不支持字段: ws-path",
		},
		{
			name: "下拉取值非法",
			node: config.CustomNode{Name: "a", Type: "ss", Config: map[string]any{
				"server": "x", "port": 1, "cipher": "aes-999-gcm", "password": "p",
			}},
			want: "取值不在可选范围内",
		},
		{
			name: "整数非法",
			node: config.CustomNode{Name: "a", Type: "ss", Config: map[string]any{
				"server": "x", "port": 1.5, "cipher": "aes-256-gcm", "password": "p",
			}},
			want: "应为整数",
		},
		{
			name: "端口二选一未填",
			node: config.CustomNode{Name: "a", Type: "hysteria2", Config: map[string]any{"server": "x"}},
			want: "至少需要填写一组",
		},
		{
			name: "节点名为空",
			node: config.CustomNode{Name: "  ", Type: "http"},
			want: "节点名不能为空",
		},
		{
			name: "嵌套块未知子键",
			node: config.CustomNode{Name: "a", Type: "vmess", Config: map[string]any{
				"server": "x", "port": 1, "uuid": "u", "ws-opts": map[string]any{"pth": "/x"},
			}},
			want: "不支持字段: ws-opts.pth",
		},
		{
			name: "嵌套块内必填缺失",
			node: config.CustomNode{Name: "a", Type: "vless", Config: map[string]any{
				"server": "x", "port": 1, "uuid": "u", "reality-opts": map[string]any{"short-id": "0123"},
			}},
			want: "缺少必填字段: reality-opts.public-key",
		},
		{
			name: "嵌套块类型不符",
			node: config.CustomNode{Name: "a", Type: "vmess", Config: map[string]any{
				"server": "x", "port": 1, "uuid": "u", "ws-opts": "path=/x",
			}},
			want: "应为选项块",
		},
		{
			name: "键值对格式非法",
			node: config.CustomNode{Name: "a", Type: "vmess", Config: map[string]any{
				"server": "x", "port": 1, "uuid": "u", "network": "ws",
				"ws-opts": map[string]any{"headers": "Hostcdn.example.com"},
			}},
			want: "应为键值对",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Normalize(tc.node)
			if err == nil {
				t.Fatalf("应当报错")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误信息应包含 %q，实际: %v", tc.want, err)
			}
		})
	}
}

// TestNormalizeIdempotent 归一化必须幂等：接口每次保存都会重放一次，
// 若两次结果不同，界面刷新后取值会漂移。
func TestNormalizeIdempotent(t *testing.T) {
	for _, p := range Protocols() {
		node := config.CustomNode{Name: "n", Type: p.Type, Config: sampleConfig(p)}
		first, err := Normalize(node)
		if err != nil {
			t.Fatalf("协议 %s 归一化失败: %v", p.Type, err)
		}
		second, err := Normalize(first)
		if err != nil {
			t.Fatalf("协议 %s 二次归一化失败: %v", p.Type, err)
		}
		if !reflect.DeepEqual(first.Config, second.Config) {
			t.Fatalf("协议 %s 归一化不幂等:\n第一次 %+v\n第二次 %+v", p.Type, first.Config, second.Config)
		}
		// 落库字段必须都能补齐回非空（否则写的配置会缺关键项）
		full, err := Materialize(second)
		if err != nil {
			t.Fatalf("协议 %s 补齐失败: %v", p.Type, err)
		}
		for _, f := range p.Fields {
			if f.Required && IsEmpty(full[f.Key]) {
				t.Fatalf("协议 %s 必填字段 %s 补齐后仍为空", p.Type, f.Key)
			}
		}
		// 写盘取值不得包含空块，且必须能编码（保证产物可用）
		if _, err := WritableOrdered(second); err != nil {
			t.Fatalf("协议 %s 写盘取值失败: %v", p.Type, err)
		}
	}
}

// TestListCoercion 列表字段接受序列与逗号/换行分隔文本。
func TestListCoercion(t *testing.T) {
	base := map[string]any{"server": "1.1.1.1", "port": 443, "password": "p"}
	cases := []struct {
		raw  any
		want []string
	}{
		{"a, b", []string{"a", "b"}},               // 逗号分隔
		{"a\nb\nc", []string{"a", "b", "c"}},       // 换行分隔
		{[]any{"a", "b"}, []string{"a", "b"}},      // JSON 序列
		{[]string{" a ", "b"}, []string{"a", "b"}}, // 逐项去空白
	}
	for _, tc := range cases {
		raw, want := tc.raw, tc.want
		cfg := map[string]any{}
		for k, v := range base {
			cfg[k] = v
		}
		cfg["alpn"] = raw
		out, err := Normalize(config.CustomNode{Name: "n", Type: "trojan", Config: cfg})
		if err != nil {
			t.Fatalf("列表取值 %#v 归一化失败: %v", raw, err)
		}
		if !reflect.DeepEqual(out.Config["alpn"], want) {
			t.Fatalf("列表取值 %#v 应归一化为 %v，实际 %#v", raw, want, out.Config["alpn"])
		}
	}
}

// TestMapCoercion 键值对字段接受「键: 值」文本与映射两种形态。
func TestMapCoercion(t *testing.T) {
	base := map[string]any{
		"server": "x", "port": 1, "uuid": "u", "network": "ws",
	}
	cases := []struct {
		raw  any
		want map[string]string
	}{
		{"Host: cdn.example.com\nUser-Agent: v2ray", map[string]string{"Host": "cdn.example.com", "User-Agent": "v2ray"}},
		{map[string]any{"Host": "cdn.example.com"}, map[string]string{"Host": "cdn.example.com"}},
		{map[string]string{"Host": "cdn.example.com"}, map[string]string{"Host": "cdn.example.com"}},
	}
	for _, tc := range cases {
		cfg := map[string]any{}
		for k, v := range base {
			cfg[k] = v
		}
		cfg["ws-opts"] = map[string]any{"headers": tc.raw}
		out, err := Normalize(config.CustomNode{Name: "n", Type: "vmess", Config: cfg})
		if err != nil {
			t.Fatalf("键值对取值 %#v 归一化失败: %v", tc.raw, err)
		}
		ws := out.Config["ws-opts"].(map[string]any)
		if !reflect.DeepEqual(ws["headers"], tc.want) {
			t.Fatalf("键值对取值 %#v 应归一化为 %v，实际 %#v", tc.raw, tc.want, ws["headers"])
		}
	}
}

// TestMaterializeUnknownProtocol 补齐未知协议必须报错而不是产出半成品。
func TestMaterializeUnknownProtocol(t *testing.T) {
	if _, err := Materialize(config.CustomNode{Name: "n", Type: "nope"}); err == nil {
		t.Fatalf("应当报错")
	}
}

// TestFieldOrderStable 字段顺序必须与声明一致（写 YAML 的键序依赖它）。
func TestFieldOrderStable(t *testing.T) {
	proto, ok := Lookup("ss")
	if !ok {
		t.Fatalf("找不到 ss 协议")
	}
	if len(proto.Fields) < 2 || proto.Fields[0].Key != "server" || proto.Fields[1].Key != "port" {
		t.Fatalf("ss 字段顺序异常: %+v", proto.Fields)
	}
	if _, ok := Lookup("unknown"); ok {
		t.Fatalf("未知协议不应命中")
	}
}

// sectionRank 返回分区的界面顺序权重（基础 → 传输层 → TLS → 高级）。
func sectionRank(section string) int {
	switch section {
	case SectionBasic:
		return 0
	case SectionTransport:
		return 1
	case SectionTLS:
		return 2
	case SectionAdvanced:
		return 3
	default:
		return 4
	}
}

// hasKey 判定顶层字段表中是否存在某键。
func hasKey(fields []Field, key string) bool {
	_, ok := findFieldInList(fields, key)
	return ok
}

// findFieldInList 在字段表中按键查找。
func findFieldInList(fields []Field, key string) (Field, bool) {
	for _, f := range fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// countKeys 统计一组键在字段表中的命中数量。
func countKeys(fields []Field, targets []Field) int {
	count := 0
	for _, target := range targets {
		if hasKey(fields, target.Key) {
			count++
		}
	}
	return count
}

// isCommonAdvanced 判定键是否属于通用高级项。
func isCommonAdvanced(key string) bool {
	for _, f := range basicAdvanced {
		if f.Key == key {
			return true
		}
	}
	return false
}

// sampleConfig 为协议生成一份「填满必填项」的取值，用于往返测试。
func sampleConfig(p Protocol) map[string]any {
	cfg := map[string]any{}
	for _, f := range p.Fields {
		if f.Required {
			cfg[f.Key] = fillSample(f)
		}
	}
	// RequireAny 只需满足第一组（组内字段需全部填写）
	if len(p.RequireAny) > 0 {
		for _, key := range p.RequireAny[0] {
			if f, ok := findFieldInList(p.Fields, key); ok {
				cfg[f.Key] = fillSample(f)
			}
		}
	}
	return cfg
}

// fillSample 递归生成一个合法取值（嵌套块展开其子字段）。
func fillSample(f Field) any {
	if f.Kind == KindGroup {
		nested := map[string]any{}
		for _, child := range f.Children {
			nested[child.Key] = fillSample(child)
		}
		return nested
	}
	return sampleValue(f)
}

// sampleValue 按字段类型给出一个合法取值。
func sampleValue(f Field) any {
	switch f.Kind {
	case KindInt:
		return 1
	case KindBool:
		return true
	case KindList:
		return []any{"x"}
	case KindMap:
		return map[string]any{"K": "V"}
	case KindSelect:
		if len(f.Options) > 0 {
			return f.Options[0]
		}
		return "x"
	default:
		return "x"
	}
}

// findField 在协议里按键查找字段（保持旧用例的可读性）。
func findField(p Protocol, key string) (Field, bool) {
	return findFieldInList(p.Fields, key)
}
