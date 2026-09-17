// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mqtt

import (
	"fmt"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"gorm.io/gorm"

	"iot-gateway/logger"
	"iot-gateway/model/po"
	"iot-gateway/push"
	"iot-gateway/utils"
)

// init 向 push 包注册 mqtt 通道工厂。
// 依赖入口处（main.go）对本包的空导入触发，Engine 的 buildChannels 自动发现。
func init() {
	push.Register("mqtt", func(db *gorm.DB, ch *po.PushChannel) (push.Channel, error) {
		return newMqttChannel(db, ch)
	})
}

const (
	// publishTimeout 单次发布等待完成的最长时间（防止断连期间 QoS1 token 悬挂）
	publishTimeout = 3 * time.Second
	// testConnectTimeout 连通性测试等待上限（略大于 buildClientOptions 的 5s 连接超时，
	// 保证连接尝试先出确定结果再判超时，避免误报）
	testConnectTimeout = 6 * time.Second
	// defaultPublishWorkers 并发发布 worker 数默认值（配置缺省/非法时回落）
	defaultPublishWorkers = 4
	// maxPublishWorkers 发布 worker 数上限，防止配置异常导致 goroutine 数量失控
	maxPublishWorkers = 64

	// defaultSpoolMaxBatches 断网本地缓存最大批次上限默认值；
	// 达到后裁剪最旧批次以约束磁盘占用。
	defaultSpoolMaxBatches = 100000

	// clientIDBaseCap 基础前缀截断长度：拼接随机后缀后整体控制在 23 字节内，
	// 兼容 MQTT 3.1 常见的 clientId 长度上限（超长会被 broker 拒连）。
	clientIDBaseCap = 12
	// clientIDSuffixLen 运行时追加的随机后缀长度（hex 字符数）。
	// 实际 clientId = 配置值前缀 + 随机后缀，保证本实例/本通道唯一，
	// 多实例或残留进程不再因同 clientId 被 broker 会话接管踢断。
	clientIDSuffixLen = 10
)

// drainPolicy mqtt 的补发失败策略：不判毒、不窄化。
//
// 发布失败大多是 broker 慢导致的等待超时，而非数据被拒，无限重试比误删更安全；
// 队头长期发不出去（如主题配置为空导致 buildTopics 返回空）由缓存上限的裁剪兜底。
var drainPolicy = push.DrainPolicy{}

// mqttChannel 单个 MQTT 推送通道的运行时，实现 push.Channel。
//
// 每个通道独立 outbox（有界缓冲）+ N 个发布 worker：
//   - 采集线程经 push.Engine 非阻塞入队，outbox 满时丢弃最旧；
//   - N 个发布 worker（默认 4，可配置）仅在连接成功后开始消费 outbox，
//     共享同一连接并发 Publish，paho 内部流水线写帧（不等 ACK），
//     吞吐 ≈ N × 单 worker 串行发布速率，broker 慢/跨广域时不再轻易丢最旧；
//   - 断连/内存队列满的批次落入 SQLite 本地缓存（push_outbox，断网缓存），
//     重连后按入队序补发、发布成功删除 —— 网络中断期间采集数据不丢失；
//   - 重连由 paho 的 AutoReconnect / ConnectRetry 负责。
//
// 排序保证：批内记录顺序保持（单次 marshal + 单次 Publish）；跨批可能乱序，
// 但每条点位携带 collectedAt（毫秒级时间戳），下游按时间兜底排序。
//
// 队列、断网缓存、补发与状态统计由 push.Outbox 统一提供（见 push/outbox.go），
// 本通道只负责 MQTT 连接维护、主题路由与载荷序列化。
type mqttChannel struct {
	id   string
	name string
	cfg  *mqttConfig
	sig  string // 配置签名，用于热加载判断通道配置是否变化

	// clientID 实际使用的 clientId：配置值前缀 + 通道级随机后缀（见 resolveClientID）。
	// 新建通道/重启进程即换新值，从根上避免多实例同 clientId 被 broker 会话接管踢断。
	clientID string

	ob *push.Outbox

	// done 由 run 关闭：发布 worker 与补发协程全部退出。
	done chan struct{}

	mu        sync.RWMutex
	running   bool
	connected bool
}

// ChannelID 返回通道持久化 ID（实现 push.Channel）
func (ch *mqttChannel) ChannelID() string { return ch.id }

// ConfigSig 返回配置签名，用于热加载判断配置是否变化（实现 push.Channel）
func (ch *mqttChannel) ConfigSig() string { return ch.sig }

// Connected 返回当前连接状态（实现 push.Connectivity，供 Outbox 决定入队路由）
func (ch *mqttChannel) Connected() bool { return ch.isConnected() }

// Enqueue 非阻塞入队，绝不阻塞采集线程（实现 push.Channel）。
// 入队路由（直投内存队列 / 落盘 / 丢最旧）由 push.Outbox 统一决定。
func (ch *mqttChannel) Enqueue(b push.PushBatch) { ch.ob.Enqueue(b) }

