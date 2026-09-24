package wsproxy

import (
	"net/http/httptest"
	"testing"
)

// 本文件守住一条线上踩过的坑：**反向代理会剥掉 Host 的端口**，
// 而浏览器的 Origin 带端口。逐一比较「主机:端口」会把正常部署整体拒掉。
//
// 实测（fnOS 应用网关，面板挂在 https://<host>:3333 之后，来自面板日志）：
//
//	origin="https://fn.1kw.site:3333" host="fn.1kw.site"
//	err=websocket: request origin not allowed by Upgrader.CheckOrigin
//
// 表现是四路 WebSocket 同时 403 —— 面板上「流量计、内存占用、连接、日志」全部为空，
// 而其它走 HTTP 的功能（内核版本、规则、订阅）一切正常，很容易误判成后端不通。

func TestCheckSameOrigin(t *testing.T) {
	// 用例只覆盖第 2 层（不设 Sec-Fetch-Site），即老浏览器/非浏览器客户端的退化路径
	cases := []struct {
		name   string
		origin string
		host   string
		xfh    string
		want   bool
	}{
		{"无 Origin：非浏览器客户端放行", "", "fn.1kw.site", "", true},
		{"两端完全一致", "https://fn.1kw.site", "fn.1kw.site", "", true},
		// ↓ 线上真实组合：反代剥掉 Host 的端口，此前被误拒
		{"反代剥掉 Host 端口（Origin 带端口）", "https://fn.1kw.site:3333", "fn.1kw.site", "", true},
		{"两端都带端口", "http://192.168.1.10:5666", "192.168.1.10:5666", "", true},
		{"Host 带端口而 Origin 不带", "http://nas.local", "nas.local:5666", "", true},
		{"大小写不敏感", "https://FN.1KW.SITE:3333", "fn.1kw.site", "", true},
		{"IPv6 字面量", "http://[::1]:5666", "[::1]:5666", "", true},
		// ↓ 真正要防的：跨站来源
		{"跨站来源必须拒绝", "https://evil.example", "fn.1kw.site", "", false},
		{"同主机不同名（子域）也要拒绝", "https://a.fn.1kw.site", "fn.1kw.site", "", false},
		{"Origin: null 拒绝", "null", "fn.1kw.site", "", false},
		{"Origin 非法拒绝", "http://[::1", "fn.1kw.site", "", false},
		// 反代改写成上游名时靠 X-Forwarded-Host
		{"Host 被改写成 localhost，靠 X-Forwarded-Host", "https://fn.1kw.site:3333", "localhost", "fn.1kw.site", true},
		{"X-Forwarded-Host 多级链取第一个", "https://fn.1kw.site", "localhost", "fn.1kw.site, inner.local", true},
		{"X-Forwarded-Host 也不匹配则拒绝", "https://evil.example", "localhost", "fn.1kw.site", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/app/Fluxor/traffic", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.xfh != "" {
				req.Header.Set("X-Forwarded-Host", tc.xfh)
			}
			if got := checkSameOrigin(req); got != tc.want {
				t.Fatalf("checkSameOrigin(origin=%q host=%q xfh=%q) = %v, 期望 %v",
					tc.origin, tc.host, tc.xfh, got, tc.want)
			}
		})
	}
}

// TestCheckSameOriginSecFetchSite Sec-Fetch-Site 由浏览器按请求自身推导，
// 不受反代改写 Host 影响，因此即使 Host 与 Origin 完全对不上也应给出正确结论。
func TestCheckSameOriginSecFetchSite(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"same-origin", true},
		{"same-site", true},
		{"none", true},
		{"cross-site", false},
		{"SAME-ORIGIN", true}, // 大小写不敏感
		{" cross-site ", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			// Host 与 Origin 故意对不上（模拟反代改写成上游名且无 X-Forwarded-Host）
			req := httptest.NewRequest("GET", "/app/Fluxor/logs", nil)
			req.Header.Set("Origin", "https://panel.example:3333")
			req.Host = "localhost"
			req.Header.Set("Sec-Fetch-Site", tc.value)
			if got := checkSameOrigin(req); got != tc.want {
				t.Fatalf("Sec-Fetch-Site=%q 时 = %v, 期望 %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestCheckSameOriginCrossSiteWinsOverMatchingHost 浏览器明确报告跨站时，
// 即使主机名恰好相同也必须拒绝（同主机不同端口承载的恶意页面）。
func TestCheckSameOriginCrossSiteWinsOverMatchingHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/app/Fluxor/logs", nil)
	req.Header.Set("Origin", "https://fn.1kw.site:3333")
	req.Host = "fn.1kw.site"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if checkSameOrigin(req) {
		t.Fatal("Sec-Fetch-Site: cross-site 必须拒绝")
	}
}

// TestCheckSameOriginExplicitAllowList 显式放行清单能救回「Host 被改成上游名、
// 且不透传 X-Forwarded-Host」的部署。
func TestCheckSameOriginExplicitAllowList(t *testing.T) {
	prev := allowedOrigins
	t.Cleanup(func() { allowedOrigins = prev })

	// 未设置清单时：无从判定 → 拒绝
	allowedOrigins = nil
	req := httptest.NewRequest("GET", "/app/Fluxor/traffic", nil)
	req.Header.Set("Origin", "https://panel.example:3333")
	req.Host = "localhost"
	if checkSameOrigin(req) {
		t.Fatal("无从判定时应拒绝")
	}

	// 显式放行后通过
	allowedOrigins = parseAllowedOrigins("panel.example")
	req2 := httptest.NewRequest("GET", "/app/Fluxor/traffic", nil)
	req2.Header.Set("Origin", "https://panel.example:3333")
	req2.Host = "localhost"
	if !checkSameOrigin(req2) {
		t.Fatal("显式放行清单命中时应通过")
	}

	// 通配符
	allowedOrigins = parseAllowedOrigins("*")
	req3 := httptest.NewRequest("GET", "/app/Fluxor/traffic", nil)
	req3.Header.Set("Origin", "https://evil.example")
	req3.Host = "localhost"
	if !checkSameOrigin(req3) {
		t.Fatal("通配符应放行一切来源")
	}

	// 清单是「额外放行」而非白名单：未命中时仍走自动判定
	allowedOrigins = parseAllowedOrigins("other.example")
	req4 := httptest.NewRequest("GET", "/app/Fluxor/traffic", nil)
	req4.Header.Set("Origin", "https://fn.1kw.site:3333")
	req4.Host = "fn.1kw.site"
	if !checkSameOrigin(req4) {
		t.Fatal("清单未命中不应阻断自动判定（同主机名应通过）")
	}
}

// TestUpgraderUsesCheckSameOrigin 防止有人再把这道校验改回「放行一切」。
func TestUpgraderUsesCheckSameOrigin(t *testing.T) {
	if upgrader.CheckOrigin == nil {
		t.Fatal("CheckOrigin 为 nil 时会退回 gorilla 的同源校验，但本项目要求显式实现，勿置空")
	}
	req := httptest.NewRequest("GET", "/app/Fluxor/logs", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Host = "fn.1kw.site"
	if upgrader.CheckOrigin(req) {
		t.Fatal("upgrader 不得放行跨站来源")
	}
}
