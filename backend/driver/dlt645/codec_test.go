// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/goburrow/serial"
)

// fakeTransport 用预置字节流模拟传输层，让帧解析路径可以脱离硬件测试。
// maxChunk > 0 时按该大小分片返回，模拟 DTU / 串口服务器的分片投递。
type fakeTransport struct {
	in       []byte
	pos      int
	out      []byte
	maxChunk int
}

func (f *fakeTransport) Lock()                           {}
func (f *fakeTransport) Unlock()                         {}
func (f *fakeTransport) Drain()                          {}
func (f *fakeTransport) MarkDirty()                      {}
func (f *fakeTransport) SetReadDeadline(time.Time) error { return nil }
func (f *fakeTransport) IsConnected() bool               { return true }
func (f *fakeTransport) Close() error                    { return nil }
func (f *fakeTransport) Write(p []byte) (int, error) {
	f.out = append(f.out, p...)
	return len(p), nil
}
func (f *fakeTransport) Read(p []byte) (int, error) {
	if f.pos >= len(f.in) {
		return 0, serial.ErrTimeout
	}
	n := copy(p, f.in[f.pos:])
	if f.maxChunk > 0 && n > f.maxChunk {
		n = f.maxChunk
	}
	f.pos += n
	return n, nil
}

// 规范原文给出的 DL/T 645-1997 报文示例：
// 主站读取表号 000000000003 的 9010（正向有功总电能），
// 从站应答数据域还原后为 10 90（数据标识 9010）+ 28 01 00 00（BCD 00000128 → 1.28 kWh）。
// 校验码 55 = 从首个 68H 到数据域末字节的和 853 mod 256。
var real1997Response = []byte{
	0x68, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x68,
	0x81, 0x06, 0x43, 0xC3, 0x5B, 0x34, 0x33, 0x33,
	0x55, 0x16,
}

// 2007 版读 A相电压 的请求帧（规范给出的示例，含 33H 偏移）。
// 该校验码只有在按**已加 33H 偏移**的线上字节求和时才成立，
// 因此这个向量同时锁定了「偏移施加位置」与「校验和覆盖范围」两件事。
func TestBuildReadFrame2007Vector(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	cfg.PreambleBytes = 0

	addr, err := MeterAddressBytes("000000000161")
	if err != nil {
		t.Fatal(err)
	}
	got := buildReadFrame(cfg, addr, []DISpec{{DI: 0x02010100}})
	want := []byte{0x68, 0x61, 0x01, 0x00, 0x00, 0x00, 0x00, 0x68,
		0x11, 0x04, 0x33, 0x34, 0x34, 0x35, 0x17, 0x16}

	if string(got) != string(want) {
		t.Errorf("buildReadFrame = % X\n             want % X", got, want)
	}
}

// 前导唤醒字节必须被排除在校验和之外。
func TestBuildFrameExcludesPreambleFromChecksum(t *testing.T) {
	cfg := DefaultDLT645Config(TransportSerial) // 串口默认 4 个前导
	cfg.PreambleBytes = 4
	addr, _ := MeterAddressBytes("1")

	tcp := DefaultDLT645Config(TransportTCP)
	tcp.PreambleBytes = 0

	// buildReadFrame 不负责前导（由 exchange 在写入时拼接），两者应完全一致
	specs := []DISpec{{DI: 0x00000000, Bytes: 4}}
	a := buildReadFrame(cfg, addr, specs)
	b := buildReadFrame(tcp, addr, specs)
	if string(a) != string(b) {
		t.Errorf("前导配置不应影响帧体：% X vs % X", a, b)
	}
}

// 端到端：真实报文走完 解析 → 去偏移 → 校验 → 数据标识核对 → 解码。
func TestReal1997VectorEndToEnd(t *testing.T) {
	cfg := DefaultDLT645Config(TransportSerial)
	cfg.Version = Version1997
	cfg.PreambleBytes = 0

	addr, _ := MeterAddressBytes("000000000003")
	tr := &fakeTransport{in: real1997Response}

	resp, err := exchange(tr, cfg, addr, []byte{0x00})
	if err != nil {
		t.Fatalf("exchange 失败: %v", err)
	}
	if resp.C != ctrl1997OK {
		t.Fatalf("控制码 = %02X, want %02X", resp.C, ctrl1997OK)
	}

	spec, err := ParseAddress("9010", Version1997)
	if err != nil {
		t.Fatal(err)
	}
	diBytes := cfg.DIBytes()
	if got := readDI(resp.Data[:diBytes]); got != spec.DI {
		t.Fatalf("应答数据标识 = %04X, want %04X", got, spec.DI)
	}

	val, err := DecodeValue(resp.Data[diBytes:diBytes+spec.Bytes], &spec, "float")
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if got := FormatValue(val, "float"); got != "1.28" {
		t.Errorf("电能 = %q, want \"1.28\"", got)
	}
}

