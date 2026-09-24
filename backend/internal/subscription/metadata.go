package subscription

import (
	"encoding/json"
	"fluxor/internal/config"
	"fluxor/internal/core"
	"fluxor/internal/httpx"
	"fluxor/internal/logx"
	"fluxor/internal/subscription/download"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// fetchSubscriptionMetadataFromCore 从主内核获取订阅元数据
func fetchSubscriptionMetadataFromCore(subName string) (updatedAt string, subInfo map[string]interface{}, err error) {
	encoded := url.QueryEscape(subName)
	resp, err := core.CoreRequest("GET", "/providers/proxies/"+encoded, nil)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("状态码: %d", resp.StatusCode)
	}
	body, err := httpx.ReadAllLimited(resp.Body, httpx.MaxUpstreamBody)
	if err != nil {
		return "", nil, err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", nil, err
	}
	updatedAtVal, _ := data["updatedAt"].(string)
	subInfoVal, _ := data["subscriptionInfo"].(map[string]interface{})
	// 规范化键为小写
	subInfoVal = download.NormalizeMapKeys(subInfoVal)
	return updatedAtVal, subInfoVal, nil
}

// updateAllSubscriptionsMetadata 更新所有订阅的元数据（仅用于融合模式）。
//
// 采用「锁外抓取、逐个落库」：抓取阶段含重试与 500ms 退避，绝不能持 config.Mu
// 进行（会长时间阻塞 /subscribe/config 等读者）；元数据落库交给
// config.SaveSubscriptionMeta（只写 subscription-meta.json，并在内部重组装
// Current，因此不得在持 config.Mu 时调用）。入参 cfg 是本次请求的本地视图，抓到的
// 结果同时写回它的 Subscriptions 供本次生成/响应使用——不再就地改写 config.Current。
func updateAllSubscriptionsMetadata(cfg *config.SubscribeConfig) {
	// 1. 锁外抓取
	type metaResult struct {
		name string
		// ok 表示本次确实抓到了新元数据：只有它为 true 才落库
		// （抓取失败的订阅保持 store 里的旧值，不做同值回写——那会在并发下
		//  把别人刚写入的新值覆盖回去）。
		ok        bool
		updatedAt string
		subInfo   map[string]interface{}
	}
	results := make([]metaResult, 0, len(cfg.Subscriptions))

	for i := range cfg.Subscriptions {
		name := cfg.Subscriptions[i].Name
		var updatedAt string
		var subInfo map[string]interface{}
		var err error

		// 2. 重试机制：最多尝试 3 次，每次间隔 500ms
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				time.Sleep(500 * time.Millisecond)
			}
			updatedAt, subInfo, err = fetchSubscriptionMetadataFromCore(name)
			if err == nil {
				break
			}
			logx.Warn(logx.ModuleSub, "fetching metadata for subscription %q failed (attempt %d/%d): %v", name, attempt+1, 3, err)
		}

		// 「拿到内容」才叫成功：内核 provider 存在但还没拉取到 subscription-userinfo 时
		// 会返回 200 + 空内容，这与抓取失败一样不该覆盖已存下的元数据，也不该报「已更新」
		ok := err == nil && (updatedAt != "" || len(subInfo) > 0)
		if err != nil || !ok {
			// 旧元数据从 store 读（store 里仍是旧值，因此本次不写入新数据）。本地 cfg 沿用
			// 旧值，保证本次生成/响应与之一致；store 里也没有旧值时本地置空。
			oldUpdatedAt, oldInfo := config.SubscriptionMetaOf(name)
			if oldUpdatedAt == "" && oldInfo == nil {
				updatedAt, subInfo = "", nil
				logx.Warn(logx.ModuleSub, "subscription %q has no previous metadata, keeping it empty", name)
			} else {
				updatedAt, subInfo = oldUpdatedAt, oldInfo
				if err != nil {
					logx.Warn(logx.ModuleSub, "keeping previous metadata for subscription %q after fetch failure", name)
				} else {
					logx.Warn(logx.ModuleSub, "core returned no metadata for subscription %q, keeping previous", name)
				}
			}
		} else {
			logx.Info(logx.ModuleSub, "metadata for subscription %q updated", name)
		}

		results = append(results, metaResult{name: name, ok: ok, updatedAt: updatedAt, subInfo: subInfo})
	}

	// 3. 逐个落库（每个订阅一次，各自只碰自己的条目），并写回入参 cfg 的本地视图。
	// 不能再一次性写 config.Current.Subscriptions：Current 现在由各 store 组装，
	// SaveSubscriptionMeta 已经把它更新了。
	for _, r := range results {
		if r.ok {
			if err := config.SaveSubscriptionMeta(r.name, r.updatedAt, r.subInfo); err != nil {
				logx.Error(logx.ModuleSub, "saving metadata for subscription %q failed: %v", r.name, err)
			}
		}
		for i := range cfg.Subscriptions {
			if cfg.Subscriptions[i].Name == r.name {
				cfg.Subscriptions[i].UpdatedAt = r.updatedAt
				cfg.Subscriptions[i].SubscriptionInfo = r.subInfo
				break
			}
		}
	}
}
