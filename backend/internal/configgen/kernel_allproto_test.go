package configgen

// 全协议内核验证（可选）：为每个协议生成一个节点，产出 config.yaml 后交给真实内核
// `mihomo -t` 校验。字段表、必填项、类型编码任何一处写错，内核都会拒绝加载整份配置，
// 因此这是「后端为某个协议增补/调整默认模板」之后最直接的回归手段。
//
// 运行方式（缺省跳过，常规单测不依赖外部二进制）：
//
//	FLUXOR_CORE_BIN=/path/to/mihomo go test ./internal/configgen/ -run TestAllProtocolsAcceptedByCore -v
//
// 工作目录需要 geoip.metadb / geosite.dat（标准规则集含 GEOIP/GEOSITE 规则），
// 测试从仓库根目录复制；两者缺失时跳过并说明原因。

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fluxor/internal/config"
	"fluxor/internal/nodespec"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllProtocolsAcceptedByCore(t *testing.T) {
	coreBin := os.Getenv("FLUXOR_CORE_BIN")
	if coreBin == "" {
		t.Skip("未设置 FLUXOR_CORE_BIN，跳过内核校验")
	}

	dir := t.TempDir()
	for _, name := range []string{"geoip.metadb", "geosite.dat"} {
		src := filepath.Join("..", "..", "..", name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Skipf("仓库根目录缺少 %s，无法校验含 GEOIP/GEOSITE 的规则集: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			t.Fatalf("复制 %s 失败: %v", name, err)
		}
	}

	// 测试用加密材料在运行时现生成：只用于让内核的 PEM / 密钥解析路径走通，
	// 与任何真实服务无关，也就不需要在仓库里存一份固定密钥。
	overrides := protocolOverrides(t)

	var nodes []config.CustomNode
	for _, p := range nodespec.Protocols() {
		cfg := minimalNodeConfig(p)
		for key, value := range overrides[p.Type] {
			cfg[key] = value
		}
		nodes = append(nodes, config.CustomNode{Name: "N-" + p.Type, Type: p.Type, Config: cfg})
	}

	oldTarget := config.ConfigTarget
	config.ConfigTarget = filepath.Join(dir, "config.yaml")
	defer func() { config.ConfigTarget = oldTarget }()

	if err := GenerateCustomConfig(config.SubscribeConfig{
		ProxyPort:   7890,
		PanelPort:   9090,
		Mode:        config.ModeCustom,
		CustomNodes: nodes,
	}); err != nil {
		t.Fatalf("生成配置失败: %v", err)
	}

	out, err := exec.Command(coreBin, "-t", "-d", dir, "-f", config.ConfigTarget).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "test is successful") {
		t.Fatalf("内核拒绝加载全协议配置: %v\n%s", err, out)
	}
}

