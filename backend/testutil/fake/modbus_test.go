// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fake_test

import (
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"iot-gateway/testutil/fake"
)

// 这些用例直接裸发 MBAP 帧（不引任何 Modbus 库）：被测的是**假设备本身**，
// 引一个客户端库只会让断言多穿一层别人的编解码。

// mbapExchange 发一帧请求、收一帧应答，返回应答 PDU 与 MBAP 头里的长度字段。
func mbapExchange(t *testing.T, conn net.Conn, unit byte, pdu []byte) ([]byte, uint16) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	req := make([]byte, 7+len(pdu))
	binary.BigEndian.PutUint16(req[0:2], 1)
	binary.BigEndian.PutUint16(req[4:6], uint16(len(pdu)+1))
	req[6] = unit
	copy(req[7:], pdu)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("写请求: %v", err)
	}

	hdr := make([]byte, 7)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("读 MBAP 头: %v", err)
	}
	length := binary.BigEndian.Uint16(hdr[4:6])
	if length < 2 {
		t.Fatalf("MBAP 长度字段 %d 非法", length)
	}
	body := make([]byte, length-1)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("读 PDU: %v", err)
	}
	return body, length
}

// readRegisters 发一次 FC3 读，返回应答字节（异常应答时 t.Fatal）。
func readRegisters(t *testing.T, conn net.Conn, start, qty uint16) []byte {
	t.Helper()
	pdu := []byte{3, byte(start >> 8), byte(start), byte(qty >> 8), byte(qty)}
	body, _ := mbapExchange(t, conn, 1, pdu)
	if body[0] == 3|0x80 {
		t.Fatalf("读寄存器 [%d,%d) 得到异常应答：功能码 %#x 异常码 %d", start, start+qty, body[0], body[1])
	}
	if body[0] != 3 {
		t.Fatalf("应答功能码 = %d，期望 3", body[0])
	}
	return body[2:]
}

// dial 连上假从站。
func dial(t *testing.T, s *fake.Server) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", s.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("连假从站: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestModbusDefaultStillAllZero 钉住"零值 = 原行为"这条承诺。
//
// 新增的四个开关（regs/bits/span/reqs）全部是可选字段，不配它们时读写行为必须与
// 从前逐字节一致 —— 既有用例（黄金基线比对、容量压测）都建立在
// "NewModbusTCP 一律回全零"之上。
func TestModbusDefaultStillAllZero(t *testing.T) {
	s, err := fake.NewModbusTCP()
	if err != nil {
		t.Fatalf("启动假从站: %v", err)
	}
	defer s.Close()
	conn := dial(t, s)

	data := readRegisters(t, conn, 500, 4)
	if len(data) != 8 {
		t.Fatalf("应答数据长度 = %d，期望 8", len(data))
	}
	for i, b := range data {
		if b != 0 {
			t.Fatalf("默认行为应回全零，第 %d 字节 = %#x", i, b)
		}
	}

	// 地址范围也不该被校验：默认下任何地址都答。
	if got := readRegisters(t, conn, 60000, 2); len(got) != 4 {
		t.Fatalf("默认不该校验地址范围，返回 %d 字节", len(got))
	}
}

// TestModbusReturnsConfiguredRegisters 配了值的寄存器回配的值，没配的回 0。
func TestModbusReturnsConfiguredRegisters(t *testing.T) {
	regs := map[uint16]uint16{100: 0x1234, 101: 0xABCD, 105: 7}
	s, err := fake.NewModbusTCPWithRegisters(regs)
	if err != nil {
		t.Fatalf("启动假从站: %v", err)
	}
	defer s.Close()
	conn := dial(t, s)

	data := readRegisters(t, conn, 100, 6)
	want := []uint16{0x1234, 0xABCD, 0, 0, 0, 7}
	for i, w := range want {
		if got := binary.BigEndian.Uint16(data[i*2:]); got != w {
			t.Errorf("寄存器 %d = %#x，期望 %#x", 100+i, got, w)
		}
	}
}

