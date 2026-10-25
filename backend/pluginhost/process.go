// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"github.com/Ellisata/iot-gateway-sdk/pluginwire"
)

// errBinaryReplaced 磁盘上的插件二进制与加载/升级时记录的内容不一致。
//
// 它不是"这次启动失败"，而是"这次启动不该发生"：崩溃重启只是把同一个插件
// 再拉起来一次，不是一次热升级。由 superviseLoop 单独分支处理：放弃监管，
// 把文件变化交回目录扫描（热升级会做完整的注册与记账）。
var errBinaryReplaced = errors.New("pluginhost: 磁盘上的插件二进制已被替换")

// processOptions 单个插件进程的监管参数，由 Manager 从配置构造。
type processOptions struct {
	// Timeouts RPC 超时。
	Timeouts Timeouts

	// Watchdog 卡死巡检间隔：进程存活期间按此间隔检查 client.stuck()。
	Watchdog time.Duration

	// RestartMin/RestartMax 崩溃重启的退避区间（1s→2s→4s→…→上限）。
	RestartMin time.Duration
	RestartMax time.Duration

	// MaxRestarts 连续失败次数上限，超过后停止监管（恢复需重启网关，
	// 或运维移除再放回插件文件）。
	MaxRestarts int

	// StableAfter 进程连续存活超过此时长即视为稳定，重启计数与退避归零。
	StableAfter time.Duration

	// KillGrace 停机时发出 shutdown 后等待进程自行退出的宽限期，超时强杀。
	KillGrace time.Duration

	// StuckStreak/StuckAfter 卡死判定阈值，0 表示用包级默认值。
	// 仅供测试注入极小值以在毫秒级验证卡死重启路径。
	StuckStreak int64
	StuckAfter  time.Duration

	// Env 子进程环境变量。由 Manager 从零构造（不是 os.Environ()），
	// 避免把 JWT 签名密钥等无关变量白送给插件。
	Env []string

	// Infof/Warnf 日志输出函数（已带模块前缀）。
	//
	// 拆成两个字段、各自直接绑定 logger.Info / logger.Warn，而不是做成
	// "级别 + 格式化参数" 的单个入口：中间多一层闭包就多一个栈帧，
	// logger 里 runtime.Caller(2) 取到的 file:line 会全部塌到绑定的那一行。
	Infof func(format string, args ...any)
	Warnf func(format string, args ...any)
}

// defaultProcessOptions 给出各项的兜底默认值，供未显式配置的项使用。
func (o processOptions) withDefaults() processOptions {
	d := DefaultTimeouts()
	if o.Timeouts.Handshake <= 0 {
		o.Timeouts.Handshake = d.Handshake
	}
	if o.Timeouts.Connect <= 0 {
		o.Timeouts.Connect = d.Connect
	}
	if o.Timeouts.Ping <= 0 {
		o.Timeouts.Ping = d.Ping
	}
	if o.Timeouts.Read <= 0 {
		o.Timeouts.Read = d.Read
	}
	if o.Timeouts.Close <= 0 {
		o.Timeouts.Close = d.Close
	}
	if o.Watchdog <= 0 {
		o.Watchdog = 5 * time.Second
	}
	if o.RestartMin <= 0 {
		o.RestartMin = time.Second
	}
	if o.RestartMax <= 0 {
		o.RestartMax = 30 * time.Second
	}
	if o.MaxRestarts <= 0 {
		o.MaxRestarts = 10
	}
	if o.StableAfter <= 0 {
		o.StableAfter = 60 * time.Second
	}
	if o.KillGrace <= 0 {
		o.KillGrace = 2 * time.Second
	}
	if o.Env == nil {
		o.Env = pluginEnv()
	}
	if o.Infof == nil {
		o.Infof = func(string, ...any) {}
	}
	if o.Warnf == nil {
		o.Warnf = func(string, ...any) {}
	}
	return o
}

