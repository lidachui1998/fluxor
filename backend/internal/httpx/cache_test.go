package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCacheMiddlewares 覆盖两档缓存策略：中间件必须只设置 Cache-Control，
// 不改写状态码与响应体（静态资源的 404/304 等语义由内层 FileServer 决定）。
func TestCacheMiddlewares(t *testing.T) {
	cases := []struct {
		name     string
		wrap     func(http.Handler) http.Handler
		wantCC   string
		wantBody string
	}{
		{"immutable", CacheImmutable, CacheControlImmutable, "bundle"},
		{"revalidate", CacheRevalidate, CacheControlRevalidate, "bundle"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTeapot)
				w.Write([]byte(c.wantBody))
			})
			w := httptest.NewRecorder()
			c.wrap(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/app/Fluxor/assets/index-abc123.js", nil))

			if got := w.Header().Get("Cache-Control"); got != c.wantCC {
				t.Fatalf("Cache-Control = %q, 期望 %q", got, c.wantCC)
			}
			if w.Code != http.StatusTeapot {
				t.Fatalf("状态码被改写: %d", w.Code)
			}
			if got := w.Body.String(); got != c.wantBody {
				t.Fatalf("响应体被改写: %q", got)
			}
		})
	}
}

// TestCacheImmutableIsPublic 守住强缓存的可用前提：必须可被共享缓存（代理）
// 存储，且带 immutable，否则中间反代每层都会回源。
func TestCacheImmutableIsPublic(t *testing.T) {
	w := httptest.NewRecorder()
	CacheImmutable(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/app/Fluxor/assets/index-abc123.js", nil))

	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
