// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"iot-gateway/configFile"
	"iot-gateway/logger"
	"iot-gateway/model/vo"
	"iot-gateway/runtimecfg"
)

const (
	// defaultPort 默认发现端口（广播与监听共用）。
	defaultPort = 9083
	// defaultInterval 默认广播间隔。
	defaultInterval = 5 * time.Second

	// probeReplyWindow 同一源地址的最小应答间隔。
	//
	// 探询是无人应答的公共入口，而回包比探询大一个数量级：不限流的话，
	// 局域网内任何主机都能用一个伪造的源地址把这里当反射放大器用。
	probeReplyWindow = time.Second
	// probeWindowTTL 限流表条目的保留时长。
	probeWindowTTL = 2 * time.Minute
	// probeWindowMax 限流表条目数上限。
	//
	// 源地址无认证即可伪造，不设上限就是一条可被远程触发的慢速内存泄漏：
	// 每换一个伪造源就多一条。满了就拒绝新条目 —— 宁可少答一次探询，
	// 也不能让伪造流量把内存吃干。
	probeWindowMax = 4096
)

// IdentitySource 网关身份来源。
//
// *service.GatewayService 满足此接口（在 wire.go 里 Bind）。声明成独立接口
// 便于单元测试注入假源，理由同 alarm.StatusSource。
type IdentitySource interface {
	GetGateway(ctx context.Context) (*vo.GatewayVO, error)
}

// ApiKeySource 默认开放接口密钥来源。
//
// 与 IdentitySource 并列而不是并进去：身份来自 iot_gateway 单行表
// （GatewayService），密钥来自 open_api_secret 表（OpenApiSecretService），
// 是两条互不相关的查询路径。合在一个接口里会让"只想测广播格式"的用例
// 被迫实现一个它根本用不到的密钥源。
type ApiKeySource interface {
	DefaultOpenApiKey(ctx context.Context) (string, error)
}

// Announcer 局域网自动发现广播器。
//
// **一个套接字干两件事**：往各网段的广播地址反复发 notify，同时收管理平台发来的
// probe 并单播回 reply。共用套接字是刻意的 —— 拆成"发的"和"收的"两个套接字后，
// 回包的源端口会变成随机值，探询方就没法把请求与应答对起来；而且只有绑定在
// 0.0.0.0 上的那个套接字才能在各张网卡上都收到探询。
//
// 生命周期照 alarm.ChannelMonitor（stopCh/doneCh + mu，Stop 幂等），但有一处
// **必须不同**：Start 是会失败的（端口占用），所以先 listen 成功、再置 stopCh ——
// 失败时 Stop 保持无操作。main.go 对 Start 的错误只记日志不 Fatal，
// 那条路径上 `defer Stop()` 仍然要安全，这一点靠的就是这个顺序。
type Announcer struct {
	cfg *configFile.Config
	src IdentitySource
	// policy 运行期配置来源。非 nil 时 enabled 以它为准（页面可动态启停）；
	// nil 时退回 cfg，测试与最小装配路径不需要额外的依赖。
	policy runtimecfg.Runtime
	keys   ApiKeySource

	// port / interval 由 Start 从配置解析（含默认值），启动后只读。
	port     int
	interval time.Duration

	// 以下两个是测试缝，默认值在 NewAnnouncer 里给，测试可直接改字段。
	// 手法同 alarm.ChannelMonitor 用未导出 wait 字段注入巡检间隔。
	//
	// 不做成 options 结构体：只有两项，而 pluginhost 的 processOptions 之所以
	// 值得那样写，是因为它有十几个字段且全部来自另一个包的配置。
	listen  func(network, address string) (net.PacketConn, error)
	targets func(port int) []target

	// mu 保护下面三个字段。Stop 会把它们一并置 nil 以支持重复调用，
	// 所以子协程**不能**直接读这些字段（读到的可能是 nil），必须在 Start 里
	// 把句柄作为参数拿进去 —— 见 run 的签名。
	mu     sync.Mutex
	conn   net.PacketConn
	stopCh chan struct{}
	doneCh chan struct{}

	// lastProbe 源 IP → 上次应答时刻，**仅 readLoop 协程读写**，故无需加锁。
	lastProbe map[string]time.Time
	// warnedNoIdentity 让"身份行缺失"只告警一次而不每轮刷屏，
	// **仅 announceLoop 协程读写**。reply 走的是另一条路径且刻意不碰它 ——
	// 见 reply 里的说明。
	warnedNoIdentity bool
}