// protocolOverrides 把「必填字段」补成内核能真正解析的取值：占位符只能骗过必填校验，
// 过不了解析——UUID、带宽、组网 URI、密钥格式（PEM / base64）都必须真实可用。
func protocolOverrides(t *testing.T) map[string]map[string]any {
	t.Helper()

	wgKey := randomBase64(t, 32)
	ecPriv, ecPub := testECKeyPair(t)
	realityKey := testRealityPublicKey(t)

	// 传输层与 TLS 的分支也一并覆盖：ws / grpc / h2 选项块、REALITY、ECH、
	// ShadowTLS、证书指纹、Snell 混淆、Shadowsocks 插件——这些字段的类型或嵌套结构
	// 写错时，只有真实内核才会报出来。
	return map[string]map[string]any{
		"http":   {"server": "example.com", "port": 8080, "tls": true, "sni": "example.com", "headers": "X-Test: 1"},
		"socks5": {"server": "example.com", "port": 1080},
		"ss": {
			"server": "example.com", "port": 8388, "password": "pw",
			"plugin": "obfs", "plugin-opts": "mode: tls\nhost: bing.com",
			"client-fingerprint": "chrome",
		},
		"ssr": {"server": "example.com", "port": 8388, "password": "pw", "cipher": "aes-256-cfb"},
		"snell": {
			"server": "example.com", "port": 8388, "psk": "psk",
			"obfs-opts": map[string]any{"mode": "tls", "host": "bing.com"},
		},
		"vmess": {
			"server": "example.com", "port": 443, "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
			"tls": true, "servername": "cdn.example.com", "alpn": "h2,http/1.1",
			"client-fingerprint": "chrome", "skip-cert-verify": true,
			"network": "ws",
			"ws-opts": map[string]any{"path": "/video", "headers": "Host: cdn.example.com", "max-early-data": 2048},
		},
		"vless": {
			"server": "example.com", "port": 443, "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
			"tls": true, "servername": "cdn.example.com", "network": "grpc",
			"grpc-opts":    map[string]any{"grpc-service-name": "grpc-svc", "max-connections": 4},
			"reality-opts": map[string]any{"public-key": realityKey, "short-id": "0123456789abcdef"},
		},
		"trojan": {
			"server": "example.com", "port": 443, "password": "pw",
			"sni": "cdn.example.com", "alpn": "h2", "network": "ws",
			"ws-opts":         map[string]any{"path": "/trojan", "headers": "Host: cdn.example.com"},
			"shadow-tls-opts": map[string]any{"password": "stls", "version": 3},
			"ech-opts":        map[string]any{"enable": true, "query-server-name": "cloudflare-ech.com"},
		},
		"anytls":      {"server": "example.com", "port": 443, "password": "pw", "sni": "cdn.example.com", "alpn": "h2"},
		"ssh":         {"server": "example.com", "port": 22, "username": "u", "password": "pw"},
		"mieru":       {"server": "example.com", "port": 8964, "username": "u", "password": "pw", "transport": "TCP"},
		"sudoku":      {"server": "example.com", "port": 443, "key": "k"},
		"hysteria":    {"server": "example.com", "port": 443, "up": "30 Mbps", "down": "200 Mbps", "sni": "cdn.example.com", "alpn": "h3"},
		"hysteria2":   {"server": "example.com", "port": 443, "password": "pw", "sni": "cdn.example.com", "alpn": "h3", "skip-cert-verify": true},
		"tuic":        {"server": "example.com", "port": 443, "token": "t", "sni": "cdn.example.com", "alpn": "h3"},
		"shadowquic":  {"server": "example.com", "port": 443, "password": "pw", "sni": "cdn.example.com", "alpn": "h3"},
		"wireguard":   {"server": "example.com", "port": 51820, "ip": "172.16.0.2", "private-key": wgKey, "public-key": wgKey},
		"masque":      {"server": "example.com", "port": 443, "ip": "172.16.0.2/32", "private-key": ecPriv, "public-key": ecPub, "network": "h2", "sni": "cdn.example.com"},
		"trusttunnel": {"server": "example.com", "port": 443, "password": "pw", "sni": "cdn.example.com", "alpn": "h2"},
		"tailscale":   {"auth-key": "tskey-auth-k1234567890"},
		"zerotier":    {"network": "8056c2e21c000001"},
		"easytier":    {"network-name": "net1", "network-secret": "sec", "peers": []any{"tcp://192.0.2.10:11010"}},
		// OpenVPN 二选一：这里走用户名认证，所以清掉占位符填上的 cert / key
		"openvpn": {"server": "example.com", "port": 1194, "ca": testCAPEM(t), "username": "u", "password": "pw", "cert": "", "key": ""},
	}
}

// minimalNodeConfig 构造协议的最小可用节点：先取默认值，再补必填项与 RequireAny 的首个组合。
func minimalNodeConfig(p nodespec.Protocol) map[string]any {
	cfg := map[string]any{}
	for _, f := range p.Fields {
		if f.Default != nil {
			cfg[f.Key] = f.Default
		}
	}
	for _, f := range p.Fields {
		if f.Required {
			cfg[f.Key] = sampleByKind(f)
		}
	}
	if len(p.RequireAny) > 0 {
		for _, key := range p.RequireAny[0] {
			if f, ok := findFieldByKey(p, key); ok {
				cfg[f.Key] = sampleByKind(f)
			}
		}
	}
	return cfg
}

// sampleByKind 按字段类型给出一个合法占位取值（再由 protocolOverrides 修成可解析的取值）。
func sampleByKind(f nodespec.Field) any {
	switch f.Kind {
	case nodespec.KindInt:
		return 443
	case nodespec.KindBool:
		return true
	case nodespec.KindList:
		return []any{"x"}
	case nodespec.KindSelect:
		if len(f.Options) > 0 {
			return f.Options[0]
		}
		return "x"
	default:
		return "x"
	}
}

// findFieldByKey 在协议字段表中按键查找。
func findFieldByKey(p nodespec.Protocol, key string) (nodespec.Field, bool) {
	for _, f := range p.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return nodespec.Field{}, false
}

// testRealityPublicKey 生成一个真实的 X25519 公钥，按 REALITY 要求的
// 无填充 URL-safe base64 编码（内核会校验长度与编码方式）。
func testRealityPublicKey(t *testing.T) string {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 X25519 密钥失败: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
}

// randomBase64 生成 n 字节随机数据的 base64（WireGuard 密钥格式）。
func randomBase64(t *testing.T, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("生成随机密钥失败: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// testECKeyPair 生成一对 P-256 密钥，按 MASQUE 需要的格式编码：
// 私钥为 SEC1 DER 的 base64，公钥为 PKIX DER 的 base64。
func testECKeyPair(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成 EC 密钥失败: %v", err)
	}
	privDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("编码 EC 私钥失败: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("编码 EC 公钥失败: %v", err)
	}
	return base64.StdEncoding.EncodeToString(privDER), base64.StdEncoding.EncodeToString(pubDER)
}

// testCAPEM 生成一张自签 CA 证书（OpenVPN 的 ca 字段要求是 PEM）。
func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成 CA 密钥失败: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Fluxor Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发测试 CA 失败: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
