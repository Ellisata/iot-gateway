// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package mitsubishi

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// ==================== 测试用的假读取原语 ====================

// wordPattern 自描述假设备里第 w 个字的取值。
//
// 关键在于**每个字的值就是它自己的地址**（低 16 位，w < 65536 时两两不同）：
// 于是"到底读到了哪个字"从返回值上直接看得出来。偏移算错时读到的是**别处**的
// 字，而那个字的值很合理 —— 只有让值本身携带地址，这种静默错位才会被测出来。
// 这正是设计里反复强调的那类陷阱（值看着完全合理），也是本文件存在的理由。
func wordPattern(w uint32) uint16 { return uint16(w & 0xFFFF) }

// selfDescRead 与 mcTransport.Read 同形的自描述假设备。
//
//	字单位：第 w 个字的 16 位 = w
//	位单位：绝对位 i 的取值 = 第 i/16 个字的第 i%16 位
//
// 于是位点位、字点位、双字点位各自的期望值都能**只从地址本身推算**出来。
func selfDescRead(head uint32, points uint16, bitMode bool) ([]byte, error) {
	if bitMode {
		out := make([]byte, points)
		for i := range out {
			abs := head + uint32(i)
			out[i] = byte((wordPattern(abs/16) >> (abs % 16)) & 1)
		}
		return out, nil
	}
	out := make([]byte, int(points)*2)
	for i := 0; i < int(points); i++ {
		binary.LittleEndian.PutUint16(out[i*2:], wordPattern(head+uint32(i)))
	}
	return out, nil
}

// scriptedReader 可编排的假读取原语，用于驱动降级 / 末尾 / 配额 / 中止这几条路径。
//
// 与自描述假设备互补：那个验"值对不对"，这个验"读不到时怎么办"。整段读得到的
// 假 PLC（testutil/fake 的 MC3E）驱动不了后一半 —— 而那几条路径才是最容易写错的。
//
// **它模拟的是设备的真实脾气**：一个请求只要**跨到**任何一个读不到的地址，
// 设备就整段拒绝（结束码非 0），而不是只把那个地址挖空。对半降级存在的全部
// 理由就是这一点。
type scriptedReader struct {
	// unreadable 这些地址读不到。任何**覆盖到**其中之一的请求都会被整段拒绝。
	unreadable []uint32

	// endFrom/hasEnd 从这个地址起全部读不到，模拟越过软元件末尾。
	// 用 hasEnd 而不是零值判断：0 本身是一个合法的"末尾"。
	endFrom uint32
	hasEnd  bool

	// endCode 换一个结束码；0 表示用 endCodeAddrRange。
	endCode uint16

	// oneWordOnly 只接受单点请求，其余一律回结束码错误 ——
	// 用来把对半降级一路逼到单点，从而验请求数上限。
	oneWordOnly bool

	// failAfter/failErr 成功响应这么多次之后开始返回 failErr（模拟读到一半断链）。
	// failAfter <= 0 表示不启用。
	failAfter int
	failErr   error

	okCount       int
	calls         []uint32
	pointsPerCall []uint16
}

func (r *scriptedReader) code() uint16 {
	if r.endCode == 0 {
		return endCodeAddrRange
	}
	return r.endCode
}

func (r *scriptedReader) read(head uint32, points uint16, bitMode bool) ([]byte, error) {
	r.calls = append(r.calls, head)
	r.pointsPerCall = append(r.pointsPerCall, points)

	if r.failAfter > 0 {
		if r.okCount >= r.failAfter {
			return nil, r.failErr
		}
		r.okCount++
	}
	if r.oneWordOnly && points > 1 {
		return nil, newEndCodeError(r.code())
	}

	last := uint64(head) + uint64(points) - 1
	for _, bad := range r.unreadable {
		if uint64(bad) >= uint64(head) && uint64(bad) <= last {
			return nil, newEndCodeError(r.code())
		}
	}
	if r.hasEnd && last >= uint64(r.endFrom) {
		return nil, newEndCodeError(r.code())
	}
	return selfDescRead(head, points, bitMode)
}

// ==================== 地址生成：往返 ====================

// TestPointNameRoundTrip 生成的地址串必须能被 ParseMCAddress 还原回**同一个位置**。
//
// 这条不变量比它看起来重要得多：一个解析不出来的地址写进 device_address 之后，
// 这个点位永远采不到值（planFor 在规划前就把它拆出去，只报 QualityBadAddress）。
//
// 覆盖 9 个软元件 × 各自允许的粒度 × 含贴地址空间上限的起点。"同一个位置"包括
// 三件事：软元件相同、地址号相同、字内位号相同（位软元件必须没有位号）。
func TestPointNameRoundTrip(t *testing.T) {
	// 起点刻意混入非字边界（1/5/15/17）与八进制下有意义的取值（24 = X30、8 = X10），
	// 以及贴近地址空间上限的 maxDeviceNumber-5。
	starts := []uint32{0, 1, 5, 8, 15, 16, 17, 24, 100, 511, maxDeviceNumber - 5}

	for _, devName := range pointScanDevices {
		dev, ok := lookupDevice(devName)
		if !ok {
			t.Fatalf("表单里的软元件 %q 不在驱动表里", devName)
		}
		widths := []scanWidth{widthWord, widthDWord}
		if dev.isBit {
			widths = []scanWidth{widthBit}
		}
		for _, width := range widths {
			for _, start := range starts {
				opt := pointScanOptions{dev: dev, base: start, width: width, count: 5}
				// 贴上限的那几个起点装不下 5 个点（双字粒度尤甚：一个点占两个地址号）。
				// 这里**必须**按 maxCount 收窄，否则测试会拿一个超出地址空间的名字
				// 去要求解析成功 —— 而实现刻意让那种名字解析不出来（写进
				// device_address 只会让那个点位永远没有值）。
				// 真正在跑的那条路（parsePointScanOptions）本来就按同一个 maxCount 截断。
				if maxN := opt.maxCount(); opt.count > maxN {
					opt.count = maxN
				}
				if opt.count <= 0 {
					t.Fatalf("%s/%s start=%d: 该起点没有任何可寻址空间", dev.name, width, start)
				}
				for idx := 0; idx < opt.count; idx++ {
					name := opt.pointName(idx)
					addr, ok := ParseMCAddress(name)
					if !ok {
						t.Fatalf("%s/%s start=%d idx=%d: 生成的名字 %q 解析不回来",
							dev.name, width, start, idx, name)
					}
					wantNum, wantBit := expectedPosition(dev, width, start, idx)
					if addr.Device.name != dev.name {
						t.Errorf("%q 解析成了软元件 %s，期望 %s", name, addr.Device.name, dev.name)
					}
					if addr.Number != wantNum {
						t.Errorf("%s/%s start=%d idx=%d: %q 解析出地址号 %d，期望 %d",
							dev.name, width, start, idx, name, addr.Number, wantNum)
					}
					if addr.Bit != wantBit {
						t.Errorf("%s/%s start=%d idx=%d: %q 解析出字内位号 %d，期望 %d",
							dev.name, width, start, idx, name, addr.Bit, wantBit)
					}
				}
			}
		}
	}
}