// pluginProcess 监管一个插件可执行文件的进程生命周期。
//
// 崩溃自愈不依赖任何回调：remoteDriver.IsConnected() 会同时检查
// processLink.isUp() 与世代号，进程一挂即返回 false，采集引擎既有的
// 1s→60s 重连退避会自然接管；监管在此期间把进程重新拉起。
type pluginProcess struct {
	path string
	name string
	opts processOptions

	mu       sync.Mutex
	cur      *client
	cmd      *exec.Cmd
	ready    chan struct{} // 进程可用时关闭；每次 markUp 替换为新通道
	stopped  bool
	restarts int
	delay    time.Duration
	lastUp   time.Time

	// meta 首个成功握手的 describe 结果。本实例生命周期内 Meta 不会变
	// （换 Meta 等于换进程实例），故缓存首个结果即可；热升级由 Manager
	// 拉起新实例并整体替换本对象，见 pluginLink。
	meta driversdk.Meta

	// expectSHA 磁盘上这个插件**应当**具有的内容哈希（加载 / 热升级时由 Manager
	// 填入）。spawn 在拉起前比对：不一致说明二进制已被替换，此时不能把它当作
	// "旧进程崩溃重启"执行 —— 那会用一个未经网关注册的 Meta 服务（协议与类型表
	// 与注册表不一致），且在 disableHotUpgrade=true 时直接违背"新版本重启网关后
	// 才生效"的承诺。详见 spawn 的注释。
	//
	// 空串表示不校验（测试里的裸 pluginProcess）。
	expectSHA string

	// epochBase 世代号基数。首次加载时为 0，热升级时由 setEpochBase 在换入前
	// 重设为旧实例的终值（见 setEpochBase）。只允许这两处写，且都在持 p.mu
	// 或换入之前完成，之后只读。
	epochBase atomic.Uint64
	epochNum  atomic.Uint64 // 本实例内每次成功拉起自增；对外世代号 = base + num
	// spawnCount 进程被实际启动的次数（含握手失败的那些）。用于诊断
	//「一直在重启但从未就绪」以及测试中观察握手在途状态。
	spawnCount atomic.Int64
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup

	// stopCtx 在 stop() 时取消，用于立即中止进行中的握手，
	// 否则停机要等满一个握手超时（默认 10s）。
	stopCtx    context.Context
	stopCancel context.CancelFunc
}

func newPluginProcess(path string, opts processOptions) *pluginProcess {
	return newPluginProcessAt(path, opts, "")
}

// newPluginProcessAt 同 newPluginProcess，但额外声明「磁盘上应当是什么内容」。
//
// expectSHA 只用于热升级/首次加载路径（传空串即关闭校验，测试里的裸
// pluginProcess 走这条）。
//
// 世代号基数**不在这里给**：首次加载时为 0（首个进程实例，无前身），热升级时
// 由 pluginLink.retireAndSwap 在旧实例退役后调用 setEpochBase 重设 —— 构造期
// 就固定会被"窗口内旧实例崩溃重启"撞号，理由见 setEpochBase。
func newPluginProcessAt(path string, opts processOptions, expectSHA string) *pluginProcess {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	p := &pluginProcess{
		path:      path,
		name:      pluginName(path),
		opts:      opts.withDefaults(),
		expectSHA: expectSHA,
		// ready 初始为开启状态：进程尚未拉起时 acquire 会在此等待，
		// 直到 markUp 关闭它（或 ctx / stopCh 解除等待）。
		ready:      make(chan struct{}),
		stopCh:     make(chan struct{}),
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
	}
	return p
}

// describeProtocols 把 Meta 里的协议名拼成一行，用于日志。
func describeProtocols(meta driversdk.Meta) string {
	names := make([]string, 0, len(meta.Protocols))
	for _, p := range meta.Protocols {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

// pluginName 取可执行文件名（不含扩展名）用于日志。
func pluginName(path string) string {
	base := path
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".exe")
}

// setCmd 登记/清除当前拉起的进程。
func (p *pluginProcess) setCmd(cmd *exec.Cmd) {
	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()
}

// currentCmd 返回当前拉起的进程（含握手尚未完成的），无则 nil。
func (p *pluginProcess) currentCmd() *exec.Cmd {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd
}

func (p *pluginProcess) describeMeta() driversdk.Meta {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.meta
}

// start 拉起监管循环。返回后进程在后台异步启动。
func (p *pluginProcess) start() {
	p.wg.Add(1)
	go p.superviseLoop()
}

// stop 停止监管并回收进程。幂等，可重复调用。
func (p *pluginProcess) stop() {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopped = true
		p.mu.Unlock()
		// 先取消进行中的握手，再置停机位：否则若监管循环恰好卡在
		// 拉起某个启动失败的插件上，停机要等满一个握手超时。
		p.stopCancel()
		close(p.stopCh)
	})
	// 等监管循环完成 kill + Wait（回收），确保不留孤儿。
	p.wg.Wait()
}

