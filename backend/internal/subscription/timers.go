package subscription

import (
	"context"
	"fluxor/internal/config"
	"fluxor/internal/core"
	"fluxor/internal/logx"
	"sync"
	"time"
)

var (
	timerCancel map[string]context.CancelFunc
	timerMu     sync.RWMutex
)

func init() {
	timerCancel = make(map[string]context.CancelFunc)
}

// startSubscriptionTimer 为指定订阅启动定时更新。
//
// 接收订阅快照与下标（而非 *config.SubscribeConfig + 全局锁），
// 调用方 StartAllTimers 负责在锁内取好快照并判定 mode == "switch"。
func startSubscriptionTimer(subs []config.Subscription, idx int) {
	if idx < 0 || idx >= len(subs) {
		return
	}
	sub := subs[idx]
	if sub.UpdateInterval <= 0 {
		return
	}

	timerMu.Lock()
	defer timerMu.Unlock()

	// 取消旧定时器
	if cancel, ok := timerCancel[sub.Name]; ok {
		cancel()
		delete(timerCancel, sub.Name)
	}

	ctx, cancel := context.WithCancel(context.Background())
	timerCancel[sub.Name] = cancel

	go func(name string, interval int) {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 先在锁内做前置判断（模式仍为 switch、订阅仍存在），随即放锁。
				// updateSubscriptionInSwitchMode 会发起最长数十秒的网络下载，
				// 绝不能持 config.Mu 调用它——该锁被 core.CoreRequest、wsproxy、
				// quality、tproxy 等 10 处读取点共用，持锁下载会阻塞整个面板。
				config.Mu.RLock()
				if config.Current.Mode != "switch" {
					config.Mu.RUnlock()
					return
				}
				found := false
				for i := range config.Current.Subscriptions {
					if config.Current.Subscriptions[i].Name == name {
						found = true
						break
					}
				}
				config.Mu.RUnlock()
				if !found {
					return
				}

				needsReload, err := updateSubscriptionInSwitchMode(name)
				// 执行更新（订阅元数据已由 updateSubscriptionInSwitchMode 写入
				// subscription-meta.json，此处无需再落库）
				if err != nil {
					logx.Error(logx.ModuleSub, "scheduled update of subscription %q failed: %v", name, err)
				} else {
					if needsReload {
						if err := core.ReloadCore(); err != nil {
							logx.Error(logx.ModuleSub, "reloading core after scheduled update failed: %v", err)
						}
					}
				}
			}
		}
	}(sub.Name, sub.UpdateInterval)
}

// StartAllTimers 按当前模式启动所有定时任务：
// switch 模式下为每个订阅启动更新定时器，并额外启动健康检查定时器。
func StartAllTimers() {
	// 先取快照再释放锁：startSubscriptionTimer / startHealthCheckTimer 会获取
	// 其它锁，若在此持锁调用，就与并发的保存请求形成「读锁重入 + 排队写者」的
	// 死锁——Go 的 RWMutex 在有写者排队时会阻塞新的读锁，于是内层 RLock 永久
	// 等待外层释放，而外层又在等内层返回。
	config.Mu.RLock()
	mode := config.Current.Mode
	activeSub := config.Current.ActiveSubscription
	subs := make([]config.Subscription, len(config.Current.Subscriptions))
	copy(subs, config.Current.Subscriptions)
	config.Mu.RUnlock()

	if mode != "switch" {
		return
	}
	for i := range subs {
		startSubscriptionTimer(subs, i)
	}
	startHealthCheckTimer(activeSub)
}

// StopAllTimers 停止所有定时器
func StopAllTimers() {
	timerMu.Lock()
	defer timerMu.Unlock()
	for name, cancel := range timerCancel {
		cancel()
		delete(timerCancel, name)
	}
	stopHealthCheckTimer()
}
