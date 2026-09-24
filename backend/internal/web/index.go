package web

import (
	"fluxor/internal/config"
	"fluxor/internal/httpx"
	"html/template"
	"net/http"
	"strings"
)

// HandleIndex 渲染主页单页应用
func HandleIndex(w http.ResponseWriter, r *http.Request) {
	expected := config.BaseURL
	if expected == "" {
		expected = "/"
	}
	if r.URL.Path != expected && r.URL.Path != expected+"/" {
		if !(expected == "/" && r.URL.Path == "/") {
			http.NotFound(w, r)
			return
		}
	}
	if IndexTmpl == nil {
		http.Error(w, "主页模板未加载", http.StatusInternalServerError)
		return
	}

	// 构造 baseHref（用于 <base> 标签，保证以 / 开头和结尾）
	baseHref := config.BaseURL
	if baseHref == "" {
		baseHref = "/"
	} else {
		if !strings.HasPrefix(baseHref, "/") {
			baseHref = "/" + baseHref
		}
		if !strings.HasSuffix(baseHref, "/") {
			baseHref += "/"
		}
	}
	// rawBase 保留原始值，用于 window.BASE_URL
	rawBase := config.OriginalBaseURL

	data := map[string]string{
		"BaseHref": baseHref,
		"RawBase":  rawBase,
	}
	// 入口 HTML 不缓存：它决定本次加载去取哪一份带哈希名的资源。若浏览器留下
	// 旧副本，重新部署后仍会按旧 <script> 去取（旧资源本身还有一年 immutable
	// 缓存），页面能开却永远停在上一个版本。响应体不足 1 kB，回源代价可忽略。
	w.Header().Set("Cache-Control", httpx.CacheControlRevalidate)
	IndexTmpl.Execute(w, data)
}

// IndexTmpl 由 main 在启动时从内嵌文件系统解析的 index.html 模板。
var IndexTmpl *template.Template