// NewAnnouncer 创建广播器（未启动、未绑定任何端口）。
//
// 构造刻意不读配置也不失败：配置解析与端口绑定都在 Start 里做，
// 与 pluginhost.NewManager 的取舍一致。
func NewAnnouncer(cfg *configFile.Config, src IdentitySource, keys ApiKeySource) *Announcer {
	return NewAnnouncerWithPolicy(cfg, src, keys, nil)
}

// NewAnnouncerWithPolicy 同 NewAnnouncer，但 enabled 从运行期配置读取，
// 并注册启停回调供「运行开关」页动态切换。
func NewAnnouncerWithPolicy(cfg *configFile.Config, src IdentitySource, keys ApiKeySource,
	policy runtimecfg.Runtime) *Announcer {
	a := &Announcer{
		cfg:       cfg,
		src:       src,
		keys:      keys,
		policy:    policy,
		listen:    net.ListenPacket,
		targets:   lanTargets,
		lastProbe: make(map[string]time.Time),
	}
	if policy != nil {
		policy.RegisterApplier(runtimecfg.KeyDiscoveryEnabled, func(_, new any) error {
			enabled, ok := new.(bool)
			if !ok {
				return fmt.Errorf("discovery: enabled 期望 bool，得到 %T", new)
			}
			return a.ApplyEnabled(enabled)
		})
	}
	return a
}

// enabled 读当前生效的开关：运行期配置优先，否则用启动期配置。
func (a *Announcer) enabled() bool {
	if a.policy != nil {
		return a.policy.Bool(runtimecfg.KeyDiscoveryEnabled)
	}
	return a.cfg != nil && a.cfg.Discovery.Enabled
}

// ApplyEnabled 热启停广播器。Start / Stop 本身都是幂等的，因此重复调用安全。
//
// 只用于运行期切换：端口 / 间隔不在动态管理范围内 —— 它们决定套接字的绑定，
// 改了就得重新 Start，而页面上的「开关」不应该悄悄换掉发现端口。
func (a *Announcer) ApplyEnabled(enabled bool) error {
	if enabled {
		return a.Start()
	}
	a.Stop()
	return nil
}