// newMqttChannel 根据推送通道配置创建通道运行时（未启动）。
// db 用于构建断网本地缓存（spoolEnabled 时）。
func newMqttChannel(db *gorm.DB, ch *po.PushChannel) (*mqttChannel, error) {
	cfg, err := parseConfig(ch.ConfigJSON)
	if err != nil {
		return nil, err
	}
	mch := &mqttChannel{
		id:       ch.ID,
		name:     ch.Name,
		cfg:      cfg,
		sig:      ch.ID + "|" + ch.Name + "|" + ch.ConfigJSON,
		clientID: resolveClientID(cfg.ClientID),
	}
	mch.ob = push.NewOutbox(push.OutboxConfig{
		Tag:          "mqtt",
		ID:           ch.ID,
		Name:         ch.Name,
		DB:           db,
		SpoolEnabled: cfg.spoolEnabled(),
		SpoolCap:     cfg.spoolBatchCap(),
		Conn:         mch,
	})
	return mch, nil
}

// TestConnectivity 同步建立一次连接验证 broker 可达与鉴权，成功后立即断开。
// 测试模式关闭自动重连/重试（否则连接失败会无限重试挂住测试），
// 不启动通道后台 goroutine，用于管理界面保存配置前的连通性校验。
func (ch *mqttChannel) TestConnectivity() error {
	opts := ch.buildClientOptions()
	opts.SetAutoReconnect(false)
	opts.SetConnectRetry(false)

	client := mqtt.NewClient(opts)
	token := client.Connect()
	defer client.Disconnect(0)
	if !token.WaitTimeout(testConnectTimeout) {
		return fmt.Errorf("mqtt: connect timed out after %s", testConnectTimeout)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: connect failed: %w", err)
	}
	return nil
}

// Start 启动通道的发布 goroutine
func (ch *mqttChannel) Start() {
	ch.mu.Lock()
	if ch.running {
		ch.mu.Unlock()
		return
	}
	ch.running = true
	ch.done = make(chan struct{})
	ch.mu.Unlock()

	ch.ob.Start()
	go ch.run()
}

// Stop 停止通道，等待发布 goroutine 退出
func (ch *mqttChannel) Stop() {
	ch.mu.Lock()
	if !ch.running {
		ch.mu.Unlock()
		return
	}
	ch.running = false
	ch.mu.Unlock()

	// 顺序有约束：先触发停止，再等 worker 组退出（停服前还会把 outbox 中发失败的
	// 批次转投缓存），最后才等落盘协程排空 —— 否则在途批次会随进程退出而丢失。
	ch.ob.Close()
	<-ch.done
	ch.ob.Wait()
}

// run 通道主循环：等待首次连接成功后，按配置起 N 个发布 worker 与补发协程并发消费
func (ch *mqttChannel) run() {
	defer close(ch.done)

	client := mqtt.NewClient(ch.buildClientOptions())

	token := client.Connect()
	if !ch.waitFirstConnect(token) {
		// 停止或连接中止：直接退出
		client.Disconnect(0)
		return
	}

	var wg sync.WaitGroup
	for i := 0; i < ch.cfg.publishWorkers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.publishLoop(client)
		}()
	}
	// 断网缓存的补发协程：连接成功后按入队序回放未发完的批次
	if ch.ob.SpoolEnabled() {
		drain := func(pend []push.SpoolBatch, _ bool) (bool, int64) {
			// 本通道逐批发布，天然就是「单批一组」，无需窄化
			return ch.drainWindow(client, pend)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch.ob.DrainLoop(drain, drainPolicy)
		}()
	}
	// 所有 worker 退出（quit 关闭）后断开连接
	wg.Wait()
	client.Disconnect(250)
}

// publishLoop 单个发布 worker：循环消费 outbox，直至通道停止。
// 发布失败（断连/超时等）时将该批次转入本地缓存，重连后补发，避免数据丢失。
func (ch *mqttChannel) publishLoop(client mqtt.Client) {
	for {
		select {
		case <-ch.ob.Quit():
			return
		case b := <-ch.ob.Batches():
			if !ch.publish(client, b) && ch.ob.SpoolEnabled() {
				ch.ob.SpoolEnqueue(b)
			}
		}
	}
}

// waitFirstConnect 阻塞等待首次连接成功，同时监听停止信号。
//
// paho 开启 ConnectRetry 后，Connect 返回的 token 仅在连接成功或 Disconnect
// 后被终止时完成，因此不能裸 Wait()，需轮询以保证停止时能及时退出。
// 返回 false 表示已停止/连接被中止。
func (ch *mqttChannel) waitFirstConnect(token mqtt.Token) bool {
	for {
		select {
		case <-ch.ob.Quit():
			return false
		case <-time.After(200 * time.Millisecond):
			if token.WaitTimeout(50 * time.Millisecond) {
				if token.Error() != nil {
					// 连接被中止（如 Stop 触发 Disconnect）
					errMsg := token.Error().Error()
					logger.Error("%s", errMsg)
					ch.setConnected(false)
					ch.ob.SetLastErr(errMsg)
					return false
				}
				ch.setConnected(true)
				ch.ob.SetLastErr("")
				return true
			}
		}
	}
}

