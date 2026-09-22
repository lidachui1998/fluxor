package configcheck

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// topLevelOrder 返回顶层映射的键顺序，同时给出每个键是否为「块」（映射/序列值）。
func topLevelOrder(t *testing.T, content []byte) ([]string, []bool) {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		t.Fatalf("顶层应为映射")
	}
	var keys []string
	var isBlock []bool
	top := root.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		keys = append(keys, top.Content[i].Value)
		isBlock = append(isBlock, isBlockValue(top.Content[i+1]))
	}
	return keys, isBlock
}

// TestOrderTopLevelHoistsKeys 标量键整体前移，块按声明顺序排在最后。
func TestOrderTopLevelHoistsKeys(t *testing.T) {
	doc, err := ParseDoc([]byte(`mode: rule
mixed-port: 7890
proxy-providers:
  p1:
    type: file
rules:
  - MATCH,DIRECT
tproxy-port: 7895
dns:
  enable: true
secret: s3cret
`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	doc.OrderTopLevel()

	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	keys, isBlock := topLevelOrder(t, out)
	want := []string{"mode", "mixed-port", "tproxy-port", "secret", "dns", "proxy-providers", "rules"}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Fatalf("键序错误:\n实际: %v\n期望: %v\n%s", keys, want, out)
	}
	// 前 4 个必须是标量键，其余必须都是块
	for i, block := range isBlock {
		if wantBlock := i >= 4; block != wantBlock {
			t.Fatalf("第 %d 个键 %s 的块属性错误: isBlock=%v 期望 %v", i, keys[i], block, wantBlock)
		}
	}

	// 内容不得被改动（只调顺序）
	text := string(out)
	for _, needle := range []string{"mode: rule", "mixed-port: 7890", "tproxy-port: 7895", "secret: s3cret",
		"type: file", "MATCH,DIRECT", "enable: true"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("产物缺少 %q:\n%s", needle, text)
		}
	}
	if strings.Count(text, "tproxy-port") != 1 {
		t.Fatalf("键被重复写入:\n%s", text)
	}
}

// TestOrderTopLevelKeepsOrderInsideGroups 同组内保持原有相对顺序（稳定排序）。
func TestOrderTopLevelKeepsOrderInsideGroups(t *testing.T) {
	doc, err := ParseDoc([]byte(`z-block:
  a: 1
b-key: 1
a-block:
  b: 2
a-key: 2
`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	doc.OrderTopLevel()
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	keys, _ := topLevelOrder(t, out)
	want := []string{"b-key", "a-key", "z-block", "a-block"}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Fatalf("键序错误:\n实际: %v\n期望: %v", keys, want)
	}
}

// TestOrderTopLevelNoopWhenAlreadyOrdered 已满足顺序时不做任何改动。
func TestOrderTopLevelNoopWhenAlreadyOrdered(t *testing.T) {
	for name, body := range map[string]string{
		"键在前块在后": `mode: rule
rules:
  - MATCH,DIRECT
`,
		"全是键": `mode: rule
mixed-port: 7890
`,
		"全是块": `dns:
  enable: true
rules:
  - MATCH,DIRECT
`,
		"空文档": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := ParseDoc([]byte(body))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			before, err := doc.Bytes()
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			doc.OrderTopLevel()
			after, err := doc.Bytes()
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			if string(before) != string(after) {
				t.Fatalf("不应改动:\n之前:\n%s\n之后:\n%s", before, after)
			}
		})
	}
}