// retire 退役一个进程实例：停机，并**确定性地**摘除客户端。
//
// 与 stop() 的差别只在收尾。优雅停机路径不经过 markDown（见 superviseLoop
// 里 watch 返回 stopped 时的提前 return），p.cur 保持非 nil，客户端要等
// readLoop 读到管道 EOF 才被**异步** fail —— 而 readLoop 不在 p.wg 里，
// 所以 stop() 返回瞬间 isUp() 可能仍为 true。热升级要求退役返回后 isUp()
// 必为 false，否则采集侧会短暂看到一个「已退役却仍活着」的实例，并据此
// 向已死的进程发起调用。
//
// 顺序不可颠倒：
//   - 先 stop() 而非先 fail()：fail 会关掉 c.rw（stdin），插件会走
//     stdin-EOF 路径直接退出，**跳过 gracefulStop 的 MethodShutdown RPC**，
//     失去让插件自行关闭串口/句柄的机会；
//   - 先 detach 再 fail：让 isUp() 在一个确定的时刻翻转，而不是取决于
//     readLoop 的调度。fail/wait 都幂等且廉价（closeOnce；进程已被回收，
//     管道已闭，wait 立即返回）。
//
// 不改 stop() 本身：Manager.Stop() 与插件移除路径不需要这份确定性，
// 且既有测试断言的是 stop() 当前的语义。
func (p *pluginProcess) retire() {
	p.stop()

	p.mu.Lock()
	cl := p.cur
	p.cur = nil // 此后 isUp() 必为 false，与 markDown 的收尾一致
	p.mu.Unlock()

	if cl != nil {
		cl.fail(ErrProcessDown)
		cl.wait()
	}
}

// superviseLoop 是进程的守护循环：拉起 → 等待退出（含卡死巡检）→ 退避重启。
func (p *pluginProcess) superviseLoop() {
	defer p.wg.Done()

	for {
		if p.isStopped() {
			return
		}

		cmd, cl, err := p.spawn()
		if err != nil {
			if errors.Is(err, errBinaryReplaced) {
				// 不是启动失败，是"这次启动不该发生"：重试只会一直撞同一堵墙，
				// 还会按退避把 MaxRestarts 白烧掉。放弃监管，把文件变化交回目录扫描。
				p.opts.Warnf("driver plugin %s: %v；已停止按崩溃重启拉起，"+
					"等待目录扫描热升级（plugin.disableHotUpgrade=true 时需重启网关）", p.name, err)
				return
			}
			p.opts.Warnf("driver plugin %s: 启动失败: %v", p.name, err)
			if !p.recordFail() || !p.sleepBackoff() {
				return
			}
			continue
		}

		p.markUp(cl)
		p.opts.Infof("driver plugin %s: 已就绪 (协议 %s)", p.name, describeProtocols(p.describeMeta()))

		waitCh := make(chan error, 1)
		go func() { waitCh <- cmd.Wait() }()

		exitErr, stopped := p.watch(cmd, cl, waitCh)
		if stopped {
			return // 收到停机信号，进程已回收
		}

		p.markDown(exitErr)
		if p.isStopped() {
			return
		}
		p.opts.Warnf("driver plugin %s: 进程退出: %v", p.name, exitErr)
		if !p.recordFail() || !p.sleepBackoff() {
			return
		}
	}
}

// watch 在进程存活期间做卡死巡检，直到进程退出或收到停机信号。
//
// 返回 (进程退出原因, 是否因停机而返回)。停机路径下进程已回收，调用方直接退出。
func (p *pluginProcess) watch(cmd *exec.Cmd, cl *client, waitCh <-chan error) (error, bool) {
	ticker := time.NewTicker(p.opts.Watchdog)
	defer ticker.Stop()

	for {
		select {
		case err := <-waitCh:
			return err, false

		case <-ticker.C:
			if cl.stuck() {
				// 进程还活着但卡死：IsConnected() 会永远停在 true 且 Read 永远超时，
				// 而采集引擎只在 IsConnected()==false 时重连 —— 必须杀掉重启。
				p.opts.Warnf("driver plugin %s: 检测到卡死（连续 RPC 超时且长期无成功调用），强制重启", p.name)
				_ = cmd.Process.Kill()
			}

		case <-p.stopCh:
			p.gracefulStop(cmd, cl, waitCh)
			return nil, true
		}
	}
}