// expectedPosition 第 idx 个点位在地址空间里的期望位置（地址号 + 字内位号）。
func expectedPosition(dev mcDevice, width scanWidth, base uint32, idx int) (num uint32, bit int) {
	switch {
	case dev.isBit:
		return base + uint32(idx), -1
	case width == widthBit:
		return base + uint32(idx/16), idx % 16
	case width == widthDWord:
		return base + uint32(idx*2), -1
	default:
		return base + uint32(idx), -1
	}
}

// TestOctalStringRoundTrip octalString 与 address.go 的 parseOctal 必须严格互逆。
//
// X/Y 是八进制寻址（前端 addressSyntax.js 里 mc 的示例 "X30" 就是八进制），
// 生成侧与解析侧一旦分叉，扫出来的地址会落在**别的位置**，而名字看着完全合理。
func TestOctalStringRoundTrip(t *testing.T) {
	cases := []struct {
		v    uint32
		want string
	}{
		{0, "0"},
		{1, "1"},
		{7, "7"},
		{8, "10"},                     // 八进制 10
		{24, "30"},                    // 前端示例里的 X30
		{64, "100"},                   // 八进制 100 = 十进制 64
		{511, "777"},                  // 八进制 777
		{32767, "77777"},              //
		{maxDeviceNumber, "77777777"}, // 上限本身必须可表达
	}
	for _, c := range cases {
		got := octalString(c.v)
		if got != c.want {
			t.Errorf("octalString(%d) = %q，期望 %q", c.v, got, c.want)
		}
		back, err := parseOctal(got)
		if err != nil {
			t.Errorf("parseOctal(%q) = %v —— 生成的名字解析不回来", got, err)
			continue
		}
		if uint32(back) != c.v {
			t.Errorf("parseOctal(%q) = %d，期望 %d", got, back, c.v)
		}
	}
}

// ==================== 选项解析 ====================

// TestParsePointScanOptions 覆盖取舍：count 缺失/非法报错、X/Y 的八进制解释、
// 位软元件不能用字粒度、count 按粒度截断。
func TestParsePointScanOptions(t *testing.T) {
	t.Run("正常解析", func(t *testing.T) {
		opt, err := parsePointScanOptions(json.RawMessage(`{"device":"D","start":"100","width":"dword","count":8}`))
		if err != nil {
			t.Fatalf("= %v", err)
		}
		if opt.dev.name != "D" || opt.base != 100 || opt.width != widthDWord || opt.count != 8 {
			t.Errorf("解析结果 = %+v", opt)
		}
	})

	t.Run("大小写与空值容错", func(t *testing.T) {
		opt, err := parsePointScanOptions(json.RawMessage(`{"device":" d ","width":"","count":"4"}`))
		if err != nil {
			t.Fatalf("= %v", err)
		}
		if opt.dev.name != "D" || opt.width != widthWord || opt.count != 4 || opt.base != 0 {
			t.Errorf("解析结果 = %+v（start 缺省应为 0，width 缺省应为 word）", opt)
		}
	})

	t.Run("起始地址可以写成 JSON 数字", func(t *testing.T) {
		// 表单里 start 是文本框（送字符串），但客户端仍可能送数字 ——
		// 声明成 string 时一个数字会让整个 unmarshal 失败，不是那一栏失效。
		opt, err := parsePointScanOptions(json.RawMessage(`{"device":"D","start":100,"width":"word","count":1}`))
		if err != nil {
			t.Fatalf("= %v", err)
		}
		if opt.base != 100 {
			t.Errorf("base = %d，期望 100", opt.base)
		}
	})

	t.Run("X 的起始地址按八进制解释", func(t *testing.T) {
		opt, err := parsePointScanOptions(json.RawMessage(`{"device":"X","start":"30","width":"bit","count":2}`))
		if err != nil {
			t.Fatalf("= %v", err)
		}
		// 八进制 30 = 十进制 24，于是第一个点位是 X30 而不是 X24。
		if opt.base != 24 {
			t.Fatalf("base = %d，期望 24（八进制 30）", opt.base)
		}
		if got := opt.pointName(0); got != "X30" {
			t.Errorf("第一个点位 = %q，期望 X30", got)
		}
		if got := opt.pointName(1); got != "X31" {
			t.Errorf("第二个点位 = %q，期望 X31", got)
		}
	})

	t.Run("X 的非法八进制位必须报错", func(t *testing.T) {
		// 静默按十进制解析会让用户扫到一段完全不同的地址，而结果看着完全正常。
		for _, bad := range []string{"8", "9", "19", "1A", "-1"} {
			if _, err := parsePointScanOptions(
				json.RawMessage(fmt.Sprintf(`{"device":"X","start":%q,"width":"bit","count":1}`, bad))); err == nil {
				t.Errorf("start=%q 应报错（X 是八进制，只接受 0~7）", bad)
			}
		}
	})

	t.Run("D 的十进制起始地址", func(t *testing.T) {
		opt, err := parsePointScanOptions(json.RawMessage(`{"device":"D","start":"100","width":"word","count":1}`))
		if err != nil {
			t.Fatalf("= %v", err)
		}
		if opt.base != 100 || opt.pointName(0) != "D100" {
			t.Errorf("base=%d name=%q，期望 100 / D100", opt.base, opt.pointName(0))
		}
	})

	t.Run("位软元件不能用字粒度", func(t *testing.T) {
		// 字单位读位软元件是驱动支持的，但产出的是 M0/M16/M32 这种「看着像位、
		// 其实是 16 点一组」的名字，而值是整字 —— 用户不会发现。
		for _, w := range []string{"word", "dword"} {
			if _, err := parsePointScanOptions(
				json.RawMessage(fmt.Sprintf(`{"device":"M","start":"0","width":%q,"count":4}`, w))); err == nil {
				t.Errorf("位软元件 M + 粒度 %s 应报错", w)
			}
		}
	})

	t.Run("字软元件可以用位粒度", func(t *testing.T) {
		opt, err := parsePointScanOptions(json.RawMessage(`{"device":"D","start":"100","width":"bit","count":3}`))
		if err != nil {
			t.Fatalf("= %v", err)
		}
		want := []string{"D100.0", "D100.1", "D100.2"}
		for i, w := range want {
			if got := opt.pointName(i); got != w {
				t.Errorf("第 %d 个点位 = %q，期望 %q", i, got, w)
			}
		}
	})

	t.Run("未知软元件报错", func(t *testing.T) {
		if _, err := parsePointScanOptions(json.RawMessage(`{"device":"Q","count":1}`)); err == nil {
			t.Error("未知软元件应报错")
		}
	})

	t.Run("count 缺失或非正报错", func(t *testing.T) {
		// 「扫多少」没有合理的默认值 —— 猜一个会让用户拿到一份他并没有要求的清单。
		for _, opt := range []string{`{"device":"D"}`, `{"device":"D","count":0}`, `{"device":"D","count":-1}`} {
			if _, err := parsePointScanOptions(json.RawMessage(opt)); err == nil {
				t.Errorf("%s 应报错", opt)
			}
		}
	})

	t.Run("空选项报错", func(t *testing.T) {
		if _, err := parsePointScanOptions(nil); err == nil {
			t.Error("空选项应报错")
		}
	})

	t.Run("非法粒度报错", func(t *testing.T) {
		if _, err := parsePointScanOptions(json.RawMessage(`{"device":"D","width":"qword","count":1}`)); err == nil {
			t.Error("非法粒度应报错")
		}
	})

	t.Run("count 越过地址空间按粒度截断而不是报错", func(t *testing.T) {
		// 意图很清楚：扫到能扫的末尾。
		opt, err := parsePointScanOptions(
			json.RawMessage(fmt.Sprintf(`{"device":"D","start":"%d","width":"word","count":1000000}`, maxDeviceNumber-2)))
		if err != nil {
			t.Fatalf("= %v", err)
		}
		if opt.count != 3 {
			t.Errorf("count = %d，期望截断到 3（D%d~D%d）", opt.count, maxDeviceNumber-2, maxDeviceNumber)
		}
	})

	t.Run("双字粒度的地址空间截断要除以二", func(t *testing.T) {
		// 差一陷阱：一个双字点位占两个地址号，写成 avail 会让最后一个点位的
		// 第二个字越界，而越界那半边读到的是**下一段**的数据。
		opt, err := parsePointScanOptions(
			json.RawMessage(fmt.Sprintf(`{"device":"D","start":"%d","width":"dword","count":1000000}`, maxDeviceNumber-4)))
		if err != nil {
			t.Fatalf("= %v", err)
		}
		if opt.count != 2 {
			t.Errorf("count = %d，期望 2（剩余 5 个地址号只装得下 2 个双字）", opt.count)
		}
		for idx := 0; idx < opt.count; idx++ {
			addr, _ := ParseMCAddress(opt.pointName(idx))
			if addr.Number+1 > maxDeviceNumber {
				t.Errorf("第 %d 个点位 %q 的第二个字越界了", idx, opt.pointName(idx))
			}
		}
	})

	t.Run("起始地址已在末尾时报错", func(t *testing.T) {
		_, err := parsePointScanOptions(
			json.RawMessage(fmt.Sprintf(`{"device":"D","start":"%d","width":"dword","count":4}`, maxDeviceNumber)))
		if err == nil {
			t.Error("双字粒度在最后一个地址上没有任何可扫空间，应报错")
		}
	})
}