// TestOrderTopLevelEmptyCollectionsAreBlocks 空映射/空序列仍按块处理，留在后方。
func TestOrderTopLevelEmptyCollectionsAreBlocks(t *testing.T) {
	doc, err := ParseDoc([]byte(`proxy-groups: []
mode: rule
dns: {}
`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	doc.OrderTopLevel()
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	keys, _ := topLevelOrder(t, out)
	want := []string{"mode", "dns", "proxy-groups"}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Fatalf("键序错误:\n实际: %v\n期望: %v", keys, want)
	}
}

// TestOrderTopLevelBlockOrder 块之间的先后：dns 在 proxy-providers 之前，
// 末尾依次 proxy-providers / proxy-groups / proxies / rule-providers / rules。
func TestOrderTopLevelBlockOrder(t *testing.T) {
	// 故意打乱：dns 在末尾、proxies 在最前、未声明的块与声明块交错
	doc, err := ParseDoc([]byte(`rules:
  - MATCH,DIRECT
tun:
  enable: false
proxies:
  - {name: n1, type: ss}
mode: rule
rule-providers:
  r1: {type: http}
proxy-groups:
  - {name: G, type: select}
experimental:
  quic: true
proxy-providers:
  p1: {type: file}
dns:
  enable: true
mixed-port: 7890
`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	doc.OrderTopLevel()
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	keys, _ := topLevelOrder(t, out)
	// 标量键 -> 未声明的块（原序：tun、experimental）-> dns -> 代理/规则数据
	want := []string{
		"mode", "mixed-port",
		"tun", "experimental",
		"dns", "proxy-providers", "proxy-groups", "proxies", "rule-providers", "rules",
	}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Fatalf("顶层键序错误:\n实际: %v\n期望: %v\n%s", keys, want, out)
	}
}

// TestOrderTopLevelProxyGroupsBeforeProxies 切换模式：proxies 排在 proxy-groups 之后。
func TestOrderTopLevelProxyGroupsBeforeProxies(t *testing.T) {
	doc, err := ParseDoc([]byte(`proxies:
  - {name: n1, type: ss}
rules:
  - MATCH,DIRECT
proxy-groups:
  - {name: G, type: select}
`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	doc.OrderTopLevel()
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	keys, _ := topLevelOrder(t, out)
	want := []string{"proxy-groups", "proxies", "rules"}
	if strings.Join(keys, "|") != strings.Join(want, "|") {
		t.Fatalf("顶层键序错误:\n实际: %v\n期望: %v", keys, want)
	}
}

// TestSetNodeBefore 定位插入：新键落在最早出现的锚点键之前，锚点缺失时追加到末尾。
//
// 这是切换模式 config.yaml（订阅文件副本，不做整体键序归一）里 tunnels 块的定位手段：
// 靠 Set/SetNode 追加会让它落到 rules 之后，与「tunnels 写在 rule-providers / rules 之前」
// 的要求相悖。
func TestSetNodeBefore(t *testing.T) {
	newSeq := func() *yaml.Node {
		return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}

	cases := []struct {
		name    string
		fixture string
		anchors []string
		want    []string
	}{
		{
			name: "插在最靠前的锚点之前",
			fixture: `mode: rule
proxies:
  - {name: n1, type: ss}
rule-providers:
  ads: {type: http}
rules:
  - MATCH,DIRECT
`,
			anchors: []string{"rule-providers", "rules"},
			want:    []string{"mode", "proxies", "tunnels", "rule-providers", "rules"},
		},
		{
			name: "只命中后一个锚点",
			fixture: `mode: rule
proxies:
  - {name: n1, type: ss}
rules:
  - MATCH,DIRECT
`,
			anchors: []string{"rule-providers", "rules"},
			want:    []string{"mode", "proxies", "tunnels", "rules"},
		},
		{
			name: "锚点都不存在时追加到末尾",
			fixture: `mode: rule
proxies:
  - {name: n1, type: ss}
`,
			anchors: []string{"rule-providers", "rules"},
			want:    []string{"mode", "proxies", "tunnels"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := ParseDoc([]byte(tc.fixture))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			doc.SetNodeBefore("tunnels", newSeq(), tc.anchors...)
			out, err := doc.Bytes()
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			keys, _ := topLevelOrder(t, out)
			if strings.Join(keys, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("键序错误:\n实际: %v\n期望: %v\n%s", keys, tc.want, out)
			}
		})
	}

	// 键已存在时就地覆盖，位置不变（幂等重写的必要条件）
	doc, err := ParseDoc([]byte("mode: rule\ntunnels:\n  - old\nrules:\n  - MATCH,DIRECT\n"))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	doc.SetNodeBefore("tunnels", newSeq(), "rules")
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	keys, _ := topLevelOrder(t, out)
	if strings.Join(keys, "|") != "mode|tunnels|rules" {
		t.Fatalf("覆盖后键序不应变化: %v", keys)
	}
	if strings.Contains(string(out), "old") {
		t.Fatalf("旧值应被覆盖: %s", out)
	}
}
