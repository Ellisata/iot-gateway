// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/goburrow/serial"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway-sdk/driversdk"
	"github.com/Ellisata/iot-gateway-sdk/driversdk/serialproxy"
	"github.com/Ellisata/iot-gateway-sdk/pluginwire"
)

// fakeHostPort 主机侧串口替身：Read 无数据时按 goburrow/serial 的约定返回
// serial.ErrTimeout（宿主据此在截止时间内继续等）。
type fakeHostPort struct {
	mu     sync.Mutex
	in     []byte
	out    []byte
	closed int
}

func (p *fakeHostPort) Read(b []byte) (int, error) {
	p.mu.Lock()
	if len(p.in) == 0 {
		p.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		return 0, serial.ErrTimeout
	}
	n := copy(b, p.in)
	p.in = p.in[n:]
	p.mu.Unlock()
	return n, nil
}

func (p *fakeHostPort) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.out = append(p.out, b...)
	p.mu.Unlock()
	return len(b), nil
}

func (p *fakeHostPort) Close() error {
	p.mu.Lock()
	p.closed++
	p.mu.Unlock()
	return nil
}

func (p *fakeHostPort) feed(b []byte) {
	p.mu.Lock()
	p.in = append(p.in, b...)
	p.mu.Unlock()
}

func (p *fakeHostPort) written() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.out...)
}

func (p *fakeHostPort) closeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

type hostPortFactory struct {
	mu    sync.Mutex
	ports []*fakeHostPort
}

func stubHostPorts(t *testing.T) *hostPortFactory {
	t.Helper()
	f := &hostPortFactory{}
	old := openHostSerialPort
	openHostSerialPort = func(*serial.Config) (io.ReadWriteCloser, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := &fakeHostPort{}
		f.ports = append(f.ports, p)
		return p, nil
	}
	t.Cleanup(func() { openHostSerialPort = old })
	return f
}

func (f *hostPortFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ports)
}

func (f *hostPortFactory) port(i int) *fakeHostPort {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ports[i]
}

func hostParams(port string, baud int) serialproxy.SerialOpenParams {
	return serialproxy.SerialOpenParams{
		Port: port, BaudRate: baud, DataBits: 8, StopBits: 1, Parity: "N",
	}
}

// 同一端口、相同参数的两个插件会话共享一个物理句柄；参数不同明确报错。
func TestSerialHostOpenSharesAndConflicts(t *testing.T) {
	h := newSerialHost()
	f := stubHostPorts(t)
	a, b := &client{}, &client{}

	ida, err := h.open(a, hostParams("COM7", 9600))
	if err != nil {
		t.Fatalf("首个 open 失败: %v", err)
	}
	idb, err := h.open(b, hostParams("COM7", 9600))
	if err != nil {
		t.Fatalf("第二个 open 失败: %v", err)
	}
	if f.count() != 1 {
		t.Fatalf("同一端口被打开 %d 次，want 1", f.count())
	}

	_, err = h.open(b, hostParams("COM7", 19200))
	if err == nil {
		t.Fatal("同端口不同波特率应当报配置错误")
	}

	_ = h.close(a, ida)
	_ = h.close(b, idb)
	if got := f.port(0).closeCount(); got != 1 {
		t.Errorf("全部句柄释放后端口关闭 %d 次，want 1", got)
	}
}

// 跨插件总线锁：一方持锁时另一方等不到；释放后可以拿到。
func TestSerialHostCrossOwnerLock(t *testing.T) {
	h := newSerialHost()
	stubHostPorts(t)
	a, b := &client{}, &client{}

	ida, _ := h.open(a, hostParams("COM8", 9600))
	idb, _ := h.open(b, hostParams("COM8", 9600))

	if err := h.lock(a, ida, time.Second); err != nil {
		t.Fatalf("A 加锁失败: %v", err)
	}
	if err := h.lock(b, idb, 20*time.Millisecond); err == nil {
		t.Fatal("A 持锁时 B 仍拿到了锁 —— 两个插件的帧会交错")
	}
	if _, err := h.write(b, idb, []byte{1}); err == nil {
		t.Fatal("未持锁的会话仍能写入")
	}

	if err := h.unlock(a, ida); err != nil {
		t.Fatalf("A 解锁失败: %v", err)
	}
	if err := h.lock(b, idb, time.Second); err != nil {
		t.Fatalf("A 解锁后 B 仍拿不到锁: %v", err)
	}
	if _, err := h.write(b, idb, []byte{9}); err != nil {
		t.Fatalf("B 持锁写入失败: %v", err)
	}
	_ = h.unlock(b, idb)
}

