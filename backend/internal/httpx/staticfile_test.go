package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// ETagFile 的存在理由是「内嵌文件系统没有 ModTime/ETag」：没有它，配 no-cache 的
// 80 KB favicon 每次导航都要完整重下。这里把协商行为固定下来。

func newIconHandler(t *testing.T, body string) (http.Handler, string) {
	t.Helper()
	fsys := fstest.MapFS{"ICON.PNG": &fstest.MapFile{Data: []byte(body)}}
	h, err := ETagFile(fsys, "ICON.PNG", "image/png")
	if err != nil {
		t.Fatalf("ETagFile: %v", err)
	}
	return h, body
}

func TestETagFileServesContentWithValidator(t *testing.T) {
	h, body := newIconHandler(t, "png-bytes")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ICON.PNG", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if rec.Body.String() != body {
		t.Fatalf("响应体 = %q，期望 %q", rec.Body.String(), body)
	}
	if got := rec.Header().Get("ETag"); got == "" {
		t.Fatal("必须带 ETag，否则 no-cache 等于每次完整重下")
	}
	if got := rec.Header().Get("Cache-Control"); got != CacheControlRevalidate {
		t.Fatalf("Cache-Control = %q，期望 %q（文件名固定，不能强缓存）", got, CacheControlRevalidate)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Last-Modified"); got != "" {
		t.Fatalf("内嵌文件不该伪造 Last-Modified，实际 %q", got)
	}
}

func TestETagFileReturns304OnMatch(t *testing.T) {
	h, _ := newIconHandler(t, "png-bytes")

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/ICON.PNG", nil))
	etag := first.Header().Get("ETag")

	req := httptest.NewRequest(http.MethodGet, "/ICON.PNG", nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	h.ServeHTTP(second, req)

	if second.Code != http.StatusNotModified {
		t.Fatalf("命中 If-None-Match 应回 304，实际 %d", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 不应带响应体，实际 %d 字节", second.Body.Len())
	}
}

func TestETagChangesWithContent(t *testing.T) {
	h1, _ := newIconHandler(t, "old-icon")
	h2, _ := newIconHandler(t, "new-icon")

	rec1 := httptest.NewRecorder()
	h1.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/ICON.PNG", nil))
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/ICON.PNG", nil))

	e1, e2 := rec1.Header().Get("ETag"), rec2.Header().Get("ETag")
	if e1 == e2 {
		t.Fatalf("内容变化后 ETag 必须变化（否则部署后浏览器永远用旧图标）: %q", e1)
	}

	// 旧 ETag 在新内容上不应再命中 304
	req := httptest.NewRequest(http.MethodGet, "/ICON.PNG", nil)
	req.Header.Set("If-None-Match", e1)
	rec3 := httptest.NewRecorder()
	h2.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusOK {
		t.Fatalf("旧 ETag 不应命中新内容，实际 %d", rec3.Code)
	}
}

func TestETagFileSupportsRange(t *testing.T) {
	h, _ := newIconHandler(t, "0123456789")

	req := httptest.NewRequest(http.MethodGet, "/ICON.PNG", nil)
	req.Header.Set("Range", "bytes=0-2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("Range 请求应回 206，实际 %d", rec.Code)
	}
	if rec.Body.String() != "012" {
		t.Fatalf("Range 响应体 = %q，期望 012", rec.Body.String())
	}
}

func TestETagFileFailsOnMissingFile(t *testing.T) {
	fsys := fstest.MapFS{"other.txt": &fstest.MapFile{Data: []byte("x")}}
	if _, err := ETagFile(fsys, "ICON.PNG", "image/png"); err == nil {
		t.Fatal("文件缺失时应在启动阶段就报错，而不是等到请求时才 500")
	}
}
