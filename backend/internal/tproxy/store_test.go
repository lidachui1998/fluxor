package tproxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fluxor/internal/config"
)

// useTempTproxyFile 把 tproxy.json 指到临时目录，并清理包级缓存。
//
// 载入后缓存里是「生效列表」（nil 会被还原成预填模板），因此用例结束时必须复位，
// 否则会污染同包其它用例（rules_test.go 依赖默认家族定义与解析结果）。
func useTempTproxyFile(t *testing.T) string {
	t.Helper()
	prevDataDir, prevFile := config.FluxorDataDir, config.FluxorTproxyFile
	dir := t.TempDir()
	config.SetDataDir(dir)
	t.Cleanup(func() {
		config.FluxorDataDir, config.FluxorTproxyFile = prevDataDir, prevFile
		exceptionsMu.Lock()
		tproxyDstExceptionsCache = nil
		tproxySrcExceptionsCache = nil
		exceptionsMu.Unlock()
	})
	return dir
}

// TestDefaultBypassTemplateNotPersisted 预填模板不落盘，只有用户改过才写文件。
//
// 旧实现把 20+ 行中文注释模板写进用户配置，空配置的文件体积九成都来自这里；
// 现在 nil 表示「用代码里的默认值」，文件里不出现这两个键。
func TestDefaultBypassTemplateNotPersisted(t *testing.T) {
	dir := useTempTproxyFile(t)

	LoadTproxyState()

	// 生效列表（内存缓存）必须是预填模板
	if got := dstExceptions(); len(got) != len(defaultDstExceptions()) {
		t.Fatalf("未自定义时应使用预填模板，实际 %d 条", len(got))
	}
	// 此时不该生成任何文件（只有开关状态默认值，也不需要落盘）
	if _, err := os.Stat(filepath.Join(dir, "tproxy.json")); !os.IsNotExist(err) {
		t.Fatalf("默认值不应落盘: %v", err)
	}
	if !proxyLocalEnabled() {
		t.Fatal("本机流量接管默认应为开启")
	}

	// 用户「恢复默认」= 保存一份与模板一致的列表 → 仍然不落盘
	if err := SaveTproxyDstExceptions(defaultDstExceptions()); err != nil {
		t.Fatalf("SaveTproxyDstExceptions: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tproxy.json"))
	if err != nil {
		t.Fatalf("保存开关后应生成文件: %v", err)
	}
	if strings.Contains(string(data), "dst_exceptions") {
		t.Fatalf("与预填模板一致的列表不应落盘: %s", string(data))
	}

	// 自定义列表则必须落盘并可重新载入
	custom := []string{"1.1.1.1", "8.8.8.8"}
	if err := SaveTproxyDstExceptions(custom); err != nil {
		t.Fatalf("SaveTproxyDstExceptions: %v", err)
	}
	LoadTproxyState() // 重新载入，验证自定义列表确实来自文件
	got := dstExceptions()
	if len(got) != len(custom) || got[0] != custom[0] {
		t.Fatalf("自定义列表未能往返: %v", got)
	}
}

// TestLegacyDefaultTemplateIsDropped 迁移过来的旧默认模板在载入时被移除（自动瘦身）。
func TestLegacyDefaultTemplateIsDropped(t *testing.T) {
	dir := useTempTproxyFile(t)

	legacy := config.TproxyFile{
		ProxyLocal:    true,
		DstExceptions: defaultDstExceptions(),
		SrcExceptions: defaultSrcExceptions(),
	}
	writeTproxyFile(t, dir, legacy)

	LoadTproxyState()

	data, err := os.ReadFile(filepath.Join(dir, "tproxy.json"))
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if strings.Contains(string(data), "dst_exceptions") || strings.Contains(string(data), "src_exceptions") {
		t.Fatalf("旧文件里的默认模板应被移除: %s", string(data))
	}
	if len(dstExceptions()) != len(defaultDstExceptions()) {
		t.Fatal("移除后生效列表应仍是默认模板")
	}
}

// TestSwitchesRoundTrip 三个开关能落盘并重新载入。
func TestSwitchesRoundTrip(t *testing.T) {
	dir := useTempTproxyFile(t)

	LoadTproxyState()
	if err := SaveTproxyProxyLocal(false); err != nil {
		t.Fatalf("SaveTproxyProxyLocal: %v", err)
	}
	if err := SaveTproxyIPv6(true); err != nil {
		t.Fatalf("SaveTproxyIPv6: %v", err)
	}
	SetTproxyEnabled(true)

	// 清掉缓存后重新载入，验证确实来自文件
	exceptionsMu.Lock()
	tproxyProxyLocal, tproxyIPv6 = true, false
	exceptionsMu.Unlock()

	LoadTproxyState()
	if proxyLocalEnabled() {
		t.Fatal("proxy_local 未持久化")
	}
	if !ipv6Enabled() {
		t.Fatal("ipv6 未持久化")
	}
	if !LoadTproxyEnabled() {
		t.Fatal("enabled 未持久化")
	}
	_ = dir
}

func writeTproxyFile(t *testing.T, dir string, v config.TproxyFile) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tproxy.json"), data, 0644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
}