// ==================== 单请求规模 ====================

// TestPointsPerRequestPerMode 单请求点数上限随「软元件类别 × 粒度」变四档。
//
// 当成同一个常数会静默错两档：字软元件扫位时请求数变成 16 倍（一段本来扫得完的
// 地址会撞上请求数上限、拿到一份标着「未扫完」的清单）；双字时每帧字数翻倍
// （配置贴近型号上限的现场直接超限）。
func TestPointsPerRequestPerMode(t *testing.T) {
	mk := func(devName string, w scanWidth) pointScanOptions {
		return pointScanOptions{dev: mustDev(devName), width: w}
	}
	cases := []struct {
		name string
		opt  pointScanOptions
		want int
	}{
		{"位软元件 + bit（1 点 = 1 位）", mk("M", widthBit), 100},
		{"字软元件 + bit（一块 = maxWords 个字的位，少要一个字见注释）", mk("D", widthBit), 16 * 99},
		{"字软元件 + word（1 点 = 1 字）", mk("D", widthWord), 100},
		{"字软元件 + dword（1 点 = 2 字）", mk("D", widthDWord), 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.opt.pointsPerRequest(100); got != c.want {
				t.Errorf("pointsPerRequest(100) = %d，期望 %d", got, c.want)
			}
		})
	}
}

// TestSpanWithinSingleRequestLimit 任何一块的**请求点数**都不得超过设备自己的
// maxReadWords —— 那条上限就是「这台 PLC 一帧能读多少」的已知可用值。
//
// 对位粒度尤其重要：块头向下取整到字边界、块尾向上取整，最坏会多用掉一个字。
// 起点刻意取遍 0..31（含各种字内偏移），把那个最坏情况走满。
func TestSpanWithinSingleRequestLimit(t *testing.T) {
	const maxWords = 100
	modes := []struct {
		dev   string
		width scanWidth
	}{
		{"M", widthBit},
		{"X", widthBit},
		{"D", widthBit},
		{"D", widthWord},
		{"D", widthDWord},
		{"W", widthWord},
	}
	for _, m := range modes {
		opt := pointScanOptions{dev: mustDev(m.dev), width: m.width}
		per := opt.pointsPerRequest(maxWords)
		for start := 0; start < 32; start++ {
			for _, n := range []int{1, per - 1, per} {
				if n <= 0 {
					continue
				}
				_, points, _ := opt.span(start, n)
				if int(points) > maxWords {
					t.Errorf("%s/%s start=%d n=%d: 单请求点数 %d 超过 maxReadWords %d",
						opt.dev.name, m.width, start, n, points, maxWords)
				}
			}
		}
	}
}

