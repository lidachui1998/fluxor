package core

import (
	"encoding/json"
	"fluxor/internal/logx"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// coreEventHub 维护所有 SSE 订阅者，并在内核运行状态变化时向它们广播。
//
// 采用「最新状态即真相」的模型：hub 不排队历史事件，只保存当前已知状态；
// 新订阅者接入时立即收到一次当前状态（快照），之后仅在状态真正发生变化时
// 收到增量推送。这样：
//   - 前端无需轮询即可保持同步；
//   - 浏览器 EventSource 自动重连后立即校正状态，不会漏事件。
//
// 本推送**只承载运行状态**。内核版本是内核原生 API（`/version`）的职责，
// 由前端按需直接请求，不经此处转发——避免在状态通道里夹带会变化的业务数据，
// 也避免 hub 为了取版本而引入网络 IO 与它带来的失败分支。
//
// 两把锁的分工：
//   - pubMu 串行化 publish，保证事件顺序（并发启停时不会乱序）；
//   - mu 仅保护状态字段与订阅者集合。
type coreEventHubT struct {
	pubMu       sync.Mutex
	mu          sync.Mutex
	subscribers map[chan coreStateEvent]struct{}
	known       bool // known 为 false 表示尚未确定过状态
	running     bool
}

// coreStateEvent 是推送给前端的事件负载。
type coreStateEvent struct {
	Running bool  `json:"running"`
	TS      int64 `json:"ts"` // 事件时间戳（毫秒）
}

var coreEventHub = &coreEventHubT{subscribers: make(map[chan coreStateEvent]struct{})}

// subscriberBuffer 每个订阅者的事件通道缓冲深度。
//
// 状态事件只在「真正变化」时产生，正常情况下一条连接整个生命周期也收不到几条；
// 缓冲的意义仅在于「发布路径永不阻塞」。再大的缓冲也没有额外收益——前端只关心最新状态。
const subscriberBuffer = 4

// subscribe 注册订阅者，返回事件通道与注销函数。
//
// 注册与「接入快照」在同一个临界区内完成，注销与「关闭通道」也在同一个临界区内完成——
// 这两点都是必需的：
//   - 快照与注册分开时，订阅者可能先收到增量事件、再收到一个更旧的快照，把状态写反；
//   - 关闭通道若发生在锁外，publish 就可能在「取出订阅者」与「投递」之间撞上 close，
//     向已关闭的通道发送会 panic（`default` 分支挡不住 panic，它只处理「无人接收」）。
//     该 panic 一旦落在 lifecycle 里等待内核退出的那个 goroutine（非 HTTP handler，
//     没有 net/http 的 recover 兜底）就会直接终止整个面板进程。
func (h *coreEventHubT) subscribe() (<-chan coreStateEvent, func()) {
	ch := make(chan coreStateEvent, subscriberBuffer)

	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	// 接入即下发当前已知状态；通道满则丢弃快照，前端仍可由 GET /core/status 兜底
	if h.known {
		select {
		case ch <- coreStateEvent{Running: h.running, TS: time.Now().UnixMilli()}:
		default:
		}
	}
	h.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subscribers, ch)
			// close 与 publish 的发送共用 h.mu，因此不存在「向已关闭通道发送」的窗口
			close(ch)
			h.mu.Unlock()
		})
	}
	return ch, unsubscribe
}

// publish 记录状态，并在其发生变化时广播。
//
// 仅当「首次确定状态」或「状态值与上次不同」时才推送，因此可安全地多次调用
// （例如 StartCore 与 StopCore 都会广播，但不会产生重复事件）。
//
// 投递在 h.mu 内完成（而非先取快照再锁外发送）：发送一律是非阻塞的 select，
// 临界区长度只与订阅者数量成正比，不会因为某个慢客户端而阻塞；换来的是与
// unsubscribe 的 close 互斥，杜绝向已关闭通道发送的 panic。
func (h *coreEventHubT) publish(running bool) {
	// 串行化整个发布过程，避免并发启停时事件乱序
	h.pubMu.Lock()
	defer h.pubMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.known && h.running == running {
		return // 无变化，不广播
	}
	h.known = true
	h.running = running

	ev := coreStateEvent{Running: running, TS: time.Now().UnixMilli()}
	for ch := range h.subscribers {
		select {
		case ch <- ev:
		default:
			// 订阅者消费过慢：丢弃本条。其重连或下次状态变化时会重新校正，
			// 且 GET /core/status 始终可用，不会造成状态永久错乱。
		}
	}
}

// PublishCoreState 供 lifecycle / handlers 在状态变化后调用。
func PublishCoreState(running bool) {
	coreEventHub.publish(running)
}

// HandleCoreEvents 通过 SSE 推送内核运行状态变化（GET /core/events）。
//
// 协议要点：
//   - Content-Type: text/event-stream，每条事件以 "\n\n" 结束；
//   - 每 25 秒发送一次 ": ping" 注释心跳，防止中间代理因空闲而断开连接；
//   - 客户端断开（r.Context() 完成）时注销订阅者并退出；
//   - 发送 X-Accel-Buffering: no，提示 nginx 等反代不要缓冲本响应。
func HandleCoreEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		// 无法流式输出（自定义 ResponseWriter）。返回错误，前端会退回
		// GET /core/status 的静态结果。
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // 关键：阻止 nginx 缓冲 SSE

	events, unsubscribe := coreEventHub.subscribe()
	defer unsubscribe()

	// 立即写出注释行，促使客户端尽快确认连接、并让代理开始透传
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				logx.Warn(logx.ModuleCore, "failed to encode core state event for SSE client: %v", err)
				continue
			}
			// 事件名固定为 core-state，便于前端 addEventListener 精确订阅
			if _, err := fmt.Fprintf(w, "event: core-state\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
