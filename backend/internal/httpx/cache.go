package httpx

import "net/http"

// 静态资源缓存策略（两个取值都写在这里，避免字面量散落到各 handler）
const (
	// CacheControlImmutable 供「内容寻址」的资源使用：文件名内含内容哈希，
	// 内容一改文件名就变，旧副本永远不会再被新的入口 HTML 引用。
	CacheControlImmutable = "public, max-age=31536000, immutable"

	// CacheControlRevalidate 供「每次都必须回源校验」的响应使用：
	// 入口 HTML（随部署变化，决定切到哪一份带哈希的资源）与名字固定、
	// 内容会变的静态文件（如 ICON.PNG）。
	//
	// 注意：内嵌文件系统的文件 ModTime 为零，http.ServeContent 既不发
	// Last-Modified 也不生成 ETag，因此这里的 no-cache 实际等价于每次
	// 完整重下——HTML 不足 1 kB，代价可以忽略。
	CacheControlRevalidate = "no-cache"
)

// CacheImmutable 为带内容哈希的静态资源设置一年期强缓存。
//
// 只可用于 Vite 产物这类「文件名随内容变化」的文件（frontend/dist/assets/ 下
// 全部带 8 位内容哈希）；若套在名字固定的文件上，用户将永远取不到更新后的内容。
//
// 将来若在本层之上再加压缩中间件，记得同时补 Vary: Accept-Encoding——
// 否则带 immutable 的响应会把某一种编码固化一年。
func CacheImmutable(next http.Handler) http.Handler {
	return withCacheControl(CacheControlImmutable, next)
}

// CacheRevalidate 要求浏览器每次使用本地副本前回源校验。
func CacheRevalidate(next http.Handler) http.Handler {
	return withCacheControl(CacheControlRevalidate, next)
}

func withCacheControl(value string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 在 next 写入响应头之前设置，next 内部若显式设置同名字段仍以它为准
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}