// 现场报文并不规整：前导字节、回显的请求帧、总线上其它表的应答、
// 缺失的结束符，都必须能正确跳过并取到真正的应答。
func TestTolerantParsing(t *testing.T) {
	cfg := DefaultDLT645Config(TransportSerial)
	cfg.Version = Version1997
	cfg.PreambleBytes = 0
	addr, _ := MeterAddressBytes("000000000003")

	// 半双工链路回显的主站请求帧（控制码 0x01 是请求码，不是应答码）
	echo := []byte{0x68, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x68,
		0x01, 0x02, 0x43, 0xC3, 0x0E, 0x16}

	// 同一 485 总线上另一块表的应答（地址域不同）
	other := append([]byte(nil), real1997Response...)
	other[1] = 0x09

	cases := map[string][]byte{
		"前导唤醒字节": {0xFE, 0xFE, 0xFE, 0xFE},
		"回显的请求帧": echo,
		"其它表的应答": other,
		"线路噪声":   {0x00, 0x68, 0xFF},
	}
	for name, prefix := range cases {
		t.Run(name, func(t *testing.T) {
			in := append(append([]byte(nil), prefix...), real1997Response...)
			tr := &fakeTransport{in: in}
			resp, err := exchange(tr, cfg, addr, []byte{0x00})
			if err != nil {
				t.Fatalf("exchange 失败: %v", err)
			}
			if got := readDI(resp.Data[:cfg.DIBytes()]); got != 0x9010 {
				t.Fatalf("数据标识 = %04X, want 9010", got)
			}
		})
	}

	// 部分表的实现瑕疵：不发最后一个 0x16。
	// 校验码是帧体的最后一字节，收到它并通过校验即可认定帧完整——
	// 若坚持等满 12+L 字节，这类表会永远读不到应答（表现为稳定超时）。
	t.Run("结束符缺失", func(t *testing.T) {
		tr := &fakeTransport{in: real1997Response[:len(real1997Response)-1]}
		resp, err := exchange(tr, cfg, addr, []byte{0x00})
		if err != nil {
			t.Fatalf("exchange 失败: %v", err)
		}
		if got := readDI(resp.Data[:cfg.DIBytes()]); got != 0x9010 {
			t.Fatalf("数据标识 = %04X, want 9010", got)
		}
	})
}

// 校验码不符的帧必须被丢弃，不能按长度跳过（长度本身也可能被干扰）。
func TestChecksumMismatchIsSkipped(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version1997
	addr, _ := MeterAddressBytes("000000000003")

	bad := append([]byte(nil), real1997Response...)
	bad[len(bad)-2] ^= 0xFF // 破坏校验码

	tr := &fakeTransport{in: bad}
	_, err := exchange(tr, cfg, addr, []byte{0x00})
	if err == nil {
		t.Fatal("校验码不符的帧不应被接受")
	}
}

// 关闭校验后应仅凭帧结构接受该帧（面向校验和计算不规范的廉价表）。
func TestChecksumDisabled(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version1997
	cfg.CheckChecksum = false
	addr, _ := MeterAddressBytes("000000000003")

	// 帧长与结构都完整，仅校验码字节被破坏
	bad := append([]byte(nil), real1997Response...)
	bad[len(bad)-2] ^= 0xFF

	tr := &fakeTransport{in: bad}
	resp, err := exchange(tr, cfg, addr, []byte{0x00})
	if err != nil {
		t.Fatalf("关闭校验后应接受该帧: %v", err)
	}
	if got := readDI(resp.Data[:cfg.DIBytes()]); got != 0x9010 {
		t.Errorf("数据标识 = %04X, want 9010", got)
	}
}

// 异常应答（设备可达但拒绝请求）必须被识别为协议级错误，
// 绝不能据此判定连接断开——否则一台能通但拒绝某数据标识的表会被整台判离线。
func TestAbnormalResponseIsProtocolError(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	addr, _ := MeterAddressBytes("1")

	// 68 01 00 00 00 00 00 68 D1 01 <错误码 0x02> CS 16
	// buildFrame 会施加 +0x33 偏移，故此处传未偏移的数据域
	frame := buildFrame(cfg, addr, 0xD1, []byte{0x02})

	tr := &fakeTransport{in: frame}
	_, err := exchange(tr, cfg, addr, []byte{0x00})
	if err == nil {
		t.Fatal("异常应答应返回错误")
	}
	if !isAbnormalErr(err) {
		t.Fatalf("错误应可被 isAbnormalErr 识别，实际: %v", err)
	}

	var ae *abnormalError
	if !errors.As(err, &ae) || ae.Code != 0x02 {
		t.Errorf("错误码 = %+v, want 0x02（无请求数据）", ae)
	}
}

