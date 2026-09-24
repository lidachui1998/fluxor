package config

import (
	"sync"
)

var (
	// Current 当前生效的订阅配置快照，读写必须通过 Mu 保护。
	Current SubscribeConfig
	// Mu 保护 Current 的读写锁。
	Mu sync.RWMutex
)

// CurrentSnapshot 返回 Current 的值拷贝（持读锁）。
//
// 所有「在锁外使用 Current」的地方都必须走这里。直接读全局变量会与 assembleCurrent 的
// 「Mu.Lock(); Current = cfg」构成数据竞争（`go test -race` 可复现），而且可能读到
// 组装到一半的中间态——订阅表与规则/隧道来自不同的 store，中途读到的是「新订阅名 +
// 旧规则」这种不存在的组合，拿它生成配置就会产出错乱的结果。
//
// 返回值的切片/map 与 Current 共享底层数组（值拷贝不深拷贝容器），但各 store 在写入时
// 都是整份替换而非就地修改，因此副本可以安全地在锁外读取。调用方不得就地修改它们。
func CurrentSnapshot() SubscribeConfig {
	Mu.RLock()
	defer Mu.RUnlock()
	return Current
}