// gracefulStop 停机路径：先请求插件自行退出，宽限期后强杀并回收。
//
// 这是防孤儿进程的第二层（第一层是子进程读到 stdin EOF 自行退出）。
func (p *pluginProcess) gracefulStop(cmd *exec.Cmd, cl *client, waitCh <-chan error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.opts.KillGrace)
	defer cancel()

	// 尽力而为：插件不理睬就直接进宽限期。
	_ = cl.call(ctx, pluginwire.MethodShutdown, nil, nil)

	select {
	case <-waitCh:
		// 自行退出
	case <-time.After(p.opts.KillGrace):
		_ = cmd.Process.Kill()
		<-waitCh
	}
}

// spawn 拉起进程并完成 describe 握手。
func (p *pluginProcess) spawn() (*exec.Cmd, *client, error) {
	// 内容校验必须在 exec 之前：崩溃重启不是一次热升级。若磁盘上的内容已经变了，
	// 拉起来的进程会用**它自己的** Meta（协议名、类型表）服务，而网关的运行时
	// 注册表还是上一版的 —— 症状是这些协议全部 unknown protocol、类型解析不到；
	// 更糟的是 disableHotUpgrade=true 时会静默换上未经记账的新版本（含
	// lp.sha/lp.meta/provisionProtocols 全部不更新），直接违背文档承诺。
	// 这里拒绝拉起，把"文件变了"交回目录扫描：热升级会把它正常注册进来，
	// 关掉热升级时运维看到的是一条明确的告警而不是一次无声明的换版。
	if p.expectSHA != "" {
		got, err := hashFile(p.path)
		if err != nil {
			return nil, nil, fmt.Errorf("校验插件内容: %w", err)
		}
		if got != p.expectSHA {
			return nil, nil, fmt.Errorf("%w（期望 sha256 %s，实际 %s）",
				errBinaryReplaced, shortSHA(p.expectSHA), shortSHA(got))
		}
	}

	// 不经 shell、不接受任何参数：插件路径是唯一输入。
	cmd := exec.Command(p.path)
	cmd.Env = p.opts.Env
	cmd.SysProcAttr = pluginSysProcAttr()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start: %w", err)
	}
	p.spawnCount.Add(1)

	// 挂到「父进程退出即连坐终止」的 Job Object（Windows）。
	// 失败不致命 —— 孤儿防护退回 stdin EOF 那一层，但记下来便于排查。
	if err := pluginAfterStart(cmd); err != nil {
		p.opts.Warnf("driver plugin %s: 绑定父进程生命周期失败，孤儿防护降级为 stdin EOF: %v", p.name, err)
	}

	// 登记「当前进程」放在绑定之后：这样 currentCmd() != nil 蕴含
	// 「该进程已挂好孤儿防护」，诊断与测试都可据此判断，不必再猜时序。
	p.setCmd(cmd)

	// stderr 是插件的日志通道（SDK 已把 os.Stdout 重定向到 stderr），
	// 按行转成网关日志。走 Infof：这是插件自己的日志流，内容以常规运行为主。
	go logStderrLines(stderr, p.name, p.opts.Infof, p.opts.Warnf)

	cl := newClient(&stdioPipe{stdin: stdin, stdout: stdout})
	cl.setStuckThreshold(p.opts.StuckStreak, p.opts.StuckAfter)

	// 以 stopCtx 为父：停机时立即中止握手，不必等满超时。
	ctx, cancel := context.WithTimeout(p.stopCtx, p.opts.Timeouts.Handshake)
	defer cancel()

	var meta driversdk.Meta
	if err := cl.call(ctx, pluginwire.MethodDescribe, struct{}{}, &meta); err != nil {
		p.killAndReap(cmd, cl)
		return nil, nil, fmt.Errorf("握手失败: %w", err)
	}
	// 网关侧复校一遍插件自报的 Meta（SDK 也校验一次，但插件可用旧版 SDK）。
	if err := meta.Validate(); err != nil {
		p.killAndReap(cmd, cl)
		return nil, nil, fmt.Errorf("Meta 非法: %w", err)
	}

	p.mu.Lock()
	p.meta = meta
	p.mu.Unlock()

	return cmd, cl, nil
}