// ==================== 值的位置：名字 ↔ 值必须一致 ====================

// TestScanPointsValueMatchesName 每个点位的值必须就是**它的名字指向的那个位置**上的值。
//
// 这是全套实现里最重要的一条断言。地址生成（pointName）与偏移换算（span +
// buildPoint）是两段独立的代码，它们对「第 idx 个点位在哪里」的看法一旦分叉，
// 产出的就是一份地址正确、值却来自别处的清单 —— 而**值看着完全合理**，
// 用户不会发现，直到采上来的数据对不上。自描述假设备（字 w 的值 = w）让这种
// 错位必然表现为断言失败。
//
// 覆盖四种读法，起点刻意落在字中间（5）与非字边界（17）。
func TestScanPointsValueMatchesName(t *testing.T) {
	modes := []struct {
		dev   string
		width scanWidth
	}{
		{"D", widthWord},
		{"D", widthDWord},
		{"D", widthBit},
		{"M", widthBit}, // 位软元件，非八进制
		{"X", widthBit}, // 位软元件，八进制
		{"L", widthBit},
		{"W", widthWord}, // 非连续软元件
	}
	starts := []uint32{0, 5, 17}

	for _, m := range modes {
		dev := mustDev(m.dev)
		for _, start := range starts {
			t.Run(fmt.Sprintf("%s-%s-start%d", dev.name, m.width, start), func(t *testing.T) {
				opt := pointScanOptions{dev: dev, base: start, width: m.width, count: 37}
				res, err := scanWith(selfDescRead, opt, 100, time.Now().Add(5*time.Second))
				if err != nil {
					t.Fatalf("scanWith = %v", err)
				}
				if res.Incomplete || res.Truncated {
					t.Fatalf("不该触到任何上限: %+v", res)
				}
				if res.Scanned != 37 || res.Readable != 37 || len(res.Points) != 37 {
					t.Fatalf("计数 = scanned %d / readable %d / points %d，期望 37",
						res.Scanned, res.Readable, len(res.Points))
				}
				for idx, p := range res.Points {
					wantName := opt.pointName(idx)
					if p.Name != wantName {
						t.Errorf("第 %d 个点位的名字 = %q，期望 %q", idx, p.Name, wantName)
					}
					wantValue, wantZero := expectedValueFromName(t, p.Name, m.width)
					if wantZero {
						if p.Value != "" || p.Note != mcPointScanZeroNote {
							t.Errorf("%s: 期望零值点位（Value 空、Note %q），得到 Value=%q Note=%q",
								p.Name, mcPointScanZeroNote, p.Value, p.Note)
						}
						continue
					}
					if p.Value != wantValue {
						t.Errorf("%s: 值 = %q，期望 %q —— 读到的不是这个名字指向的那个位置",
							p.Name, p.Value, wantValue)
					}
				}
			})
		}
	}
}

// expectedValueFromName 只从**地址名字**推算出该点位应有的值。
//
// 名字是唯一的真源：ParseMCAddress 把它还原成（软元件、地址号、字内位号），
// 再按自描述假设备的编码把那个位置的值算出来。这条路径与实现里的偏移换算
// （span + buildPoint）**没有任何共享代码**，因此它能独立地发现错位。
func expectedValueFromName(t *testing.T, name string, width scanWidth) (value string, zero bool) {
	t.Helper()
	addr, ok := ParseMCAddress(name)
	if !ok {
		t.Fatalf("生成的地址 %q 解析不回来", name)
	}

	if width == widthBit {
		var bit uint16
		if addr.Bit < 0 {
			// 位软元件：Number 就是绝对位地址
			bit = (wordPattern(addr.Number/16) >> (addr.Number % 16)) & 1
		} else {
			// 字软元件取位：Number 是字地址，Bit 是字内位号
			bit = (wordPattern(addr.Number) >> uint(addr.Bit)) & 1
		}
		if bit == 0 {
			return "", true
		}
		return "1", false
	}

	// 字 / 双字：**解出来是 0 就按零值点位记**（插件侧的判据是原始字节全 0，
	// 而这里的字模式编码下"原始字节全 0"与"值为 0"是同一件事）。
	var v uint32
	if width == widthDWord {
		v = uint32(wordPattern(addr.Number)) | uint32(wordPattern(addr.Number+1))<<16
	} else {
		v = uint32(wordPattern(addr.Number))
	}
	if v == 0 {
		return "", true
	}
	return strconv.FormatUint(uint64(v), 10), false
}

// ==================== 降级与各种停止条件 ====================

// TestScanPointsDegradesAroundHole 含空洞的范围必须**对半降级拆开**，
// 而不是把整块记成读不到。
//
// 设备对含"不存在地址"的整个请求会整段拒绝，只有一路拆到单点才问得出来
// "哪几个地址真的不存在"。这里断言的是**可读的那一段被摘出来了**
// （D100~D102 出现在列表里）以及请求数是对数级而不是逐点。
//
// **注意 D104 之后被记成"读不到"是有意的**：D 是单片连续的软元件，
// 因此单点读不到就被推断为"越过末尾"。这个取舍的另一半（有空洞的软元件
// 会继续往后扫）见 TestScanPointsNonContiguousKeepsScanning。
func TestScanPointsDegradesAroundHole(t *testing.T) {
	r := &scriptedReader{unreadable: []uint32{103}}
	opt := pointScanOptions{dev: mustDev("D"), base: 100, width: widthWord, count: 8}

	res, err := scanWith(r.read, opt, 100, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("scanWith = %v", err)
	}
	if res.Scanned != 8 {
		t.Errorf("Scanned = %d，期望 8（范围里每个地址都要有账）", res.Scanned)
	}
	got := pointNames(res)
	for _, want := range []string{"D100", "D101", "D102"} {
		if !contains(got, want) {
			t.Errorf("点位 %q 不在结果里（实得 %v）—— 含空洞的整块被记成了"+
				"「全都读不到」，而没有降级拆开", want, got)
		}
	}
	if contains(got, "D103") {
		t.Errorf("读不到的 D103 不该出现在列表里（实得 %v）", got)
	}
	// 对数级而不是逐点：8 个地址逐个试要 8 次以上，降级只要几次。
	if len(r.calls) >= 8 {
		t.Errorf("发了 %d 次请求，看不出对半降级（8 个地址不该逐个试）", len(r.calls))
	}
}