// Start 绑定发现端口并开始广播与应答。
//
// 返回的错误只应记日志、不应中断启动（同 pluginhost.Manager.Start 的处置）：
// 发现失败不影响采集与上报，只影响管理平台能否自动找到这台网关。
//
// discovery.enabled 为 false 时完全静默：不绑定、不起协程、不打日志 ——
// 与 plugin.enabled=false 时 DriverManager.Start 的静默无操作一致。
func (a *Announcer) Start() error {
	if !a.enabled() {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopCh != nil {
		return nil // 已启动，幂等
	}

	a.port = a.cfg.Discovery.Port
	if a.port <= 0 {
		a.port = defaultPort
	}
	a.interval = time.Duration(a.cfg.Discovery.IntervalMs) * time.Millisecond
	if a.interval <= 0 {
		a.interval = defaultInterval
	}

	// 网络必须写死字面量 "udp4"，不能写 "udp"：Windows 上的 setDefaultSockopts
	// 对 AF_INET6 套接字**不设** SO_BROADCAST，双栈主机上裸 "udp" 可能落到
	// AF_INET6，之后往 IPv4 广播地址写就会失败。这是 Windows 上的硬约束。
	//
	// 反过来，IPv4 的 UDP 套接字上 Go 已默认置 SO_BROADCAST，因此这里
	// 不需要任何 raw sockopt 代码。
	//
	// 也不设 SO_REUSEADDR：同主机跑第二个网关实例就该在这里失败。两个实例
	// 共用一个接收端口会让探询应答落到不确定的那个实例上，比启动失败更糟。
	conn, err := a.listen("udp4", fmt.Sprintf(":%d", a.port))
	if err != nil {
		// 此处不解锁地 return 是安全的：defer 会解锁，且 stopCh 仍为 nil，
		// 所以随后无论在哪个分支调 Stop 都是无操作。
		return fmt.Errorf("discovery: listen udp :%d: %w", a.port, err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	a.conn = conn
	a.stopCh = stop
	a.doneCh = done

	targets := a.targets(a.port)
	if len(targets) == 0 {
		// 空目标不是错误：只有 loopback 的机器确实无处可发，但监听侧照常工作，
		// 管理平台主动探询仍能收到应答。别把这里"修"成启动失败 ——
		// 那会让一台本来能被探询发现的网关彻底消失。
		logger.Warn("discovery: no usable IPv4 interface, announce disabled but probe replies still work")
	}
	for _, t := range targets {
		logger.Info("discovery: announcing %s %s -> %s every %s", t.iface, t.ip, t.bcast, a.interval)
	}

	go a.run(stop, done, conn)
	return nil
}

// Stop 停止广播与应答，等待协程退出。未启动、启动失败、以及重复调用都是无操作。
func (a *Announcer) Stop() {
	a.mu.Lock()
	if a.stopCh == nil {
		a.mu.Unlock()
		return
	}
	conn, stop, done := a.conn, a.stopCh, a.doneCh
	// 先摘掉字段再解锁：这样第二次调用会走上一条提前返回，而不是二次 close
	// 一个已关闭的 channel（那会 panic）。Stop 可重复调用是明确契约 ——
	// main.go 的 defer 与测试都依赖它。
	//
	// 也正因如此，子协程拿到的是 Start 传进去的句柄而不是这些字段：
	// 它们若读字段，就会在 select 上等一个 nil channel 而永久阻塞。
	a.conn, a.stopCh, a.doneCh = nil, nil, nil
	a.mu.Unlock()

	close(stop)
	// 关套接字是为了把阻塞在 ReadFrom 上的读协程叫醒：只 close(stop) 的话
	// 读协程会一直卡在系统调用里，下面的 <-done 就永远等不到。
	// ReadFrom 随后返回 net.ErrClosed（见 readLoop）。
	_ = conn.Close()
	<-done
}

// run 等两个子协程都退出后再关闭 done。
//
// done 只在这里 close 一次：若让两个协程各自 `defer close(done)`，
// 第二个 close 会 panic（close of closed channel）。
func (a *Announcer) run(stop <-chan struct{}, done chan<- struct{}, conn net.PacketConn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a.announceLoop(stop, conn) }()
	go func() { defer wg.Done(); a.readLoop(conn) }()
	wg.Wait()
	close(done)
}

// announceLoop 周期广播，启动时先立即广播一次。
//
// 首轮立即发是刻意的：否则网关重启后要等满一个周期（默认 5s）才会被平台看见。
// "启动即宣告"让平台在一次报文内就发现本网关 —— 对逐台重启的批量运维，
// 这个差别是分钟级的。
func (a *Announcer) announceLoop(stop <-chan struct{}, conn net.PacketConn) {
	a.announce(conn)

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.announce(conn)
		}
	}
}

// announce 取一次身份快照，发往当前所有广播目标。
func (a *Announcer) announce(conn net.PacketConn) {
	gw, ok := a.currentIdentity()
	if !ok {
		return
	}
	port := a.announcePort(gw)

	// 每轮重新枚举网卡：插拔网线、VPN 起落都能在一个周期内自愈，
	// 代价不过是一次 net.Interfaces()（每 5 秒一次的系统调用）。
	targets := a.targets(a.port)
	if len(targets) == 0 {
		return
	}

	// 时间戳取一次给本轮所有目标共用：同一轮广播里各网段的时刻本该一致。
	ts := time.Now().UnixMilli()
	// 密钥同理取一次：本轮各网段发的是同一份内容。取不到就是空串，
	// 报文里该字段被 omitempty 略去，不影响其余字段。
	apiKey := a.defaultApiKey()

	for _, t := range targets {
		msg := Message{
			Protocol:  Protocol,
			Version:   ProtocolVersion,
			Type:      TypeNotify,
			GatewayID: gw.ID,
			Name:      gw.Name,
			// ip 填本次广播所用网卡的地址 —— "这个网段上我在哪"，
			// 与接收方从哪个网段收到这条报文正好对应。
			IP:              t.ip.String(),
			Port:            port,
			FirmwareVersion: gw.FirmwareVersion,
			Description:     gw.Description,
			ApiKey:          apiKey,
			Timestamp:       ts,
		}
		b, err := encode(msg)
		if err != nil {
			logger.Error("discovery: encode notify: %v", err)
			return
		}
		if _, err := conn.WriteTo(b, t.bcast); err != nil {
			// 单个目标失败不中断本轮：某个网段不通（例如网线拔了）不该拖累
			// 其他网段的宣告。这是可恢复的常态，故用 Debug 而不是 Warn。
			logger.Debug("discovery: notify to %s failed: %v", t.bcast, err)
		}
	}
}