func (p *pluginProcess) killAndReap(cmd *exec.Cmd, cl *client) {
	p.setCmd(nil)
	_ = cmd.Process.Kill()
	cl.fail(ErrProcessDown)
	cl.wait()
	_ = cmd.Wait()
}

// markUp 换入新进程，并递增世代号使旧会话全部作废。
// markUp 标记进程已就绪（describe 握手通过）。
//
// **不在这里重置 p.delay**：退避的归零条件是「进程连续存活超过 StableAfter」，
// 那是 markDown 的判定。在这里无条件清零会让「起来 → 很快崩」这种最常见的
// 崩溃循环每次都被重置回 RestartMin，recordFail 的 `delay *= 2` 永远不可达，
// 于是 MaxRestarts 次重试在 ~MaxRestarts×1s 内烧完、插件被永久放弃 ——
// 与文档承诺的 1s→2s→4s→…→30s 完全相反。
func (p *pluginProcess) markUp(cl *client) {
	p.mu.Lock()
	p.cur = cl
	p.lastUp = time.Now()
	// 关闭当前 ready 唤醒所有 acquire 等待者，并换上新通道供下次等待。
	close(p.ready)
	p.ready = make(chan struct{})

	// 世代号递增：旧进程的会话全部作废。remoteDriver 据此判定陈旧会话
	// （不能只靠"未知会话"错误兜底 —— 新进程的会话 id 从 1 重排，
	// 陈旧 id 可能恰好命中另一个设备的会话，既不出错也察觉不到）。
	p.epochNum.Add(1)

	// 把世代号烙进客户端：remoteDriver 用「实际对话的那个客户端的世代号」
	// 判定会话有效性（见 client.epoch）。
	cl.epoch.Store(p.epoch())
	p.mu.Unlock()
}

// setEpochBase 在**换代时**重设世代号基数，使本实例的世代号严格大于旧实例
// 曾经到达过的最大值。
//
// 为什么不能在构造期就固定：upgrade 从读旧实例 epoch 到换入新实例之间要拉起
// 新进程并等握手（可达数十秒的一个窗口），而旧实例的监管协程在这段窗口内
// 仍可能崩溃重启一次，把它的 epoch 推到「旧值 + 1」。构造期定下的基数
// （= 旧值）在 markUp 后恰好也是「旧值 + 1」—— 撞上了。后果是所有重连到
// 重启后旧实例的驱动在换代后 stale() 为假，继续把旧会话 id 发给新进程。
//
// 调用方必须持 Manager.scanMu，且**必须在旧实例 retire() 之后**调用：
// retire 同步等监管协程退出并回收进程，此后 epochNum 不会再变，读到的即为终值。
//
// 注意新的基数大于 0 时，这里必须同步刷新 p.cur 的 epoch ——
// 「cl.epoch == p.epoch()」是 remoteDriver 判定会话有效性的不变量，
// 漏掉这一半会让换入后的首个客户端带着一个永远对不上的世代号，
// 表现为设备反复重连却永远连不上。
func (p *pluginProcess) setEpochBase(base uint64) {
	p.mu.Lock()
	p.epochBase.Store(base)
	if p.cur != nil {
		p.cur.epoch.Store(p.epoch())
	}
	p.mu.Unlock()
}

// markDown 摘除已退出的进程。
func (p *pluginProcess) markDown(cause error) {
	p.mu.Lock()
	cl := p.cur
	p.cmd = nil // 进程已退出，currentCmd/诊断不应再指向它
	// 存活足够久则视为稳定，重置重启计数与退避。
	if !p.lastUp.IsZero() && time.Since(p.lastUp) >= p.opts.StableAfter {
		p.restarts = 0
		p.delay = 0
	}
	p.mu.Unlock()

	if cl != nil {
		cl.fail(fmt.Errorf("%w: %v", ErrProcessDown, cause))
		cl.wait()
	}
}

