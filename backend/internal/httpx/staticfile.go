package httpx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"time"
)

// ETagFile 为「名字固定、内容会随部署变化」的内嵌静态文件提供强校验器（ETag）。
//
// 为什么需要它：内嵌文件系统（//go:embed）里的文件 ModTime 恒为零值，http.FileServer /
// http.ServeContent 因此既不发 Last-Modified 也不生成 ETag。只靠 Cache-Control: no-cache
// 做条件请求，浏览器每次都要**完整重下**——对不足 1 kB 的入口 HTML 无所谓，但 favicon
// ICON.PNG 有 80 kB，每次导航都白传一遍。
//
// 内容在启动时读入内存并算出 ETag（内嵌文件本来就在二进制里，不额外占资源）；
// 随后交给 http.ServeContent 处理，它会依据我们设好的 ETag 自行完成
// If-None-Match / If-Range 协商与 Range 请求，命中时回 304，不带响应体。
//
// 仍然必须每次回源校验（no-cache）而不能强缓存：文件名固定，内容随部署变化，
// 强缓存会让用户永远取到旧图标。
func ETagFile(fsys fs.FS, name, contentType string) (http.Handler, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	// 取 sha256 前 16 字节（128 位）做 ETag：碰撞概率可忽略，且比全量摘要短得多
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("ETag", etag)
		h.Set("Cache-Control", CacheControlRevalidate)
		if contentType != "" {
			h.Set("Content-Type", contentType)
		}
		// ModTime 传零值：内容不变就不该有"修改时间"这回事，它由 ETag 表达。
		// ServeContent 见到已设好的 ETag，会自己处理 If-None-Match（命中回 304）。
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	}), nil
}