// TestModbusReturnsConfiguredBits 配了的线圈置位，没配的为 0（位图从起始地址起算）。
func TestModbusReturnsConfiguredBits(t *testing.T) {
	s, err := fake.NewModbusTCPWithOption(fake.WithModbusBits(map[uint16]bool{3: true, 9: true}))
	if err != nil {
		t.Fatalf("启动假从站: %v", err)
	}
	defer s.Close()
	conn := dial(t, s)

	body, _ := mbapExchange(t, conn, 1, []byte{1, 0, 0, 0, 16})
	if body[0] != 1 {
		t.Fatalf("应答功能码 = %d，期望 1", body[0])
	}
	bitmap := body[2:]
	for i := 0; i < 16; i++ {
		got := bitmap[i/8]&(1<<uint(i%8)) != 0
		want := i == 3 || i == 9
		if got != want {
			t.Errorf("位 %d = %v，期望 %v", i, got, want)
		}
	}
}

// TestModbusUnreadableGap 落在区间之外的读请求回 IllegalDataAddress(2)。
//
// 这条是点位扫描「对半降级」用例的支点：假设备必须真的拒绝越界读，
// 扫描器才会一路拆到单点，把空洞一个个摘出来。
func TestModbusUnreadableGap(t *testing.T) {
	s, err := fake.NewModbusTCPWithUnreadableGap(100, 199)
	if err != nil {
		t.Fatalf("启动假从站: %v", err)
	}
	defer s.Close()
	conn := dial(t, s)

	// 区间内：正常。
	if got := readRegisters(t, conn, 100, 100); len(got) != 200 {
		t.Fatalf("区间内读回应有 200 字节，实际 %d", len(got))
	}

	cases := []struct {
		name       string
		start, qty uint16
	}{
		{"整段在区间之前", 0, 10},
		{"整段在区间之后", 200, 10},
		{"骑在区间起点上（前半越界）", 95, 10},
		{"骑在区间终点上（后半越界）", 195, 10},
		{"只有起点在区间内", 199, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pdu := []byte{3, byte(c.start >> 8), byte(c.start), byte(c.qty >> 8), byte(c.qty)}
			body, _ := mbapExchange(t, conn, 1, pdu)
			if body[0] != 3|0x80 {
				t.Fatalf("越界读应回异常应答，实际功能码 %#x", body[0])
			}
			if body[1] != 2 {
				t.Fatalf("异常码 = %d，期望 2（IllegalDataAddress）", body[1])
			}
		})
	}

	// 位区同样受区间约束（上限 200 也适用于线圈地址空间）。
	body, _ := mbapExchange(t, conn, 1, []byte{1, 0, 0, 0, 8})
	if body[0] != 1|0x80 || body[1] != 2 {
		t.Fatalf("位区越界应回 IllegalDataAddress，实际 %#v", body[:2])
	}
}

// TestModbusRequestCounter 请求计数把每一个到过设备的请求都算上（含被站号过滤丢弃的）。
func TestModbusRequestCounter(t *testing.T) {
	var reqs atomic.Int64
	s, err := fake.NewModbusTCPWithRequestCounter(&reqs)
	if err != nil {
		t.Fatalf("启动假从站: %v", err)
	}
	defer s.Close()
	conn := dial(t, s)

	for i := 0; i < 3; i++ {
		readRegisters(t, conn, uint16(i), 1)
	}
	if got := reqs.Load(); got != 3 {
		t.Fatalf("请求计数 = %d，期望 3", got)
	}
}