// publish 向配置的所有主题发布一批数据。
// 返回是否全部主题发布成功（断连/超时/错误均视为失败），
// 供补发协程决定该批次是删除还是保留重试。
func (ch *mqttChannel) publish(client mqtt.Client, b push.PushBatch) bool {
	if client == nil || !client.IsConnected() {
		ch.ob.Drop(1)
		return false
	}

	topics := buildTopics(ch.cfg.Topic, b.DeviceID)
	if len(topics) == 0 {
		ch.ob.Drop(1)
		return false
	}

	payload, err := marshalBatch(b.Records, b.CollectedAt)
	if err != nil {
		errMsg := fmt.Sprintf("marshal batch: %v", err)
		logger.Error("%s", errMsg)
		ch.ob.SetLastErr(errMsg)
		ch.ob.Drop(1)
		return false
	}

	ok := true
	for _, topic := range topics {
		tok := client.Publish(topic, ch.cfg.QoS, false, payload)
		if !tok.WaitTimeout(publishTimeout) {
			errMsg := fmt.Sprintf("publish %s timed out", topic)
			logger.Error("%s", errMsg)
			ch.ob.SetLastErr(errMsg)
			ch.ob.Drop(1)
			ok = false
			continue
		}
		if err := tok.Error(); err != nil {
			errMsg := fmt.Sprintf("publish %s: %v", topic, err)
			logger.Error("%s", errMsg)
			ch.ob.SetLastErr(errMsg)
			ch.ob.Drop(1)
			ok = false
			continue
		}
		ch.ob.MarkPublished(1)
	}
	return ok
}

// drainWindow 按入队序逐批发布窗口内批次，成功批次收集后合并删除（单条 DELETE）；
// 任一发布失败即停止，保留该批及后续待重连重试。
// 返回是否本窗口全部发布成功，以及首个发布失败的批次 ID（全成功为 0）。
func (ch *mqttChannel) drainWindow(client mqtt.Client, pend []push.SpoolBatch) (bool, int64) {
	successIDs := make([]int64, 0, len(pend))
	var failID int64
	for _, p := range pend {
		batch := push.PushBatch{DeviceID: p.DeviceID, CollectedAt: p.CollectedAt, Records: p.Records}
		if !ch.publish(client, batch) {
			failID = p.ID
			break
		}
		successIDs = append(successIDs, p.ID)
	}
	if len(successIDs) > 0 {
		if err := ch.ob.Spool().DeleteBatch(successIDs); err != nil {
			logger.Error("mqtt: spool delete batch failed (n=%d): %v", len(successIDs), err)
		}
	}
	return len(successIDs) == len(pend), failID
}

// isConnected 返回当前连接状态（由 paho 连接/断开回调维护）
func (ch *mqttChannel) isConnected() bool {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.connected
}

// Snapshot 返回通道状态快照（用于状态查询）
func (ch *mqttChannel) Snapshot() push.ChannelStatusVO {
	vo := push.ChannelStatusVO{
		Type:   "mqtt",
		Broker: ch.cfg.brokerURL(),
		Topic:  ch.cfg.Topic,
	}
	ch.ob.FillStatus(&vo)
	return vo
}

func (ch *mqttChannel) setConnected(ok bool) {
	ch.mu.Lock()
	ch.connected = ok
	ch.mu.Unlock()
}

// resolveClientID 生成实际使用的 clientId：配置值为可辨识前缀 + 通道级随机后缀，
// 新建通道/重启进程即得到唯一值，多实例或残留进程无法再因同 clientId 互相踢断。
// 基础前缀截断至 clientIDBaseCap，加后缀后总长 23 字节，兼容 MQTT 3.1 的 clientId 长度上限。
func resolveClientID(base string) string {
	if base == "" {
		base = "iot-gateway"
	}
	if len(base) > clientIDBaseCap {
		base = base[:clientIDBaseCap]
	}
	return base + "-" + utils.GenerateUUID()[:clientIDSuffixLen]
}

// buildClientOptions 构建 paho 客户端选项
func (ch *mqttChannel) buildClientOptions() *mqtt.ClientOptions {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(ch.cfg.brokerURL())
	opts.SetClientID(ch.clientID)
	if ch.cfg.Username != "" {
		opts.SetUsername(ch.cfg.Username)
		opts.SetPassword(ch.cfg.Password)
	}
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetMaxReconnectInterval(30 * time.Second)
	opts.SetOrderMatters(false)

	opts.SetOnConnectHandler(func(mqtt.Client) {
		ch.setConnected(true)
		ch.ob.SetLastErr("")
		ch.ob.Notify() // 重连后可能积压断连期间落盘的批次，即时唤醒补发
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		errMsg := err.Error()
		logger.Error("%s", errMsg)
		ch.setConnected(false)
		ch.ob.SetLastErr(errMsg)
	})
	opts.SetReconnectingHandler(func(_ mqtt.Client, _ *mqtt.ClientOptions) {
		ch.setConnected(false)
	})
	return opts
}