// 读：有数据立即返回；无数据且超过请求的时间返回 timedOut。
func TestSerialHostRead(t *testing.T) {
	h := newSerialHost()
	f := stubHostPorts(t)
	a := &client{}
	id, _ := h.open(a, hostParams("COM9", 9600))
	defer h.close(a, id)

	if err := h.lock(a, id, time.Second); err != nil {
		t.Fatalf("加锁失败: %v", err)
	}
	defer h.unlock(a, id)

	f.port(0).feed([]byte{0x68, 0x01})
	data, timedOut, err := h.read(a, id, 8, 200)
	if err != nil || timedOut || len(data) != 2 {
		t.Fatalf("读到 (% X, timedOut=%v, err=%v)", data, timedOut, err)
	}

	start := time.Now()
	data, timedOut, err = h.read(a, id, 8, 30)
	if err != nil {
		t.Fatalf("空口读报错: %v", err)
	}
	if !timedOut || len(data) != 0 {
		t.Fatalf("空口读到 (% X, timedOut=%v)，want 超时且无数据", data, timedOut)
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Errorf("空口读只等了 %v，没有按请求的 30ms 等", elapsed)
	}
}

// 插件进程断开时必须归还句柄与总线锁，否则总线会被一个不存在的会话占死。
func TestSerialHostCloseOwnerReleasesLock(t *testing.T) {
	h := newSerialHost()
	stubHostPorts(t)
	a, b := &client{}, &client{}

	ida, _ := h.open(a, hostParams("COM10", 9600))
	idb, _ := h.open(b, hostParams("COM10", 9600))
	if err := h.lock(a, ida, time.Second); err != nil {
		t.Fatalf("A 加锁失败: %v", err)
	}

	h.closeOwner(a)

	if _, err := h.lookup(a, ida); err == nil {
		t.Fatal("closeOwner 后句柄仍然可查")
	}
	if err := h.lock(b, idb, time.Second); err != nil {
		t.Errorf("A 断开后 B 仍拿不到总线锁: %v", err)
	}
	_ = h.unlock(b, idb)
	_ = h.close(b, idb)
}

// 句柄 id 只能由它的属主使用。
func TestSerialHostOwnership(t *testing.T) {
	h := newSerialHost()
	stubHostPorts(t)
	a, b := &client{}, &client{}
	id, _ := h.open(a, hostParams("COM11", 9600))

	if _, err := h.lookup(b, id); err == nil {
		t.Fatal("另一个插件会话能查到不属于它的句柄")
	}
	if err := h.lock(b, id, time.Millisecond); err == nil {
		t.Fatal("另一个插件会话能给不属于它的句柄加锁")
	}
	_ = h.close(a, id)
}

// ==================== 端到端：插件 ⇄ 网关代理 ====================

// proxyTestDriver 一个最小的串口驱动：Connect 通过注入的 opener 取得字节流，
// Read 按真实的请求-应答形态「加锁 → 写 → 读 → 解锁」。
type proxyTestDriver struct {
	open driver.SerialTransportOpener
	conn io.ReadWriteCloser
}

func (d *proxyTestDriver) SetSerialTransport(o driver.SerialTransportOpener) { d.open = o }

