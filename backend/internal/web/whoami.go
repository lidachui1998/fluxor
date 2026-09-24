package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

// maxUsernameLen 展示用用户名的长度上限。
//
// 这个值只服务于「欢迎回来，X」这一句提示，没有任何鉴权含义，过长的输入除了把界面
// 撑坏之外没有别的可能。
const maxUsernameLen = 64

// HandleWhoAmI 返回当前用户信息（从 X-Trim-Username 请求头读取）。
//
// ⚠️ 这不是身份，**绝不能拿它做鉴权或授权判断**：请求头完全由客户端自填，任何人都能
// 声明自己是别人。它唯一的用途是门户（fnOS / trim）已经做过一次登录后，把用户名透传给
// 面板显示一句欢迎语；面板自身没有任何认证（见 AGENTS 里关于监听地址与反代的说明，
// 访问控制由「只监听回环 / 反代」承担）。
//
// 这里仍做一次净化：去掉控制字符、压缩空白、限长。不是为了防注入（前端是 Vue 插值，
// 本来就转义），而是避免一个构造出来的超长/多行用户名把提示条撑坏——这类"看着像 bug
// 的显示异常"排查起来很费时间。
func HandleWhoAmI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	username := sanitizeUsername(r.Header.Get("X-Trim-Username"))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"username": username})
}

// sanitizeUsername 净化展示用用户名：去掉控制字符与首尾空白、合并内部连续空白、限长。
func sanitizeUsername(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		// 控制字符（含 \r\n\t 与各类不可见字符）一律丢弃：它们只可能被用来伪造换行或
		// 撑乱排版，用户名里没有任何合法用途
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)

	cleaned = strings.Join(strings.Fields(cleaned), " ")
	// 按 rune 截断，避免把一个多字节字符切成半个而产出乱码
	if runes := []rune(cleaned); len(runes) > maxUsernameLen {
		cleaned = string(runes[:maxUsernameLen])
	}
	return cleaned
}
