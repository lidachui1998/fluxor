package dashapi

import (
	"fluxor/internal/config"
	"fluxor/internal/tproxy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tproxy-port 是唯一一个「面板自己也在用」的内核字段：内核按它监听，nft 规则按它重定向。
// 因此这两件事必须分别被测到：
//   - 取值校验（非法端口不能在落库前溜过去，否则内核会因为监听失败而起不来）；
//   - 「与当前取值相同」必须归一为「没有变化」，否则一次无意义的表单提交就会把防火墙
//     规则拆掉重建（TProxy 正在接管时那是一次不必要的网络瞬断）。

func TestTproxyPortFromPatch(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantPort    int
		wantChanged bool
		wantErr     bool
	}{
		{"未提及该字段", `{"mode":"rule"}`, 0, false, false},
		{"空请求体", ``, 0, false, false},
		{"不是 JSON 对象", `not json at all`, 0, false, false},
		{"合法端口", `{"tproxy-port":7898}`, 7898, true, false},
		{"0 表示禁用", `{"tproxy-port":0}`, 0, true, false},
		{"边界值 1025", `{"tproxy-port":1025}`, 1025, true, false},
		{"边界值 65535", `{"tproxy-port":65535}`, 65535, true, false},
		{"低于 1025 应拒绝", `{"tproxy-port":80}`, 0, false, true},
		{"超过 65535 应拒绝", `{"tproxy-port":70000}`, 0, false, true},
		{"负数应拒绝", `{"tproxy-port":-1}`, 0, false, true},
		{"小数应拒绝", `{"tproxy-port":7898.5}`, 0, false, true},
		{"字符串应拒绝", `{"tproxy-port":"7898"}`, 0, false, true},
		{"极大取值不得溢出成合法端口", `{"tproxy-port":1e18}`, 0, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port, changed, err := tproxyPortFromPatch([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("应报错，实际 port=%d changed=%v", port, changed)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if port != tc.wantPort || changed != tc.wantChanged {
				t.Fatalf("port=%d changed=%v，期望 port=%d changed=%v",
					port, changed, tc.wantPort, tc.wantChanged)
			}
		})
	}
}

// useTempConfigDir 把数据目录指到临时目录并载入一份种子配置。
func useTempConfigDir(t *testing.T, tproxyPort int) {
	t.Helper()
	prevDir := config.FluxorDataDir
	config.SetDataDir(t.TempDir())
	t.Cleanup(func() { config.SetDataDir(prevDir) })
	config.LoadAll()
	if err := config.SaveSettings(config.SubscribeConfig{
		ProxyPort:  7890,
		PanelPort:  9090,
		TproxyPort: tproxyPort,
		Mode:       config.ModeMerge,
		RuleGroup:  config.RuleGroupBase,
		UIPanel:    "metacubexd",
	}); err != nil {
		t.Fatalf("播种配置失败: %v", err)
	}
}

func TestPersistTproxyPortOnlyReportsRealChange(t *testing.T) {
	useTempConfigDir(t, 7898)

	// 与当前取值相同：不得报告变更（调用方据此跳过规则重建）
	prev, changed, err := persistTproxyPort(7898)
	if err != nil {
		t.Fatalf("persistTproxyPort: %v", err)
	}
	if changed {
		t.Fatal("端口未变时必须报告 changed=false，否则会白白重建一次防火墙规则")
	}
	if prev != 7898 {
		t.Fatalf("prev=%d，期望 7898", prev)
	}
	if got := config.CurrentSnapshot().TproxyPort; got != 7898 {
		t.Fatalf("端口的实际取值被改动了: %d", got)
	}

	// 真改动：报告 changed=true 并返回旧值
	prev, changed, err = persistTproxyPort(9999)
	if err != nil {
		t.Fatalf("persistTproxyPort: %v", err)
	}
	if !changed {
		t.Fatal("端口确实变化时必须报告 changed=true")
	}
	if prev != 7898 {
		t.Fatalf("prev=%d，期望 7898", prev)
	}
	if got := config.CurrentSnapshot().TproxyPort; got != 9999 {
		t.Fatalf("端口未落库: %d", got)
	}

	// 再存同值：应当再次归一为「无变化」
	if _, changed, err = persistTproxyPort(9999); err != nil {
		t.Fatalf("persistTproxyPort: %v", err)
	}
	if changed {
		t.Fatal("重复写入同一端口不应报告变更")
	}
}

func TestRollbackTproxyPort(t *testing.T) {
	useTempConfigDir(t, 7898)

	if _, _, err := persistTproxyPort(9999); err != nil {
		t.Fatalf("persistTproxyPort: %v", err)
	}
	rollbackTproxyPort(7898)
	if got := config.CurrentSnapshot().TproxyPort; got != 7898 {
		t.Fatalf("回滚后端口的实际取值 = %d，期望 7898", got)
	}

	// 已经是目标值时应为空操作（不得报错、不得改写）
	rollbackTproxyPort(7898)
	if got := config.CurrentSnapshot().TproxyPort; got != 7898 {
		t.Fatalf("空操作回滚不应改动端口: %d", got)
	}
}

// TestPatchRejectsZeroTproxyPortWhileEnabled 守卫「端口置 0 + 接管仍开着」这种状态：
// 内核不再监听该端口，而防火墙规则还在把流量导过去——那是直接断网。
//
// 用真实 handler 覆盖：请求在被拒时必须发生在任何副作用之前（不落库、不改开关）。
// 注意：本例只改内存开关与 tproxy.json，不触碰 nft（本用例不启用真实规则）。
func TestPatchRejectsZeroTproxyPortWhileEnabled(t *testing.T) {
	useTempConfigDir(t, 7898)
	tproxy.SetTproxyEnabled(true)
	t.Cleanup(func() { tproxy.SetTproxyEnabled(false) })

	req := httptest.NewRequest(http.MethodPatch, "/configs", strings.NewReader(`{"tproxy-port":0}`))
	rec := httptest.NewRecorder()
	HandleConfigsAPI(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("接管中把端口置 0 应被拒绝（400），实际 %d，body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "TPROXY") {
		t.Fatalf("错误信息应说明原因，实际: %s", rec.Body.String())
	}
	if !tproxy.GetTproxyState() {
		t.Fatal("被拒时不得改动接管开关")
	}
	if got := config.CurrentSnapshot().TproxyPort; got != 7898 {
		t.Fatalf("被拒时不得落库新端口，实际 %d", got)
	}
}

// TestPatchAllowsZeroTproxyPortWhenDisabled 接管未启用时，端口置 0 是正常操作（禁用端口）。
func TestPatchAllowsZeroTproxyPortWhenDisabled(t *testing.T) {
	useTempConfigDir(t, 7898)
	tproxy.SetTproxyEnabled(false)

	req := httptest.NewRequest(http.MethodPatch, "/configs", strings.NewReader(`{"tproxy-port":0}`))
	rec := httptest.NewRecorder()
	HandleConfigsAPI(rec, req)

	// 没有内核 socket，PATCH 内核那一步会失败并回滚端口 —— 这里要断言的是
	// **不是 400**（即没有被「接管中置 0」那条校验拦住）
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("接管未启用时端口置 0 不应被拒: %s", rec.Body.String())
	}
}