// TestScanPointsStopsAtAreaEnd 单片连续的软元件上，读到第一个读不到的点就收工。
//
// 继续逐个去问只会把一次"扫到末尾"变成几百次必然失败的请求，最后撞上请求数
// 上限、给用户一份标着「未扫完」的结果 —— 而它其实早就扫完了。
func TestScanPointsStopsAtAreaEnd(t *testing.T) {
	// 只有 D0~D2 读得到；D3 起越过了这个区的末尾。
	r := &scriptedReader{hasEnd: true, endFrom: 3}
	opt := pointScanOptions{dev: mustDev("D"), base: 0, width: widthWord, count: 400}

	res, err := scanWith(r.read, opt, 100, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("scanWith = %v", err)
	}
	if res.Scanned != 400 || res.Readable != 3 || res.Unreadable != 397 {
		t.Errorf("计数 = scanned %d / readable %d / unreadable %d，期望 400 / 3 / 397",
			res.Scanned, res.Readable, res.Unreadable)
	}
	// 走的是「推断」而不是「逐个探测」：后面 397 个地址一个请求都没发。
	if len(r.calls) > 20 {
		t.Errorf("发了 %d 次请求，说明没有在读到末尾时收工（应为对数级）", len(r.calls))
	}
	if res.Incomplete {
		t.Error("扫到末尾是正常完成，不该标成「未扫完」 —— 那会让用户以为范围没覆盖到")
	}
}

// TestScanPointsNonContiguousKeepsScanning X/Y/B/W 的地址空间**有空洞**，
// 单点读不到不得推断"后面都读不到"。
//
// 这四种软元件的地址按槽位与模块分配（X0~X1F 是 1 号槽、X20~X3F 是 2 号槽……），
// 没插模块的槽位读不到，而它后面的槽位完全可能是好的。照抄 FINS 那条「读到末尾
// 就停」的捷径，清单会**从空洞处断掉**，而 Truncated / Incomplete 都不置位 ——
// 用户看到一份短了一截、却看不出任何异常的清单。这是本功能最难查的一类故障。
func TestScanPointsNonContiguousKeepsScanning(t *testing.T) {
	// 粒度按软元件自身的性质取：X/Y/B 是位软元件（位粒度），W 是字软元件（字粒度）。
	// 假读取原语里的 unreadable 单位因此与请求的块头一致 —— 位粒度下是位地址，
	// 字粒度下是字地址。
	cases := []struct {
		dev   string
		width scanWidth
	}{
		{"X", widthBit},
		{"Y", widthBit},
		{"B", widthBit},
		{"W", widthWord},
	}
	for _, c := range cases {
		t.Run(c.dev, func(t *testing.T) {
			// 头 8 个地址读不到（一段未分配的槽位/模块），其后的全部正常。
			r := &scriptedReader{unreadable: []uint32{0, 1, 2, 3, 4, 5, 6, 7}}
			opt := pointScanOptions{dev: mustDev(c.dev), base: 0, width: c.width, count: 40}

			res, err := scanWith(r.read, opt, 100, time.Now().Add(5*time.Second))
			if err != nil {
				t.Fatalf("scanWith = %v", err)
			}
			if res.Readable != 32 || res.Unreadable != 8 {
				t.Errorf("计数 = readable %d / unreadable %d，期望 32 / 8 —— "+
					"空洞之后的地址被漏掉了（清单从空洞处断掉）", res.Readable, res.Unreadable)
			}
			got := pointNames(res)
			if last := opt.pointName(39); !contains(got, last) {
				t.Errorf("最后一个点位 %q 不在结果里（实得 %v）—— "+
					"非连续软元件上不该在第一个空洞处收工", last, got)
			}
		})
	}
}

// TestContiguousOnlyForSolidAreas 这张表是上面那条行为的分界线，逐项钉住。
//
// 判错的方向是「把有空洞的当连续的」（清单静默变短）与「把连续的当有空洞的」
// （请求数从对数级涨到 2n-1，一段大范围扫描会撞上请求数上限、标着未扫完）。
func TestContiguousOnlyForSolidAreas(t *testing.T) {
	want := map[string]bool{
		// 按槽位/模块分配，中段可以有未分配的空洞
		"X": false, "Y": false, "B": false, "W": false,
		// 单片连续
		"D": true, "M": true, "L": true, "R": true, "ZR": true,
	}
	for name, w := range want {
		if got := (pointScanOptions{dev: mustDev(name)}).contiguous(); got != w {
			t.Errorf("软元件 %s 的 contiguous = %v，期望 %v", name, got, w)
		}
	}
}

// TestScanPointsStopsAtRequestQuota 请求数上限必须是一个确定性的上界。
//
// 对一段整段读不到的地址，降级会退化成一棵满二叉树（最坏 2n-1 次请求）；
// 只在循环里判时间，耗时就会随网络延迟漂移（本地 PLC 上几百次请求可能只要
// 几百毫秒，于是照样跑完，而在慢链路上又跑不完）。触顶置 Incomplete 并返回已得结果。
func TestScanPointsStopsAtRequestQuota(t *testing.T) {
	r := &scriptedReader{oneWordOnly: true}
	opt := pointScanOptions{dev: mustDev("D"), base: 0, width: widthWord, count: 400}

	res, err := scanWith(r.read, opt, 100, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("scanWith = %v", err)
	}
	if !res.Incomplete {
		t.Error("请求数用尽时必须置 Incomplete —— 用户得知道范围没扫完")
	}
	if len(r.calls) > mcPointScanMaxRequests {
		t.Errorf("发了 %d 次请求，超过上限 %d", len(r.calls), mcPointScanMaxRequests)
	}
}

// TestScanPointsTruncatesAtMaxPoints 达到 MaxPoints 即停止并置 Truncated。
//
// Truncated（结果条数到上限）与 Incomplete（范围还没扫到）是两个不同的标志，
// 合并成一句话会让用户得出错误的结论。
func TestScanPointsTruncatesAtMaxPoints(t *testing.T) {
	opt := pointScanOptions{dev: mustDev("D"), base: 0, width: widthWord, count: mcPointScanMaxPoints + 50}
	res, err := scanWith(selfDescRead, opt, 100, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("scanWith = %v", err)
	}
	if !res.Truncated {
		t.Error("达到 MaxPoints 必须置 Truncated")
	}
	if len(res.Points) != mcPointScanMaxPoints {
		t.Errorf("返回 %d 个点位，期望恰好 %d 个", len(res.Points), mcPointScanMaxPoints)
	}
	if res.Incomplete {
		t.Error("只是结果条数到上限，范围本身扫得完，不该同时置 Incomplete")
	}
}

