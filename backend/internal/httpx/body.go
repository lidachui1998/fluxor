package httpx

import (
	"fmt"
	"io"
)

// 上游响应体的默认读取上限。
//
// 存在的意义不是业务校验，而是「别被对方决定我们的内存占用」：所有上游（机场订阅、
// IP 归属地接口、元数据接口、内核 socket）都是外部或可被外部影响的数据源，一个被投毒
// 或被劫持的响应可以把面板直接读到 OOM，而这类故障在日志里表现为进程无故重启。
const (
	// MaxUpstreamBody 通用上游响应上限（元数据、IP 查询、内核小接口）。
	MaxUpstreamBody int64 = 1 << 20 // 1 MiB
	// MaxSubscriptionBody 订阅文件上限：机场下发的节点列表可能确实很大，
	// 留出远超常规的量级，只拦住「明显不是配置文件」的响应。
	MaxSubscriptionBody int64 = 32 << 20 // 32 MiB
)

// ReadAllLimited 读取 r 的全部内容，但绝不超过 max 字节。
//
// 与 io.ReadAll 的区别：先按 max+1 截断，一旦读满就判定超限并返回错误，因此内存占用
// 有硬上限，超限的响应不会先被完整读进内存再判断。错误信息里带上 max 便于排查
// （是上游返回了异常大的内容，还是上限设置得不合理）。
func ReadAllLimited(r io.Reader, max int64) ([]byte, error) {
	// 多读 1 字节：正好等于 max 时无法区分「刚好」与「更多」
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("响应体超过上限 %d 字节，已中止读取", max)
	}
	return data, nil
}