// currentIdentity 读取身份快照；取不到时返回 false，本轮跳过。
//
// 每轮都重新读库而不是启动时缓存：单行表、本地 SQLite、微秒级，换来的是
// 「在系统设置页改完名称，下一个周期广播里就是新值」。缓存 TTL 只会凭空
// 买来一个失效问题，而这里要缓存的东西小到不值得。
func (a *Announcer) currentIdentity() (*vo.GatewayVO, bool) {
	gw, err := a.src.GetGateway(context.Background())
	if err != nil {
		// 身份行缺失是**合法状态**：InitDB 里播种失败是刻意非致命的
		//（见 database/sqlite/database.go），此时网关照常运行，只是没得可发布。
		// 每 5 秒刷一条 WARN 会变成稳定噪声，所以只在状态翻转时告警一次。
		if !a.warnedNoIdentity {
			a.warnedNoIdentity = true
			logger.Warn("discovery: gateway identity unavailable, announce suspended until it exists: %v", err)
		}
		return nil, false
	}
	if a.warnedNoIdentity {
		a.warnedNoIdentity = false
		logger.Info("discovery: gateway identity available again, announce resumed")
	}
	return gw, true
}

// announcePort 取要发布的端口，**当前运行值**优先。
//
// 不能直接用 gw.Port：那一列只在首次启动时从 cfg.Server.Port 写入
// （见 database/sqlite/seedGateway.go），此后永不刷新 —— 运维改过 server.port
// 之后它就是一个指向没人监听的端口的陈旧值，管理平台照它连必然失败。
//
// 回退到 gw.Port 是为了手工构造 Config 的场景（负载测试的 harness 没有 server 段），
// 与 seedGateway.go 里 gatewayPort 的兜底同一处道理。
func (a *Announcer) announcePort(gw *vo.GatewayVO) int {
	if a.cfg != nil && a.cfg.Server.Port > 0 {
		return a.cfg.Server.Port
	}
	return gw.Port
}

// readLoop 接收探询并应答，直到套接字被 Stop 关闭。
//
// 不需要 stop 通道：退出信号来自 conn 被关闭（见 Stop），这是唯一能打断
// 阻塞中的 ReadFrom 的手段。
func (a *Announcer) readLoop(conn net.PacketConn) {
	buf := make([]byte, maxDatagram)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			// Stop 关了套接字 —— 这是正常的退出路径。用 errors.Is 判断，
			// 不要去匹配 "use of closed network connection" 字符串。
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// **其余错误必须继续读，不能 return**：Windows 上，此前发往一个
			// 已下线探询方的回包会激发 ICMP port unreachable，并在**下一次**
			// recvfrom 上以 WSAECONNRESET 的形式暴露出来。若把"任意错误"
			// 当作终止条件，读循环会在第一个平台探测完就静默死掉 ——
			// 表现是通知照发、探询从此无人应答，且日志里看不出发生了什么。
			logger.Debug("discovery: read failed: %v", err)
			continue
		}

		msg, ok := decode(buf[:n])
		if !ok {
			// 该端口不是保留端口，畸形报文与陌生协议都是常态，静默丢弃。
			continue
		}
		// 只处理探询。收到别的网关的 notify 是有意忽略的 —— 本功能只做单向
		// 发布，"局域网里有哪些网关"那份名单归管理平台维护。
		if msg.Type != TypeProbe {
			continue
		}

		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}
		if !a.probeAllowed(udpAddr.IP.String()) {
			continue
		}
		a.reply(conn, udpAddr)
	}
}