// TestModbusCombinedOptions 需要同时用多个开关时走 NewModbusTCPWithOption。
//
// 点位扫描的用例几乎都要"配值 + 空洞 + 计帧数"三件套同时生效，单独那几个
// 便捷构造函数拼不出来 —— 这条钉住组合路径确实工作。
func TestModbusCombinedOptions(t *testing.T) {
	var reqs atomic.Int64
	s, err := fake.NewModbusTCPWithOption(
		fake.WithModbusRegisters(map[uint16]uint16{50: 42}),
		fake.WithModbusSpan(40, 59),
		fake.WithModbusRequests(&reqs),
	)
	if err != nil {
		t.Fatalf("启动假从站: %v", err)
	}
	defer s.Close()
	conn := dial(t, s)

	if got := binary.BigEndian.Uint16(readRegisters(t, conn, 50, 1)); got != 42 {
		t.Errorf("寄存器 50 = %d，期望 42", got)
	}
	body, _ := mbapExchange(t, conn, 1, []byte{3, 0, 30, 0, 1})
	if body[0] != 3|0x80 || body[1] != 2 {
		t.Errorf("区间外应回 IllegalDataAddress，实际 %#v", body[:2])
	}
	if reqs.Load() != 2 {
		t.Errorf("请求计数 = %d，期望 2", reqs.Load())
	}
}

// TestServerCloseStopsServingAcceptedConns Close 返回之后，这台假设备不得再服务
// 任何连接 —— 包括那条**Accept 已经返回、注册还没发生**的连接。
//
// 这是 Server 骨架本身的一条契约（不只属于 Modbus），但判据要用真实帧来问，
// 所以用例落在这里。
//
// 它防的是一个真实发生过的假阴性：`srv.Close()` 与 acceptLoop 抢 s.mu 的窗口
// 只有两条指令宽（Accept 返回 → 抢锁注册），Close 若先拿到锁，就遍历完 conns
// 而这条连接**从此再也不会被关**，并且被正常地服务下去。原先的症状是
// modbusdrv 的 TestScanPointsAbortsOnConnectionError 大约每 8 次挂 1 次：
// Close 之后第一帧仍被应答，扫描一路读到底、一个错误都没有，而它要验的正是
// "连接断了要中止扫描"。单次几乎撞不上，所以这里连打 rounds 次把窗口摊开。
//
// 判据取"一个字节都收不到"，而不是"读报错"：连接没被 accept 到、监听器就关了
// 的那种次序下，这条连接既不会被服务也不会被关，读会一直阻塞 —— 那也是
// "不再服务"，同样算通过。
func TestServerCloseStopsServingAcceptedConns(t *testing.T) {
	const rounds = 50
	// 本机回环上一次应答是微秒级的，50ms 足够区分"被服务"与"没被服务"；
	// 而没被 accept 到的那种次序下每轮只赔这 50ms。
	const quiet = 50 * time.Millisecond

	reqPdu := []byte{3, 0, 0, 0, 1}
	req := make([]byte, 7+len(reqPdu))
	binary.BigEndian.PutUint16(req[0:2], 1)
	binary.BigEndian.PutUint16(req[4:6], uint16(len(reqPdu)+1))
	req[6] = 1
	copy(req[7:], reqPdu)

	for i := 0; i < rounds; i++ {
		var reqs atomic.Int64
		srv, err := fake.NewModbusTCPWithOption(
			fake.WithModbusRegisters(map[uint16]uint16{0: 1}),
			fake.WithModbusRequests(&reqs),
		)
		if err != nil {
			t.Fatalf("第 %d 轮起假 PLC: %v", i, err)
		}

		conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
		if err != nil {
			t.Fatalf("第 %d 轮连假 PLC: %v", i, err)
		}

		srv.Close()

		if err := conn.SetDeadline(time.Now().Add(quiet)); err != nil {
			t.Fatalf("SetDeadline: %v", err)
		}
		if _, err := conn.Write(req); err != nil {
			// 写就失败了，说明连接已经被关 —— 正是期望的结果
			_ = conn.Close()
			continue
		}
		buf := make([]byte, 8)
		if n, _ := conn.Read(buf); n > 0 {
			t.Fatalf("第 %d 轮：Close 之后这条连接仍被服务（收到 % X，设备共处理了 %d 帧）",
				i, buf[:n], reqs.Load())
		}
		_ = conn.Close()
	}
}
