package subscription

import (
	"fluxor/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// airportFixture 是一份贴近真实机场下发的订阅文件：只有块，没有任何 Fluxor 端口/密钥键。
//
// 这正是问题场景：patchSubscriptionFile 注入的键在源文件里不存在，doc.Set 只能追加到
// 末尾——若不归一化键序，它们会排到 rules 之后。
const airportFixture = `mode: rule
proxies:
  - {name: "香港 01", type: ss, server: 127.0.0.1, port: 1, cipher: aes-128-gcm, password: x}
  - {name: "日本 01", type: ss, server: 127.0.0.1, port: 1, cipher: aes-128-gcm, password: x}
proxy-groups:
  - {name: "🚀 节点选择", type: select, proxies: ["香港 01", "日本 01"]}
rules:
  - DOMAIN-SUFFIX,example.org,🚀 节点选择
  - MATCH,🚀 节点选择
`

// TestPatchSubscriptionFileKeysBeforeBlocks 切换模式的 config.yaml 是这份文件的副本，
// 因此这里必须满足「顶层键在前、顶层块在后」，且块顺序与内容不被破坏。
func TestPatchSubscriptionFileKeysBeforeBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "机场A.yaml")
	if err := os.WriteFile(path, []byte(airportFixture), 0644); err != nil {
		t.Fatalf("写入夹具失败: %v", err)
	}

	oldSocket := config.CoreSocket
	config.CoreSocket = "/tmp/fluxor-test-core.sock"
	defer func() { config.CoreSocket = oldSocket }()

	cfg := config.SubscribeConfig{
		ProxyPort:   7890,
		TproxyPort:  7895,
		PanelPort:   9090,
		PanelSecret: "s3cret",
		UIPanel:     "zashboard",
	}
	if err := patchSubscriptionFile(path, cfg); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		t.Fatalf("产物不是合法 YAML: %v", err)
	}
	top := root.Content[0]

	var keys []string
	seenBlock := ""
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, value := top.Content[i].Value, top.Content[i+1]
		keys = append(keys, key)
		block := value.Kind == yaml.MappingNode || value.Kind == yaml.SequenceNode
		if block {
			if seenBlock == "" {
				seenBlock = key
			}
			continue
		}
		if seenBlock != "" {
			t.Fatalf("顶层键 %s 排在块 %s 之后（要求键在前、块在后）:\n%v", key, seenBlock, keys)
		}
	}

	// 块序固定：dns 在代理/规则数据之前，末尾依次 proxy-groups / proxies / rules
	var blocks []string
	for _, key := range keys {
		switch key {
		case "proxies", "proxy-groups", "rules", "dns":
			blocks = append(blocks, key)
		}
	}
	if want := []string{"dns", "proxy-groups", "proxies", "rules"}; strings.Join(blocks, "|") != strings.Join(want, "|") {
		t.Fatalf("块序错误:\n实际: %v\n期望: %v\n完整键序: %v", blocks, want, keys)
	}
	if keys[len(keys)-1] != "rules" {
		t.Fatalf("rules 应为最后一个顶层键，实际键序: %v", keys)
	}

	// 注入的键值必须都落到标量区
	text := string(content)
	for _, want := range []string{
		"mixed-port: 7890",
		"tproxy-port: 7895",
		"external-controller: 0.0.0.0:9090",
		"external-controller-unix: /tmp/fluxor-test-core.sock",
		"secret: s3cret",
		"external-ui: ui/zash",
		"allow-lan: true",
		"DOMAIN-SUFFIX,example.org,🚀 节点选择",
		"MATCH,🚀 节点选择",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("产物缺少 %q:\n%s", want, text)
		}
	}
}

// TestPatchSubscriptionFileRemovesConflictingPorts 多余的端口键仍须被删除（原有行为）。
func TestPatchSubscriptionFileRemovesConflictingPorts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "机场B.yaml")
	body := "port: 1080\nsocks-port: 1081\nredir-port: 1082\nmixed-port: 7890\n" + airportFixture
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("写入夹具失败: %v", err)
	}
	if err := patchSubscriptionFile(path, config.SubscribeConfig{ProxyPort: 7890, TproxyPort: 7895, PanelPort: 9090}); err != nil {
		t.Fatalf("打补丁失败: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	for _, banned := range []string{"port: 1080", "socks-port", "redir-port"} {
		if strings.Contains(string(content), banned) {
			t.Fatalf("产物中仍有冲突端口键 %q:\n%s", banned, content)
		}
	}
}

func indexOfKey(list []string, want string) int {
	for i, item := range list {
		if item == want {
			return i
		}
	}
	return -1
}