// probeAllowed 同源限流：一个源地址每秒最多换来一次应答。
//
// 只读判断，不改状态 —— 记账放在 recordProbe 里、由 reply 在真正应答之后调用。
// 这个分法是必要的：若在这里就把时间戳记下，那么应答因身份缺失等原因没能发出时，
// 该源在接下来一秒内的探询会被一并丢掉，表现为"探询时灵时不灵"。
// 而未应答的探询本来就不消耗任何资源，没有理由去限它。
func (a *Announcer) probeAllowed(src string) bool {
	now := time.Now()

	// 顺手清理过期条目。取上限的四分之一作阈值：不清理也能跑（有硬上限兜底），
	// 但那意味着一台跑了一年的网关会一直攥着几千条早已过期的记录。
	if len(a.lastProbe) > probeWindowMax/4 {
		for k, t := range a.lastProbe {
			if now.Sub(t) > probeWindowTTL {
				delete(a.lastProbe, k)
			}
		}
	}

	if last, ok := a.lastProbe[src]; ok {
		return now.Sub(last) >= probeReplyWindow
	}
	// 清理之后仍然满：拒绝新条目，而不是让伪造源地址把内存吃干。
	return len(a.lastProbe) < probeWindowMax
}

// recordProbe 记下"已应答过这个源地址"，供 probeAllowed 判定限流窗口。
func (a *Announcer) recordProbe(src string) {
	a.lastProbe[src] = time.Now()
}

// defaultApiKey 读取默认开放接口密钥；读不到一律返回空串。
//
// 与 currentIdentity 刻意不同的处置：这里**不告警**。默认密钥由启动播种保证
// 存在，缺失只可能是老库未升级或有人直接改了库，而后果仅仅是广播里少一个字段
// （管理平台仍能发现网关，只是要人工录入密钥）。为一个"少一个字段"每 5 秒刷
// 一条 WARN，会把真正该看的那条告警淹掉。
func (a *Announcer) defaultApiKey() string {
	if a.keys == nil {
		return ""
	}
	key, err := a.keys.DefaultOpenApiKey(context.Background())
	if err != nil {
		logger.Debug("discovery: read default api key failed: %v", err)
		return ""
	}
	return key
}

// reply 单播应答一次探询。
func (a *Announcer) reply(conn net.PacketConn, to *net.UDPAddr) {
	gw, err := a.src.GetGateway(context.Background())
	if err != nil {
		// 刻意不在这里告警，也不碰 warnedNoIdentity：announceLoop 会在同一
		// 条件下打出那条唯一的 WARN，两个协程各打一条只会把同一件事记成两条。
		// warnedNoIdentity 归 announceLoop 所有，跨协程写它就得加锁，不值得。
		return
	}

	msg := Message{
		Protocol:        Protocol,
		Version:         ProtocolVersion,
		Type:            TypeReply,
		GatewayID:       gw.ID,
		Name:            gw.Name,
		IP:              a.localIPv4For(to),
		Port:            a.announcePort(gw),
		FirmwareVersion: gw.FirmwareVersion,
		Description:     gw.Description,
		ApiKey:          a.defaultApiKey(),
		Timestamp:       time.Now().UnixMilli(),
	}
	b, err := encode(msg)
	if err != nil {
		logger.Error("discovery: encode reply: %v", err)
		return
	}
	if _, err := conn.WriteTo(b, to); err != nil {
		logger.Debug("discovery: reply to %s failed: %v", to, err)
	}
	// 应答已经发出（或已尽力发出），才记入限流窗口。见 probeAllowed 的说明。
	a.recordProbe(to.IP.String())
}

// localIPv4For 求"从这个对端看过来，本机是哪个地址"。
//
// 办法是拿一个连向对端的 UDP "连接"去问内核：UDP 的 connect 只建立默认对端
// 并完成路由查找与源地址选择，**不发送任何报文**，所以这是标准库能问出这个
// 问题的办法。自己遍历网卡做最长前缀匹配要多写几十行，而且仍然只是猜。
// （iot-gateway-drivers 仓库 fins/finsdrv/clientUdp.go 求 FINS 源节点号用的是同一手法。）
//
// 取不到就返回空串：回包照发，接收方应以 UDP 源地址为准，本字段本就只是参考。
func (a *Announcer) localIPv4For(dst *net.UDPAddr) string {
	conn, err := net.DialUDP("udp4", nil, dst)
	if err != nil {
		return ""
	}
	// 必须关：每次探询都要新建一个，不关就是每个探询漏一个 fd。
	defer conn.Close()

	la, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || la.IP == nil {
		return ""
	}
	return la.IP.String()
}