// 异常应答必须把原始报文一路带出来：错误码 01「其他错误」不含任何可定位信息
// （表号不对、数据标识没实现、表处于非抄读状态都报 01），现场只能靠原始报文比对。
// Raw 是缓冲区切片的拷贝，因此即使解析前跳过了前导与噪声，内容仍是这条应答本身。
func TestAbnormalErrorCarriesRawFrame(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version2007
	addr, _ := MeterAddressBytes("1")

	frame := buildFrame(cfg, addr, ctrl2007Abnormal, []byte{0x01})
	// 前置唤醒字节与线路噪声，逼解析器跳过若干字节后重新同步
	tr := &fakeTransport{in: append([]byte{0xFE, 0xFE, 0x00}, frame...)}

	_, err := exchange(tr, cfg, addr, []byte{0x00})
	var ae *abnormalError
	if !errors.As(err, &ae) {
		t.Fatalf("应为异常应答，实际: %v", err)
	}
	if ae.Code != 0x01 {
		t.Errorf("错误码 = %02X, want 01", ae.Code)
	}
	if string(ae.Raw) != string(frame) {
		t.Errorf("原始报文 = % X\n           want % X", ae.Raw, frame)
	}
}

// 后续帧标志只在 2007 版有意义，且首帧（0xB1）与后续帧（0xB2）都要认。
func TestHasFollowUpOnlyFor2007(t *testing.T) {
	for _, ctrl := range []byte{ctrl2007More, ctrl2007NextMore} {
		if !hasFollowUp(&DLT645Config{Version: Version2007}, &response{C: ctrl}) {
			t.Errorf("2007 版控制码 %02X 应判定为有后续帧", ctrl)
		}
		if hasFollowUp(&DLT645Config{Version: Version1997}, &response{C: ctrl}) {
			t.Errorf("1997 版不应有后续帧机制（控制码 %02X）", ctrl)
		}
	}
	if hasFollowUp(&DLT645Config{Version: Version2007}, &response{C: ctrl2007OK}) {
		t.Error("0x91 是无后续帧的正常应答，不应判定为有后续帧")
	}
}

// 应答帧可能被拆成任意大小的分片到达（DTU 逐字节转发、串口按短周期读）。
// 分片边界正好落在帧头中段时曾越界 panic——判定帧头只需看到第二个 68H（8 字节），
// 但读出控制码与长度还要再 2 字节。这里把各种分片大小都锁死。
func TestFragmentedDelivery(t *testing.T) {
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = Version1997
	addr, _ := MeterAddressBytes("000000000003")

	for _, chunk := range []int{1, 2, 3, 5, 7, 8, 9, 10, 13, 17} {
		t.Run(fmt.Sprintf("chunk=%d", chunk), func(t *testing.T) {
			tr := &fakeTransport{in: real1997Response, maxChunk: chunk}
			resp, err := exchange(tr, cfg, addr, []byte{0x00})
			if err != nil {
				t.Fatalf("按 %d 字节分片投递时读取失败: %v", chunk, err)
			}
			if got := readDI(resp.Data[:cfg.DIBytes()]); got != 0x9010 {
				t.Fatalf("数据标识 = %04X, want 9010", got)
			}
		})
	}
}

// 缓冲区恰好停在帧头中段时不得越界 panic（上一条用例的单元级锁死）。
func TestParseAtShortFrameHeadNoPanic(t *testing.T) {
	cfg := DefaultDLT645Config(TransportSerial)
	cfg.Version = Version2007
	addr, _ := MeterAddressBytes("1")

	for _, avail := range []int{1, 2, 7, 8, 9} {
		buf := make([]byte, avail)
		buf[0] = frameStart
		if avail >= 8 {
			buf[7] = frameStart // 第二个 68H 已到，但 C/L 还没到
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("缓冲区剩 %d 字节时 panic: %v", avail, r)
				}
			}()
			if _, _, st := parseAt(buf, 0, cfg, addr); st != parseNeedMore {
				t.Errorf("缓冲区剩 %d 字节应判定为 parseNeedMore，得到 %v", avail, st)
			}
		}()
	}
}