func (d *proxyTestDriver) Connect(string) error {
	if d.open == nil {
		return errors.New("serial opener 未注入")
	}
	c, err := d.open(driver.SerialOpenOptions{
		Port: "COM99", BaudRate: 9600, DataBits: 8, StopBits: 1, Parity: "N",
		ReadTimeout: 40 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	d.conn = c
	return nil
}

func (d *proxyTestDriver) Read(addrs []driver.DeviceAddress) ([]driver.ReadResult, error) {
	locker, _ := d.conn.(interface{ Lock() })
	unlocker, _ := d.conn.(interface{ Unlock() })
	if locker != nil {
		locker.Lock()
		defer unlocker.Unlock()
	}
	if _, err := d.conn.Write([]byte{0x01, 0x02, 0x03}); err != nil {
		return nil, err
	}
	buf := make([]byte, 8)
	n, err := d.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return []driver.ReadResult{{
		DeviceAddressID: addrs[0].ID,
		Value:           fmt.Sprintf("% X", buf[:n]),
	}}, nil
}

func (d *proxyTestDriver) Ping(string) error { return nil }
func (d *proxyTestDriver) IsConnected() bool { return d.conn != nil }
func (d *proxyTestDriver) Close() error {
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}

// TestSerialProxyEndToEnd 跑通完整链路：网关 client ⇄ 插件 Serve ⇄ 驱动 opener
// ⇄ serial.* 反向 RPC ⇄ 网关 serialHost ⇄ 物理端口替身。
func TestSerialProxyEndToEnd(t *testing.T) {
	f := stubHostPorts(t)
	h := newSerialHost()

	gwConn, pluginConn := net.Pipe()
	meta := driversdk.Meta{
		APIVersion: driversdk.APIVersion,
		Scope:      "proxytest",
		Protocols: []driversdk.Protocol{{
			Name: "Proxy.Test", SerialExclusive: true, SerialProxy: true,
		}},
		Types:   []*driver.DataType{{Name: "int16", Kind: driver.KindInt, Size: 2}},
		TypeMap: map[string]string{"Short": "int16"},
	}
	done := make(chan error, 1)
	go func() {
		done <- driversdk.ServeConn(pluginConn, driversdk.Factories{
			"Proxy.Test": func() driver.Driver { return &proxyTestDriver{} },
		}, meta, driversdk.Capabilities{})
	}()

	cl := newClient(gwConn)
	cl.setHooks(h.handleRequest, h.closeOwner)
	t.Cleanup(func() {
		_ = gwConn.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("插件服务端未退出")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var res driversdk.ConnectResult
	if err := cl.call(ctx, pluginwire.MethodConnect, driversdk.ConnectParams{
		Protocol: "Proxy.Test", ProtocolJSON: `{}`, SerialProxy: true,
	}, &res); err != nil {
		t.Fatalf("connect 失败: %v", err)
	}
	if f.count() != 1 {
		t.Fatalf("网关没有为代理打开端口（opens=%d）", f.count())
	}

	f.port(0).feed([]byte{0x68, 0x16})

	var reply driversdk.ReadReply
	if err := cl.call(ctx, pluginwire.MethodRead, driversdk.ReadParams{
		Session: res.Session,
		Addrs:   []driversdk.WireAddr{{ID: "addr-1"}},
	}, &reply); err != nil {
		t.Fatalf("read 失败: %v", err)
	}
	if len(reply.Results) != 1 {
		t.Fatalf("read 返回 %d 条结果", len(reply.Results))
	}
	if got := reply.Results[0].Value; got != "68 16" {
		t.Errorf("驱动读到 %q，want \"68 16\"（数据没从宿主代理走通）", got)
	}
	if got := f.port(0).written(); string(got) != string([]byte{1, 2, 3}) {
		t.Errorf("宿主端口收到 % X，want 01 02 03", got)
	}
	if !reply.Connected {
		t.Error("read 应答报告未连接")
	}

	if err := cl.call(ctx, pluginwire.MethodClose, driversdk.SessionParams{Session: res.Session}, nil); err != nil {
		t.Fatalf("close 失败: %v", err)
	}
	if got := f.port(0).closeCount(); got != 1 {
		t.Errorf("会话关闭后宿主端口关闭 %d 次，want 1", got)
	}
}