// TestScanPointsAbortsOnTransportErr 链路类错误必须**中止**，且带上已得的部分结果。
//
// 把设备掉线记成"这里没有点位"，用户会从一份几乎全是空的清单里得出与事实
// 完全相反的结论，而采集侧此时也已经在重连了 —— 两边的结论会互相矛盾。
func TestScanPointsAbortsOnTransportErr(t *testing.T) {
	t.Run("读取中途断链", func(t *testing.T) {
		// 第一块 100 个点读得到，第二块开始断链。
		r := &scriptedReader{failAfter: 1, failErr: errors.New("mc tcp: read header failed: EOF")}
		opt := pointScanOptions{dev: mustDev("D"), base: 0, width: widthWord, count: 200}

		res, err := scanWith(r.read, opt, 100, time.Now().Add(5*time.Second))
		if err == nil {
			t.Fatal("链路错误必须中止扫描，而不是记成「地址不存在」")
		}
		if res == nil {
			t.Fatal("中止时也应返回已得的部分结果，不该返回 nil")
		}
		if res.Readable != 100 {
			t.Errorf("已读到的 100 个点位不该被丢掉，得到 %d 个", res.Readable)
		}
	})
}

// TestScanPointsAbortsOnBusyEndCode 可恢复的 PLC 状态错误必须中止，不得当成地址问题。
//
// **这是整套分类里最有争议的一格。** 0xC0EF（通信错误 / PLC 忙 / 程序运行中）
// 与地址无关：一次偶然的忙就会把整份点位清单从中间截断，而用户看到的是一份
// 短了一截、却看不出任何异常的清单。宁可中止并如实报错（已扫到的部分照常返回）。
func TestScanPointsAbortsOnBusyEndCode(t *testing.T) {
	opt := pointScanOptions{dev: mustDev("D"), base: 0, width: widthWord, count: 16}
	for _, code := range []uint16{endCodeCommError, 0xC059, 0xC05C, 0xC05E, 0xC200, 0xC0F0} {
		r := &scriptedReader{unreadable: []uint32{0}, endCode: code}
		if _, err := scanWith(r.read, opt, 100, time.Now().Add(5*time.Second)); err == nil {
			t.Errorf("结束码 0x%04X 应中止扫描（它不代表地址不存在）", code)
		}
	}
}

// TestScanPointsTreatsDeviceRangeEndCodeAsAddressEnd 文件寄存器没配置的 PLC 上扫 ZR：
// 每个地址都回 0x4031（真机 10.62.100.22:10000 的形状，见 frame.go 的 endCodeDeviceRange）。
//
// **这就是那个真机 bug**：白名单只认 0xC0xx 时，那台 PLC 的 ZR 扫描会中止成
// 「扫描失败：扫描 ZR0.0 … ZR3.15 时读取失败: mc: end code 0x4031」。
// 它其实是纯粹地址层面的判定 —— 整个软元件的可用范围是空的 —— 结论应当是
// "这一段（其实是整个区）读不到"，与 0xC051 那条路径完全一样。
func TestScanPointsTreatsDeviceRangeEndCodeAsAddressEnd(t *testing.T) {
	// 起点 0、位粒度、64 个点 —— 与用户报的那次扫描同一个形状（ZR0.0 … ZR3.15）。
	r := &scriptedReader{endFrom: 0, hasEnd: true, endCode: endCodeDeviceRange}
	opt := pointScanOptions{dev: mustDev("ZR"), base: 0, width: widthBit, count: 64}

	res, err := scanWith(r.read, opt, 100, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("0x4031 是地址问题（整个软元件的可用范围为空），不该中止扫描: %v", err)
	}
	if res.Readable != 0 || len(res.Points) != 0 {
		t.Errorf("这台 PLC 上一个 ZR 地址都读不到，得到 readable=%d points=%d",
			res.Readable, len(res.Points))
	}
	if res.Scanned != 64 || res.Unreadable != 64 {
		t.Errorf("64 个地址都该计成「读不到」，得到 scanned=%d unreadable=%d",
			res.Scanned, res.Unreadable)
	}
	if res.Truncated || res.Incomplete {
		t.Error("整段读不到既不是截断（条数到上限）也不是没扫完（预算耗尽）")
	}
	// 降级是对数的：读到第一个读不到就收工，后面的地址一次性记账 ——
	// 逐个地址去问会变成 64 次必然失败的请求，而结论一模一样（见 mcScanner.run）。
	if len(r.calls) > 8 {
		t.Errorf("对半降级只该发 %d 次以内的请求，实际发了 %d 次", 8, len(r.calls))
	}
}

// TestIsAddressEndCode 白名单与黑名单**各断言一遍**。
//
// 这个分类错在哪个方向都有明确的坏结果：收宽了会静默把清单从中间截断
// （用户拿着一份看不出异常的错误清单），收窄了会把一段本来扫得完的地址
// 变成一条错误。因此两个方向都要有用例，不能只测"能识别的那几条"。
func TestIsAddressEndCode(t *testing.T) {
	t.Run("算地址问题（可以降级 / 判定末尾）", func(t *testing.T) {
		for _, code := range []uint16{
			endCodeAddrRange, endCodeDeviceIllegal, endCodeDeviceRange, endCodePointsError,
		} {
			if !isAddressEndCode(newEndCodeError(code)) {
				t.Errorf("结束码 0x%04X 应判为地址问题", code)
			}
		}
	})

	t.Run("不算地址问题（必须中止）", func(t *testing.T) {
		for _, code := range []uint16{
			endCodeCommError, // PLC 忙 / 程序运行中：可恢复的设备状态
			0xC059,           // 命令指定错误：请求形状错了
			0xC05A,           // frame.go 注释里出现过（地址/设备序写反）
			0xC05C,           // 请求数据长度错误
			0xC05E,           // 子命令指定错误
			0xC0F0,           // 数据不匹配
			0xC200,           // 远程密码错误
			0xC201,           // 远程密码锁定
			0xFFFF,           // 未知码一律保守中止
		} {
			if isAddressEndCode(newEndCodeError(code)) {
				t.Errorf("结束码 0x%04X **不得**判为地址问题 —— "+
					"把它当成「读不到」会让清单静默地从中间截断", code)
			}
		}
	})

	t.Run("非结束码错误一律不算", func(t *testing.T) {
		for _, err := range []error{
			errors.New("mc tcp: read header failed: EOF"),
			errors.New("mc tcp: write failed: broken pipe"),
			nil,
		} {
			if isAddressEndCode(err) {
				t.Errorf("%v 不是结束码错误，不该判为地址问题", err)
			}
		}
	})
}

