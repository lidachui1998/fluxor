package core

import (
	"sync"
	"testing"
	"time"
)

// TestEventHubSubscribeUnsubscribeRaceWithPublish 守住 SSE hub 的一条硬约束：
// **向订阅者投递事件与注销（关闭通道）之间不能有竞争**。
//
// 回归背景：原实现是「在 h.mu 内取订阅者快照 → 放锁 → 在锁外投递」，而注销路径的
// close(ch) 也在锁外。两者之间就出现了窗口：publish 已经拿到通道准备发送，另一个
// goroutine 恰好注销并 close 掉它 —— 向已关闭通道发送会 panic，而 `select` 的
// default 分支挡不住 panic（它只处理「无人接收」）。
//
// 爆炸半径不止一条连接：StartCore 等待内核退出的那个 goroutine 会调用
// PublishCoreState(false)，它不在 HTTP handler 里，没有 net/http 的 recover 兜底，
// 一旦 panic 就是整个面板进程退出——而「内核自行退出」正是 SSE 要覆盖的场景。
//
// 这个用例把订阅/退订与状态翻转放在一起高频竞争；配合 -race 使用。
func TestEventHubSubscribeUnsubscribeRaceWithPublish(t *testing.T) {
	hub := &coreEventHubT{subscribers: make(map[chan coreStateEvent]struct{})}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 发布者：每次翻转状态，保证 publish 真的会走到投递分支（状态无变化会提前返回）
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			hub.publish(i%2 == 0)
		}
	}()

	// 并发订阅者：反复「接入 → 立刻注销」，最大化与 publish 的重叠窗口
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ch, unsubscribe := hub.subscribe()
				// 顺手把已投递的事件读掉，模拟前端消费
				select {
				case <-ch:
				default:
				}
				unsubscribe()
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	// 全部注销后订阅者表应回到空
	hub.mu.Lock()
	left := len(hub.subscribers)
	hub.mu.Unlock()
	if left != 0 {
		t.Fatalf("全部注销后订阅者表应为空，实际剩 %d 个", left)
	}
}

// TestEventHubUnsubscribeIsIdempotent 重复注销不得重复 close（会 panic）。
func TestEventHubUnsubscribeIsIdempotent(t *testing.T) {
	hub := &coreEventHubT{subscribers: make(map[chan coreStateEvent]struct{})}

	ch, unsubscribe := hub.subscribe()
	hub.publish(true)
	select {
	case ev := <-ch:
		if !ev.Running {
			t.Fatalf("接入快照/增量事件应报告 running=true")
		}
	case <-time.After(time.Second):
		t.Fatal("publish 后应收到事件")
	}

	unsubscribe()
	unsubscribe() // 第二次不得 panic
	unsubscribe()

	// 注销后通道已关闭
	if _, open := <-ch; open {
		t.Fatal("注销后通道应已关闭")
	}
}

// TestEventHubPublishRepeatsAreSuppressed 状态未变化时不重复推送。
func TestEventHubPublishRepeatsAreSuppressed(t *testing.T) {
	hub := &coreEventHubT{subscribers: make(map[chan coreStateEvent]struct{})}
	ch, unsubscribe := hub.subscribe()
	defer unsubscribe()

	hub.publish(true)  // 首次确定状态：推
	hub.publish(true)  // 无变化：不推
	hub.publish(false) // 变化：推

	got := 0
	for {
		select {
		case <-ch:
			got++
			continue
		default:
		}
		break
	}
	// 接入时 unknown → 不发快照；因此这里应为 2 条（true、false）
	if got != 2 {
		t.Fatalf("事件条数 = %d，期望 2（状态由未知→true→false）", got)
	}
}
