package dashapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 这几个接口都会让内核立刻产生副作用（下载 GEO 数据库、清 DNS / FakeIP 缓存），
// 因此必须校验方法：否则一个 GET 就能触发，配合「面板自身无 CSRF 防护」，任意站点
// 用 <img src=...> 即可反复触发。方法校验发生在任何副作用之前，因此本用例不需要内核。
func TestSideEffectingHandlersRejectWrongMethod(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
	}{
		{"GEO 更新不接受 GET", HandleConfigsGeo, http.MethodGet},
		{"GEO 回退接口不接受 GET", HandleProvidersGeo, http.MethodGet},
		{"FakeIP 清空不接受 GET", HandleFlushFakeIP, http.MethodGet},
		{"DNS 清空不接受 GET", HandleFlushDNS, http.MethodGet},
		{"DNS 查询不接受 POST", HandleDNSQuery, http.MethodPost},
		{"DNS 查询不接受 DELETE", HandleDNSQuery, http.MethodDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, httptest.NewRequest(tc.method, "/app/Fluxor/probe", nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("状态码 = %d，期望 405（%s 不得触发副作用）", rec.Code, tc.method)
			}
		})
	}
}

// 正确方法必须放行到「内核调用」这一步：没有内核 socket 时表现为 502，
// 而不是 405——据此可以确认校验没有把正常调用一起挡掉。
func TestSideEffectingHandlersAllowCorrectMethod(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
	}{
		{"GEO 更新接受 POST", HandleConfigsGeo, http.MethodPost},
		{"GEO 回退接口接受 POST", HandleProvidersGeo, http.MethodPost},
		{"FakeIP 清空接受 POST", HandleFlushFakeIP, http.MethodPost},
		{"DNS 清空接受 POST", HandleFlushDNS, http.MethodPost},
		{"DNS 查询接受 GET", HandleDNSQuery, http.MethodGet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, httptest.NewRequest(tc.method, "/app/Fluxor/probe?name=a.example&type=A", nil))
			if rec.Code == http.StatusMethodNotAllowed {
				t.Fatalf("%s 是正确方法，不该回 405", tc.method)
			}
		})
	}
}
