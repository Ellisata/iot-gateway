// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"net"
	"testing"
	"time"

	"github.com/goburrow/serial"
)

// fakePort 记录 Read 调用次数的串口替身。空口上真实串口读会阻塞满
// serialReadTimeout，故这里立刻返回超时——被调用了几次才是本例的观测点。
type fakePort struct {
	reads int
	chunk []byte
}

func (p *fakePort) Open(*serial.Config) error   { return nil }
func (p *fakePort) Write(b []byte) (int, error) { return len(b), nil }
func (p *fakePort) Close() error                { return nil }
func (p *fakePort) Read(b []byte) (int, error) {
	p.reads++
	if len(p.chunk) == 0 {
		return 0, serial.ErrTimeout
	}
	n := copy(b, p.chunk)
	p.chunk = p.chunk[n:]
	return n, nil
}

// newTestSerialClient 构造一个处于「已连接」状态的串口替身。
// connected 改用 atomic.Bool（见 serial.go）后，没法再用结构体字面量初始化。
func newTestSerialClient(port serial.Port) *serialClient {
	c := &serialClient{port: port}
	c.connected.Store(true)
	return c
}

// 串口 Drain 只在上一轮没干净收尾时才真的去读。
//
// 这是本次优化的核心：串口查不了接收缓冲，读一次空口要空等 serialReadTimeout(20ms)。
// 而迟到的应答只可能在某轮没按时收到应答时才在途 —— 那种轮次会 MarkDirty。
func TestSerialDrainSkipsCleanRounds(t *testing.T) {
	port := &fakePort{}
	c := newTestSerialClient(port)

	// 干净轮次（含刚连上的第一轮）：不读，省下空等
	c.Drain()
	if port.reads != 0 {
		t.Errorf("干净轮次 Drain 读了 %d 次，应为 0（跳过空等）", port.reads)
	}

	// 上一轮没干净收尾：必须真的去清缓冲
	c.MarkDirty()
	c.Drain()
	if port.reads == 0 {
		t.Error("MarkDirty 之后 Drain 未读，迟到的上一轮应答会留下来被当成下一轮的应答")
	}

	// 清过一次即恢复干净，下一轮不再读
	before := port.reads
	c.Drain()
	if port.reads != before {
		t.Errorf("Drain 后状态未复位：又多读了 %d 次", port.reads-before)
	}
}

// 串口 Dirty 后，Drain 要把残留读到读不出为止（最多 serialDrainRounds 轮）。
func TestSerialDrainConsumesResidue(t *testing.T) {
	port := &fakePort{chunk: []byte{1, 2, 3}}
	c := newTestSerialClient(port)

	c.MarkDirty()
	c.Drain()
	if len(port.chunk) != 0 {
		t.Errorf("Drain 后仍残留 %d 字节", len(port.chunk))
	}
}

// newTCPPair 造一对已连通的 TCP 连接，返回 (driver 侧, 对端)。
func newTCPPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	type accepted struct {
		c   net.Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		ch <- accepted{c, err}
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	a := <-ch
	if a.err != nil {
		t.Fatalf("接受连接失败: %v", a.err)
	}
	t.Cleanup(func() {
		client.Close()
		a.c.Close()
	})
	return client, a.c
}

// 空缓冲时 Drain 必须立刻返回，不得空等一个 tcpDrainDeadline。
//
// 这正是被修掉的每帧 1ms：5000 点 / 每帧 12 个标识 = 417 帧，
// 每轮白等 417ms。阈值取 500µs —— 远大于探测的 ~1µs，又远小于旧实现的
// 1~1.5ms，既能挡住回归又不至于在负载波动下误报。
func TestTCPDrainOnEmptyBufferDoesNotWait(t *testing.T) {
	conn, _ := newTCPPair(t)
	c := &tcpClient{conn: conn, connected: true}

	start := time.Now()
	c.Drain()
	elapsed := time.Since(start)

	if elapsed > 500*time.Microsecond {
		t.Errorf("空缓冲 Drain 耗时 %v，应在探测后立即返回（旧实现要空等到 %v）",
			elapsed, tcpDrainDeadline)
	}
}

// 有残留时 Drain 仍要真的清干净 —— 探测只是省掉空等，不能把清理本身省掉。
func TestTCPDrainClearsResidue(t *testing.T) {
	conn, peer := newTCPPair(t)
	c := &tcpClient{conn: conn, connected: true}

	junk := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if _, err := peer.Write(junk); err != nil {
		t.Fatalf("对端写入失败: %v", err)
	}
	// 等残留到达本机缓冲
	time.Sleep(100 * time.Millisecond)

	c.Drain()

	// 清干净了：再读应当超时，而不是读回残留
	_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if n != 0 {
		t.Errorf("Drain 后仍读回 %d 字节残留: % X", n, buf[:n])
	}
	if err == nil {
		t.Error("Drain 后读空缓冲应超时")
	}
}

// 探测本身要能如实报出待读字节数。
func TestSockPendingBytes(t *testing.T) {
	conn, peer := newTCPPair(t)
	tcp := conn.(*net.TCPConn)

	if n, ok := sockPendingBytes(tcp); !ok {
		t.Fatal("sockPendingBytes 返回 ok=false，探测不可用（Drain 会退化为空等）")
	} else if n != 0 {
		t.Errorf("空缓冲待读字节 = %d, want 0", n)
	}

	payload := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	if _, err := peer.Write(payload); err != nil {
		t.Fatalf("对端写入失败: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	n, ok := sockPendingBytes(tcp)
	if !ok {
		t.Fatal("有数据时 sockPendingBytes 返回 ok=false")
	}
	if n != len(payload) {
		t.Errorf("待读字节 = %d, want %d", n, len(payload))
	}
}