// ==================== 缓冲区边界 ====================

// TestScanPointsDoesNotSendOutOfRangeRequest 块头越界时**不发请求**。
//
// buildMCBody 只把 head 发成 3 字节（frame.go），超上限的地址会被静默截断成
// 另一个地址 —— 读到的是**别处**的数据，而用户会把它当成这段地址的真实值。
// 与 finsdrv 的 16 位回绕守卫是同一条理由。
func TestScanPointsDoesNotSendOutOfRangeRequest(t *testing.T) {
	var sent []uint32
	var sentPoints []uint16
	read := func(head uint32, n uint16, bitMode bool) ([]byte, error) {
		sent = append(sent, head)
		sentPoints = append(sentPoints, n)
		return selfDescRead(head, n, bitMode)
	}
	// 起点贴着上限：一块 8 个点的请求必然越过 maxDeviceNumber。
	opt := pointScanOptions{dev: mustDev("D"), base: maxDeviceNumber - 3, width: widthWord, count: 8}

	res, err := scanWith(read, opt, 100, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("scanWith = %v", err)
	}
	for i, head := range sent {
		if uint64(head)+uint64(sentPoints[i])-1 > uint64(maxDeviceNumber) {
			t.Errorf("发出了越界请求 head=%d points=%d —— 地址会被静默截断成另一个位置",
				head, sentPoints[i])
		}
	}
	// 越界的那些点位按"读不到"计：用户需要知道它们**在范围里但读不出来**，
	// 否则会以为范围根本没覆盖到那里。
	if res.Unreadable == 0 {
		t.Error("越界的地址必须计入 Unreadable，而不是当作没扫过")
	}
	if res.Scanned != 8 {
		t.Errorf("Scanned = %d，期望 8（范围里的每个地址都要有账）", res.Scanned)
	}
}

// ==================== 预检与会话 ====================

// stubSession 一个最小的 SessionAccess。
type stubSession struct {
	drv driver.Driver
	err error
}

func (s stubSession) Do(fn func(driver.Driver) error) error {
	if s.err != nil {
		return s.err
	}
	return fn(s.drv)
}

// notMCDriver 一个不是 *mcDriver 的驱动，用于验类型开关。
type notMCDriver struct{}

func (notMCDriver) Connect(string) error { return nil }
func (notMCDriver) Ping(string) error    { return nil }
func (notMCDriver) IsConnected() bool    { return true }
func (notMCDriver) Close() error         { return nil }
func (notMCDriver) Read([]po.DeviceAddress) ([]driver.ReadResult, error) {
	return nil, nil
}

// TestScanPointsPreflight 预检必须在**开始扫描之前**把"这台设备根本不能扫"判掉，
// 并且把它与"会话坏了"区分开。
//
// 混在一起会被当成"插件调用失败"报上去，指向完全错误的方向 —— 用户该看到的是
// "设备未连接"，而不是"插件出错"。
func TestScanPointsPreflight(t *testing.T) {
	opts := json.RawMessage(`{"device":"D","start":"0","width":"word","count":4}`)
	deadline := time.Now().Add(3 * time.Second)

	t.Run("未连接的驱动", func(t *testing.T) {
		// 只有 transport 没有 client：Connect 从未被调用过。
		res, err := ScanPoints(stubSession{drv: &mcDriver{transport: TransportTCP}}, opts, deadline)
		if err == nil {
			t.Fatal("设备未连接时应报错，而不是返回一份空清单")
		}
		if res != nil {
			t.Error("预检失败不应返回结果")
		}
		if !strings.Contains(err.Error(), "未连接") {
			t.Errorf("错误 = %v，应说明是设备未连接", err)
		}
	})

	t.Run("不是本协议的驱动", func(t *testing.T) {
		if _, err := ScanPoints(stubSession{drv: notMCDriver{}}, opts, deadline); err == nil {
			t.Error("陌生的驱动类型应报错")
		}
	})

	t.Run("会话本身出错时原样上报", func(t *testing.T) {
		sessErr := errors.New("会话已关闭")
		if _, err := ScanPoints(stubSession{err: sessErr}, opts, deadline); !errors.Is(err, sessErr) {
			t.Errorf("错误 = %v，期望原样上报会话错误", err)
		}
	})

	t.Run("选项非法时不碰会话", func(t *testing.T) {
		// 选项错误是用户的输入问题，不该先去连一次设备。
		called := false
		sess := callRecorder{sess: stubSession{drv: &mcDriver{transport: TransportTCP}}, called: &called}
		if _, err := ScanPoints(sess, json.RawMessage(`{"device":"D"}`), deadline); err == nil {
			t.Fatal("选项非法应报错")
		}
		if called {
			t.Error("选项解析失败时不该访问会话")
		}
	})

	t.Run("maxReadWords 装不下双字时报错", func(t *testing.T) {
		// 双字点位的跨度是 2 字，而 CalcMCRanges 遇到「单点位跨度超过 maxReadWords」
		// 会让**整台设备**的区间计算直接报错 ⇒ 采不到任何值。用户只是在扫描页
		// 填了个粒度，代价却是这台设备彻底停摆 —— 宁可这里报一句清楚的错。
		cfg := DefaultMCConfig()
		cfg.MaxReadWords = 1
		drv := &mcDriver{transport: TransportTCP, config: cfg, client: &fakeTransport{connected: true}}

		_, err := ScanPoints(stubSession{drv: drv},
			json.RawMessage(`{"device":"D","start":"0","width":"dword","count":4}`), deadline)
		if err == nil {
			t.Fatal("maxReadWords=1 时装不下双字点位，应报错")
		}
		if !strings.Contains(err.Error(), "maxReadWords") {
			t.Errorf("错误 = %v，应点名 maxReadWords", err)
		}
	})
}

// callRecorder 记录 Do 是否被调用过。
type callRecorder struct {
	sess   SessionAccess
	called *bool
}

func (c callRecorder) Do(fn func(driver.Driver) error) error {
	*c.called = true
	return c.sess.Do(fn)
}

// ==================== 表单与驱动表的一致性 ====================