// recordFail 记录一次失败并按退避递增延迟；返回 false 表示超过重启上限。
func (p *pluginProcess) recordFail() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.restarts++
	if p.restarts > p.opts.MaxRestarts {
		p.opts.Warnf("driver plugin %s: 连续失败 %d 次，已停止监管（恢复需重启网关，或移除文件后重新放入）",
			p.name, p.restarts)
		return false
	}
	if p.delay <= 0 {
		p.delay = p.opts.RestartMin
	} else if p.delay *= 2; p.delay > p.opts.RestartMax {
		p.delay = p.opts.RestartMax
	}
	return true
}

// sleepBackoff 按当前退避延迟等待；返回 false 表示收到停机信号。
func (p *pluginProcess) sleepBackoff() bool {
	p.mu.Lock()
	d := p.delay
	p.mu.Unlock()
	if d <= 0 {
		return true
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-p.stopCh:
		return false
	}
}

func (p *pluginProcess) isStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

// ==================== processLink 实现 ====================

// acquire 返回当前可用客户端；进程不在时等待其恢复（受 ctx 约束）。
//
// 进程处于退避窗口内时这里会等不到、最终由 ctx 超时返回错误 —— 这正是
// 期望的行为：采集引擎自身的 1s→60s 重连退避会接管，两个退避自然叠加，
// 不会互相打架。
func (p *pluginProcess) acquire(ctx context.Context) (*client, error) {
	for {
		p.mu.Lock()
		cl := p.cur
		ready := p.ready
		p.mu.Unlock()

		if cl != nil && cl.isUp() {
			return cl, nil
		}

		select {
		case <-ready:
			// 进程已（重新）就绪，回到循环再取一次客户端。
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.stopCh:
			return nil, ErrProcessDown
		}
	}
}

func (p *pluginProcess) isUp() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur != nil && p.cur.isUp()
}

func (p *pluginProcess) epoch() uint64 { return p.epochBase.Load() + p.epochNum.Load() }

// spawned 返回进程被实际启动过的次数。
func (p *pluginProcess) spawned() int64 { return p.spawnCount.Load() }

// ==================== 辅助 ====================

// stdioPipe 把子进程的 stdin/stdout 拼成 client 需要的双向流。
// Close 关闭 stdin 会让子进程读到 EOF 并自行退出（防孤儿的第一层）。
type stdioPipe struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *stdioPipe) Read(b []byte) (int, error)  { return p.stdout.Read(b) }
func (p *stdioPipe) Write(b []byte) (int, error) { return p.stdin.Write(b) }

func (p *stdioPipe) Close() error {
	return errors.Join(p.stdin.Close(), p.stdout.Close())
}

// logStderrLines 按行转发子进程 stderr 到网关日志。
//
// 插件的 stderr 是没有级别信息的自由文本（见 driversdk.Logf），网关只能
// 整条流按一个级别记。这里选 Info：多数行是插件的常规运行输出，按告警记
// 会让"插件一直在报警"成为常态，真正的告警反而淹没其中。插件启动失败这类
// 需要警觉的情况，网关另有 process.go 的「启动失败 / 进程退出」告警兜底。
func logStderrLines(r io.Reader, name string, logf, warnf func(string, ...any)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			logf("driver plugin %s: %s", name, line)
		}
	}

	// 扫完必须看 Err()：不看就等于"插件的 stderr 一旦断流就静默消失"，
	// 排查时只表现为"这个插件的日志莫名其妙没了"，无从下手。
	//
	// 但两种收尾是正常的，不能报警：
	//   - 进程退出后读到 EOF，Err() 本就是 nil；
	//   - cmd.Wait() 收尸时会连带关掉 StderrPipe 的读取端（exec 把它挂在
	//     closeAfterWait 上），正在读的这头于是拿到 fs.ErrClosed。这是拆机
	//     流程的一部分，不是故障。
	// 剩下的才是真问题：超长行（ErrTooLong）或管道读失败 —— 那之后插件的
	// 输出就再也进不了网关日志了。
	if err := sc.Err(); err != nil && !errors.Is(err, fs.ErrClosed) {
		warnf("driver plugin %s: 读取 stderr 中断，此后该插件的日志将丢失: %v", name, err)
	}
}
