package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// HandleWhoAmI 的身份来自客户端自填请求头，只用于显示欢迎语。这里把「不得用于鉴权」
// 这条约束之外的行为固定下来：净化不改变正常用户名，但会挡掉控制字符与超长输入。
func TestSanitizeUsername(t *testing.T) {
	long := strings.Repeat("a", maxUsernameLen+20)
	longWide := strings.Repeat("中", maxUsernameLen+20)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"普通用户名原样保留", "alice", "alice"},
		{"中文用户名原样保留", "张三", "张三"},
		{"首尾空白去掉", "  bob  ", "bob"},
		{"内部连续空白合并", "bob\t\t  smith", "bob smith"},
		{"控制字符被丢弃（防伪造换行）", "bob\r\nAdmin", "bobAdmin"},
		{"空值保持为空", "", ""},
		{"只有空白视为空", "   ", ""},
		{"超长按 rune 截断", long, strings.Repeat("a", maxUsernameLen)},
		{"多字节超长按 rune 截断且不产生乱码", longWide, strings.Repeat("中", maxUsernameLen)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeUsername(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeUsername(%q) = %q，期望 %q", tc.in, got, tc.want)
			}
			if !utf8Valid(got) {
				t.Fatalf("截断后不应产生非法 UTF-8: %q", got)
			}
		})
	}
}

// utf8Valid 用最小改动判定合法性（避免为了一个断言引入额外依赖）。
func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

func TestHandleWhoAmI(t *testing.T) {
	// 方法校验
	rec := httptest.NewRecorder()
	HandleWhoAmI(rec, httptest.NewRequest(http.MethodPost, "/whoami", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("非 GET 应回 405，实际 %d", rec.Code)
	}

	// 正常返回净化后的用户名
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.Header.Set("X-Trim-Username", "  alice\r\n ")
	HandleWhoAmI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["username"] != "alice" {
		t.Fatalf("username = %q，期望 alice", body["username"])
	}

	// 缺头时返回空串（前端据此不显示欢迎语）
	rec = httptest.NewRecorder()
	HandleWhoAmI(rec, httptest.NewRequest(http.MethodGet, "/whoami", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["username"] != "" {
		t.Fatalf("缺头时 username 应为空串，实际 %q", body["username"])
	}
}