// TestPointScanDeviceOptionsMatchDriver 表单里的每个软元件选项都必须能在驱动
// 自己的 mcDevices 表里找到，且**标签里的位/字与八进制标注与驱动的分类一致**。
//
// 没有这条测试，下拉框与驱动表就是两份会静默漂移的清单：加一个软元件只改一边，
// 用户在下拉里选得到它、却会被 parsePointScanOptions 拒掉；或者标签写着「位」
// 而驱动按字处理 —— 后者更糟，用户会按位去理解一份字为单位的结果。
func TestPointScanDeviceOptionsMatchDriver(t *testing.T) {
	type option struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	var form struct {
		Rule []struct {
			Field   string   `json:"field"`
			Options []option `json:"options"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(pointScanForm), &form); err != nil {
		t.Fatalf("点位扫描表单不是合法 JSON: %v", err)
	}

	var opts []option
	for _, r := range form.Rule {
		if r.Field == "device" {
			opts = r.Options
		}
	}
	if len(opts) == 0 {
		t.Fatal("表单里没有 device 下拉（或它的 options 为空）")
	}
	// 顺序也一致：下拉顺序是刻意的（D 在前），两边一致说明没人手工重排过其中一份。
	if len(opts) != len(pointScanDevices) {
		t.Fatalf("表单有 %d 个软元件，pointScanDevices 有 %d 个", len(opts), len(pointScanDevices))
	}
	for i, o := range opts {
		if o.Value != pointScanDevices[i] {
			t.Errorf("第 %d 个选项 = %q，期望 %q", i, o.Value, pointScanDevices[i])
		}
		dev, ok := lookupDevice(o.Value)
		if !ok {
			t.Errorf("表单里的软元件 %q 不在驱动表 mcDevices 里 —— 选得到但扫不了", o.Value)
			continue
		}
		if got := strings.Contains(o.Label, "（位"); got != dev.isBit {
			t.Errorf("软元件 %s 的标签 %q 与驱动的分类不符（isBit=%v）—— "+
				"标签写着「位」而驱动按字处理，会让用户按位去理解一份字为单位的结果",
				dev.name, o.Label, dev.isBit)
		}
		if got := strings.Contains(o.Label, "八进制"); got != dev.octal {
			t.Errorf("软元件 %s 的标签 %q 与驱动的分类不符（octal=%v）—— "+
				"八进制标注错了，用户会照着十进制去填起始地址", dev.name, o.Label, dev.octal)
		}
	}
}

// TestPointDiscoverySpec 能力声明里几个决定前端行为的取值。
func TestPointDiscoverySpec(t *testing.T) {
	pd := PointDiscovery()
	if pd.Title == "" {
		t.Error("Title 为空 —— 弹窗会没有标题")
	}
	if pd.Description == "" {
		t.Error("Description 为空 —— 它是用户理解「这只是一份候选清单」的主要来源")
	}
	if !json.Valid(pd.FormJSON) {
		t.Error("点位扫描表单不是合法 JSON")
	}
	// 必须是**通用类型名**（TypeMap 的键）：写成内部裸名 "word" 会被 Meta.Validate 拒掉。
	if pd.DefaultCommonDataType != driver.TypeWord {
		t.Errorf("DefaultCommonDataType = %q，期望 %q", pd.DefaultCommonDataType, driver.TypeWord)
	}
	if _, ok := CommonTypeMap()[pd.DefaultCommonDataType]; !ok {
		t.Errorf("DefaultCommonDataType %q 不是 TypeMap 的键", pd.DefaultCommonDataType)
	}
	// 与实现里的常量必须一致：前者只是把它报给网关，后者才是真正的截断点。
	if pd.MaxPoints != mcPointScanMaxPoints {
		t.Errorf("MaxPoints = %d，与 mcPointScanMaxPoints(%d) 不一致，前端显示的条数上限会是错的",
			pd.MaxPoints, mcPointScanMaxPoints)
	}
	// 取值区间由 Meta.Validate 限死，越界会让插件启动即失败。
	if pd.MaxBudgetMs < 500 || pd.MaxBudgetMs > 30000 {
		t.Errorf("MaxBudgetMs = %d，超出 [500,30000]", pd.MaxBudgetMs)
	}
	if pd.MaxPoints < 1 || pd.MaxPoints > 20000 {
		t.Errorf("MaxPoints = %d，超出 [1,20000]", pd.MaxPoints)
	}
}

// TestPointScanZeroValueSemantics 零值点位的判据是**该点位自己的那一位**。
//
// 位粒度下不能复用"整字是否全 0"：同一个字里别的位为 1 时，本该显示 0 的点位
// 会被漏掉，「显示 0 值」这枚开关也就漏了点。
func TestPointScanZeroValueSemantics(t *testing.T) {
	// D100 = 0x0001：bit 0 为 1、其余 15 位为 0。
	read := func(head uint32, points uint16, bitMode bool) ([]byte, error) {
		out := make([]byte, int(points)*2)
		for i := 0; i < int(points); i++ {
			if head+uint32(i) == 100 {
				binary.LittleEndian.PutUint16(out[i*2:], 0x0001)
			}
		}
		return out, nil
	}
	opt := pointScanOptions{dev: mustDev("D"), base: 100, width: widthBit, count: 4}
	res, err := scanWith(read, opt, 100, time.Now().Add(3*time.Second))
	if err != nil {
		t.Fatalf("scanWith = %v", err)
	}
	if len(res.Points) != 4 {
		t.Fatalf("返回 %d 个点位，期望 4", len(res.Points))
	}
	if res.Points[0].Value != "1" {
		t.Errorf("D100.0 = %q，期望 1", res.Points[0].Value)
	}
	for i := 1; i < 4; i++ {
		if res.Points[i].Value != "" || res.Points[i].Note != mcPointScanZeroNote {
			t.Errorf("%s = %q / %q，期望零值（同一个字里别的位为 1 不该让这个点位显示成有值）",
				res.Points[i].Name, res.Points[i].Value, res.Points[i].Note)
		}
	}
}

// ==================== 小工具 ====================

func mustDev(name string) mcDevice {
	dev, ok := lookupDevice(name)
	if !ok {
		panic("测试引用了驱动表里没有的软元件: " + name)
	}
	return dev
}

func pointNames(res *driver.PointScanResult) []string {
	out := make([]string, 0, len(res.Points))
	for _, p := range res.Points {
		out = append(out, p.Name)
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
