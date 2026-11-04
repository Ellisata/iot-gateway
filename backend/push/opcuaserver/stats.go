// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcuaserver

import (
	"sort"
	"time"

	"github.com/gopcua/opcua/ua"

	"iot-gateway/push"
)

// staleAfter 点位多久没有新值算「过期」。
//
// 采集间隔因设备/采集组而异，通道侧拿不到统一口径，因此用一个足够宽松的
// 常量：超过 5 分钟没更新基本可以确定不是正常轮询，而是设备停采或点位被停用。
const staleAfter = 5 * time.Minute

// pointStats 统计点位覆盖度。points 建好后只读，值走原子指针，
// 因此遍历不需要锁，也不会与 apply/客户端读互相阻塞。
func (st *serverState) pointStats(now time.Time) (withData, badQuality, stale int, oldest time.Duration) {
	for _, p := range st.points {
		dv := p.val.Load()
		if dv == nil || dv.Status == ua.StatusBadWaitingForInitialData {
			// 还没收到过采集数据：不算「已更新」，也不算 Bad 质量。
			continue
		}
		withData++
		if dv.Status >= ua.StatusBad {
			badQuality++
		}
		if dv.ServerTimestamp.IsZero() {
			continue
		}
		age := now.Sub(dv.ServerTimestamp)
		if age < 0 {
			age = 0
		}
		if age > oldest {
			oldest = age
		}
		if age > staleAfter {
			stale++
		}
	}
	return withData, badQuality, stale, oldest
}

// clientStats 返回当前订阅数与监控项数。
//
// gopcua v0.9.1 不暴露会话/连接计数（sessionBroker、channelBroker 都是私有字段），
// 但 SubscriptionService / MonitoredItemService 是导出的，且都带导出的互斥锁，
// 持锁读 len 即可安全拿到「有没有客户端在订阅」这一层信息。
func (st *serverState) clientStats() (subscriptions, monitoredItems int) {
	if st.srv.SubscriptionService != nil {
		st.srv.SubscriptionService.Mu.Lock()
		subscriptions = len(st.srv.SubscriptionService.Subs)
		st.srv.SubscriptionService.Mu.Unlock()
	}
	if st.srv.MonitoredItemService != nil {
		st.srv.MonitoredItemService.Mu.Lock()
		monitoredItems = len(st.srv.MonitoredItemService.Items)
		st.srv.MonitoredItemService.Mu.Unlock()
	}
	return subscriptions, monitoredItems
}

// endpointURLs 返回去重后的可用端点，供排障时直接复制给 UaExpert。
func (st *serverState) endpointURLs() []string {
	seen := make(map[string]struct{})
	urls := make([]string, 0, 4)
	for _, ep := range st.srv.Endpoints() {
		if _, ok := seen[ep.EndpointURL]; ok {
			continue
		}
		seen[ep.EndpointURL] = struct{}{}
		urls = append(urls, ep.EndpointURL)
	}
	sort.Strings(urls)
	return urls
}

// statusExtra 组装 OPC UA 特有的监控项，写入 ChannelStatusVO.Extra。
func (st *serverState) statusExtra(rebuilds uint64, lastRebuild time.Time) map[string]any {
	now := time.Now()
	withData, badQuality, stale, oldest := st.pointStats(now)
	subscriptions, monitoredItems := st.clientStats()

	return map[string]any{
		"addressSpaceDevices": st.deviceCount,
		"points":              len(st.points),
		"pointsWithData":      withData,
		"pointsBadQuality":    badQuality,
		"pointsStale":         stale,
		"oldestValueAgeSec":   int64(oldest.Seconds()),
		"subscriptions":       subscriptions,
		"monitoredItems":      monitoredItems,
		"rebuildCount":        rebuilds,
		"lastRebuildTime":     push.FormatTime(lastRebuild),
		"endpoints":           st.endpointURLs(),
	}
}
