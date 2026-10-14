// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package s7

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
	"iot-gateway/testutil/fake"
)

// scanDeadline 用例使用的截止时刻，远大于本地假设备的往返耗时，
// 但不足以让"预算耗尽提前返回"那条路径被意外触发。
func scanDeadline() time.Time { return time.Now().Add(10 * time.Second) }

// driverSession 把一台已 Connect 的驱动实例包成 SessionAccess。
//
// 这是生产路径的真实形态：S7 未实现 driver.ConnectionMatcher，因此
// driversdk.selectSession 对 S7 一律走**临时实例**（自己连一条 102 连接），
// 不会复用采集会话。
type driverSession struct{ drv driver.Driver }

func (s *driverSession) Do(fn func(drv driver.Driver) error) error { return fn(s.drv) }

// ==================== 地址往返（本文件最重要的一条） ====================

// TestPointNameRoundTrip 生成出来的地址串必须能被 ParseS7Address 还原回同一个位置。
//
// **这不是审美问题。** 一个"看起来正常但解析不出来"的地址写进 device_address
// 之后，这个点位会永远采不到值（planFor 在规划前就把它拆出去，只报
// QualityBadAddress / 报警「地址无效」），要改配置才恢复。
// 而扫描结果里的地址列在前端是只读的，用户根本没有机会在那里改。
//
// 覆盖全部区域 × 全部粒度，并逐项比对 Area/DB/ByteOffset/BitOffset/Width：
// 只比"解析成功"挡不住"DB 号丢了"或"位号串到字节偏移上"这类错位。
func TestPointNameRoundTrip(t *testing.T) {
	// DB 区与非 DB 区分别列：非 DB 区的 DB 号恒为 0（它没有 DB 这个概念）。
	zones := []struct {
		area s7Area
		db   int
	}{
		{S7AreaDB, 1}, {S7AreaDB, 2}, {S7AreaDB, 100}, {S7AreaDB, maxDB},
		{S7AreaM, 0}, {S7AreaI, 0}, {S7AreaQ, 0},
	}
	widths := []scanWidth{widthBit, widthByte, widthWord, widthDWord}
	offsets := []int{0, 1, 7, 8, 100, 4096, 1 << 20}
	bits := []int{0, 1, 3, 7}

	wantWidth := map[scanWidth]int{widthBit: 0, widthByte: 1, widthWord: 2, widthDWord: 4}

	for _, z := range zones {
		for _, w := range widths {
			for _, off := range offsets {
				for _, bit := range bits {
					if w != widthBit && bit != bits[0] {
						continue // 非位粒度没有位号，只跑一次
					}
					bitArg := -1
					if w == widthBit {
						bitArg = bit
					}

					name := formatPointName(z.area, z.db, off, bitArg, w)
					addr, ok := ParseS7Address(name)
					if !ok {
						t.Errorf("%q（区域 %#x db=%d off=%d bit=%d）解析不出来 —— "+
							"这个地址一旦建成点位，会永远采不到值（只报「地址无效」）", name, z.area, z.db, off, bit)
						continue
					}
					if addr.Area != z.area {
						t.Errorf("%q 的 Area = %#x，期望 %#x", name, addr.Area, z.area)
					}
					if addr.DB != z.db {
						t.Errorf("%q 的 DB = %d，期望 %d", name, addr.DB, z.db)
					}
					if addr.ByteOffset != off {
						t.Errorf("%q 的 ByteOffset = %d，期望 %d", name, addr.ByteOffset, off)
					}
					if w == widthBit {
						if addr.BitOffset != bit {
							t.Errorf("%q 的 BitOffset = %d，期望 %d", name, addr.BitOffset, bit)
						}
					} else if addr.BitOffset != -1 {
						t.Errorf("%q 不是位地址，BitOffset 应为 -1，实际 %d", name, addr.BitOffset)
					}
					if addr.Width != wantWidth[w] {
						t.Errorf("%q 的 Width = %d，期望 %d（%s）",
							name, addr.Width, wantWidth[w], w)
					}
				}
			}
		}
	}
}

// TestPointSpanCoversEveryPoint 每个点位都必须落在它所在块的读取缓冲区内。
//
// 这条不变量一旦破了，症状不是"值不对"而是**越界 panic** —— buildPoint 直接切
// buf[off:off+ub]，而 panic 会被 driversdk 的 handle recover 兜成一次 RPC 错误，
// 用户看到的是"这次扫描莫名失败"，看不出根因是 byteSpan 与 pointName 算岔了。
//
// 两者是分开写的（一个算偏移、一个算跨度），所以必须有一条测试把它们钉在一起。
func TestPointSpanCoversEveryPoint(t *testing.T) {
	cases := []pointScanOptions{
		{area: S7AreaDB, db: 1, start: 0, width: widthByte, count: 100},
		{area: S7AreaDB, db: 1, start: 0, width: widthWord, count: 100},
		{area: S7AreaM, start: 0, width: widthDWord, count: 100},
		{area: S7AreaM, start: 3, width: widthWord, count: 50}, // 起始偏移不整除单位宽度
		{area: S7AreaI, start: 5, width: widthBit, count: 100}, // 起始偏移不落在字节边界
		{area: S7AreaQ, start: 0, width: widthBit, count: 64},
		{area: S7AreaDB, db: 7, start: 1, width: widthBit, count: 33},
	}

	for _, opt := range cases {
		t.Run(fmt.Sprintf("area%02X-start%d-%s", byte(opt.area), opt.start, opt.width), func(t *testing.T) {
			for i := 0; i < opt.count; i++ {
				byteStart, byteLen := opt.byteSpan(i, 1)
				if byteLen <= 0 {
					t.Fatalf("第 %d 个点位算出长度 %d 的区间", i, byteLen)
				}

				absByte := opt.start + i*opt.width.unitBytes()
				if opt.isBit() {
					absByte = opt.start + i/8
				}
				off := absByte - byteStart
				if off < 0 || off+opt.width.unitBytes() > byteLen {
					t.Fatalf("第 %d 个点位（%s）落在读取区间 [%d,%d) 之外：off=%d",
						i, opt.pointName(i), byteStart, byteStart+byteLen, off)
				}
			}
			// 跨越块边界时也要成立：块内起点换成别的下标，同一个点位仍须落在区间内。
			for _, chunk := range []struct{ start, n int }{{0, 512}, {7, 100}, {512, 512}} {
				for i := chunk.start; i < chunk.start+chunk.n && i < opt.count; i++ {
					byteStart, byteLen := opt.byteSpan(chunk.start, chunk.n)
					absByte := opt.start + i*opt.width.unitBytes()
					if opt.isBit() {
						absByte = opt.start + i/8
					}
					if off := absByte - byteStart; off < 0 || off+opt.width.unitBytes() > byteLen {
						t.Fatalf("块 [%d,+%d) 里第 %d 个点位超出读取区间", chunk.start, chunk.n, i)
					}
				}
			}
		})
	}
}

// ==================== 逐点取值 ====================

// patternRead 按字节偏移填充可预测的数据：buf[i] = (byteStart+i) & 0xff。
//
// 用它就能断言"第 idx 个点位读到的确实是它自己那一段字节"，而不只是"读到了"——
// 字节序、字偏移、位偏移任一处算错都会让下面的期望值对不上。
func patternRead(byteStart int, buf []byte) error {
	for i := range buf {
		buf[i] = byte((byteStart + i) & 0xff)
	}
	return nil
}

// TestScanPointsWordValues 字模式下每个点位取的是它自己那 2 个字节，大端解析。
func TestScanPointsWordValues(t *testing.T) {
	opt := pointScanOptions{area: S7AreaDB, db: 1, start: 0, width: widthWord, count: 4}
	res := scanOrFail(t, opt, patternRead)

	want := []struct{ name, value string }{
		{"DB1.DBW0", "1"},    // 00 01
		{"DB1.DBW2", "515"},  // 02 03
		{"DB1.DBW4", "1029"}, // 04 05
		{"DB1.DBW6", "1543"}, // 06 07
	}
	assertPoints(t, res, want)
	if res.AssumedCommonDataType != driver.TypeWord {
		t.Errorf("AssumedCommonDataType = %q，期望 %q", res.AssumedCommonDataType, driver.TypeWord)
	}
	if res.Scanned != 4 || res.Readable != 4 || res.Unreadable != 0 {
		t.Errorf("统计 = %d/%d/%d，期望 4/4/0", res.Scanned, res.Readable, res.Unreadable)
	}
}

// TestScanPointsBitValues 位模式按位取，且位号必须落在正确的位上。
func TestScanPointsBitValues(t *testing.T) {
	// 空出前 8 个字节，让"第 8 个点位"落在第 1 个字节的 0 号位上：
	// 若位号算错（比如恒取 0 号位、或把位号当成了字节偏移），这一条立刻报错。
	opt := pointScanOptions{area: S7AreaM, start: 0, width: widthBit, count: 16}
	res := scanOrFail(t, opt, func(byteStart int, buf []byte) error {
		buf[0] = 0x00 // 第 0 个字节全零 → 前 8 位全是零值
		for i := 1; i < len(buf); i++ {
			buf[i] = 0b1000_0001 // 第 1 个字节：只有 0 号位与 7 号位为 1
		}
		return nil
	})

	if len(res.Points) != 16 {
		t.Fatalf("应有 16 个点位，实际 %d", len(res.Points))
	}
	if res.Points[0].Name != "M0.0" || res.Points[7].Name != "M0.7" {
		t.Errorf("前 8 个点位名 = %q..%q，期望 M0.0..M0.7", res.Points[0].Name, res.Points[7].Name)
	}
	if res.Points[8].Name != "M1.0" || res.Points[15].Name != "M1.7" {
		t.Errorf("后 8 个点位名 = %q..%q，期望 M1.0..M1.7", res.Points[8].Name, res.Points[15].Name)
	}
	for i := 0; i < 8; i++ {
		if res.Points[i].Value != "" || res.Points[i].Note != pointScanZeroNote {
			t.Errorf("%s 的位为 0，应为零值点位", res.Points[i].Name)
		}
	}
	// 布尔按本包 formatBool 渲染成 "1"/"0"（不是 "true"/"false"）—— 与现场
	// 时序库里存的值一致，改成别的写法会让历史数据与新数据对不上。
	if res.Points[8].Value != "1" {
		t.Errorf("M1.0 的值 = %q，期望 1（该字节的 0 号位为 1）", res.Points[8].Value)
	}
	if res.Points[15].Value != "1" {
		t.Errorf("M1.7 的值 = %q，期望 1（该字节的 7 号位为 1）", res.Points[15].Value)
	}
	for i := 9; i < 15; i++ {
		if res.Points[i].Value != "" {
			t.Errorf("%s 的位为 0，不该有值，实际 %q", res.Points[i].Name, res.Points[i].Value)
		}
	}
	if res.AssumedCommonDataType != driver.TypeBoolean {
		t.Errorf("位扫描的 AssumedCommonDataType = %q，期望 %q",
			res.AssumedCommonDataType, driver.TypeBoolean)
	}
}

// TestScanPointsByteAndDWordNames 字节/双字两种粒度的地址写法。
func TestScanPointsByteAndDWordNames(t *testing.T) {
	byteRes := scanOrFail(t,
		pointScanOptions{area: S7AreaI, start: 10, width: widthByte, count: 3}, patternRead)
	assertPoints(t, byteRes, []struct{ name, value string }{
		{"IB10", "10"}, {"IB11", "11"}, {"IB12", "12"},
	})

	dwordRes := scanOrFail(t,
		pointScanOptions{area: S7AreaQ, start: 0, width: widthDWord, count: 2}, patternRead)
	assertPoints(t, dwordRes, []struct{ name, value string }{
		{"QD0", "66051"},    // 00 01 02 03
		{"QD4", "67438087"}, // 04 05 06 07
	})
}

// TestScanPointsZeroValuesAreListed 全零的范围也必须逐条列出来。
//
// 插件侧刻意不做零值过滤（过滤是前端的开关）：对所有地址都是 0 的设备，
// 这里过滤会返回一份空列表，用户据此得出"这里没有点位"——与"没扫到"同型的错误。
func TestScanPointsZeroValuesAreListed(t *testing.T) {
	opt := pointScanOptions{area: S7AreaDB, db: 1, start: 0, width: widthWord, count: 8}
	res := scanOrFail(t, opt, func(byteStart int, buf []byte) error { return nil })

	if len(res.Points) != 8 || res.Readable != 8 {
		t.Fatalf("全零的范围也应逐条列出，实际 %d 条 / readable=%d", len(res.Points), res.Readable)
	}
	for _, p := range res.Points {
		if p.Value != "" {
			t.Errorf("%s 全零不该有值，实际 %q", p.Name, p.Value)
		}
		if p.Note != pointScanZeroNote {
			t.Errorf("%s 全零应带上说明 %q，实际 %q", p.Name, pointScanZeroNote, p.Note)
		}
	}
}

// ==================== 对半降级与中止 ====================

// readableUntil 只有整个请求都落在 [0, readableTo) 内才成功，否则报"对象不存在"。
func readableUntil(readableTo int) readFn {
	return func(byteStart int, buf []byte) error {
		if byteStart+len(buf) > readableTo {
			return errors.New("CPU : Item not available")
		}
		return patternRead(byteStart, buf)
	}
}

// TestScanPointsDegradesAroundHole 范围里有一段读不到时，对半降级要把空洞精确摘出来。
//
// S7 的 PLC 对含"不存在"地址的**整个请求**会整段拒绝，所以不存在"部分应答"——
// 不一路拆到单点，就只能得到"整段都读不到"这种没有信息量的结论。
func TestScanPointsDegradesAroundHole(t *testing.T) {
	// 只有 [0,6) 读得到，却按要求扫 24 个字节。
	opt := pointScanOptions{area: S7AreaDB, db: 1, start: 0, width: widthByte, count: 24}
	res := scanOrFail(t, opt, readableUntil(6))

	if res.Scanned != 24 {
		t.Errorf("Scanned = %d，期望 24（范围内的每个地址都要被尝试）", res.Scanned)
	}
	if res.Readable != 6 || res.Unreadable != 18 {
		t.Errorf("统计 = readable %d / unreadable %d，期望 6/18 —— "+
			"降级要把空洞逐点摘出来，而不是整段记成读不到", res.Readable, res.Unreadable)
	}
	if len(res.Points) != 6 {
		t.Fatalf("应列出 6 个可读点位，实际 %d", len(res.Points))
	}
	for i, p := range res.Points {
		want := fmt.Sprintf("DB1.DBB%d", i)
		if p.Name != want {
			t.Errorf("第 %d 个点位 = %q，期望 %q", i, p.Name, want)
		}
	}
	if res.Truncated || res.Incomplete {
		t.Errorf("这不是被截断也不是未扫完: %+v", res)
	}
}

// TestScanPointsAbortsOnConnErr 连接类错误必须**中止**，绝不能被记成"地址不存在"。
//
// 把设备掉线记成"这里没有点位"，用户会从一份几乎全是空的清单里得出与事实完全
// 相反的结论，而采集侧此时已经在重连了 —— 两边的结论互相矛盾。
func TestScanPointsAbortsOnConnErr(t *testing.T) {
	// 第一个块（512 字节）成功，第二个块开始掉线。
	const firstChunk = pointScanChunkBytes
	read := func(byteStart int, buf []byte) error {
		if byteStart >= firstChunk {
			return &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}
		}
		return patternRead(byteStart, buf)
	}

	opt := pointScanOptions{
		area: S7AreaDB, db: 1, start: 0, width: widthByte,
		count: firstChunk + 8,
	}
	res, err := scanWith(read, opt, scanDeadline())
	if err == nil {
		t.Fatal("连接中断必须报错，否则用户会把「设备掉线」读成「这里没有点位」")
	}
	if !strings.Contains(err.Error(), "连接中断") {
		t.Errorf("错误文案应说清是连接问题，实际 %v", err)
	}
	// 已经扫到的部分必须一并返回：丢掉它们对用户没有任何好处。
	if res == nil || res.Readable != firstChunk {
		t.Fatalf("应带上已扫到的 %d 个点位，实际 %+v", firstChunk, res)
	}
	if res.Unreadable != 0 {
		t.Errorf("连接中断不该被记成 unreadable，实际 %d", res.Unreadable)
	}
}

// TestIsUnreadableAddrErr 错误分类的白名单与黑名单。
//
// 分类错在哪个方向都有明确的坏结果：
//   - 把连接错误判成"地址不存在" → 用户拿到一份假的"读不到"清单；
//   - 把"地址不存在"判成中止 → 一份本来能扫出东西的清单变成一个错误。
//
// 后者是 S7-1200/1500 上的主要风险来源：它们的项级错误码（0x810A/0x8104 等）
// 经 gos7 的 CPUError 映射后落进"function refused by CPU"这个兜底桶，
// 必须被认成可降级的地址错误，否则那两类设备根本扫不了。
func TestIsUnreadableAddrErr(t *testing.T) {
	unreadable := []string{
		"CPU : Address out of range",
		"CPU : Item not available",
		"CPU : Function not available",
		"CLI : function refused by CPU (Unknown error)",
		"S7: exception (address invalid. This may be due to a memory address that is not valid for the PLC)'",
		"S7: exception (object doesn’t exist. This may be due to a data block that doesn’t exist in the PLC)'",
	}
	for _, msg := range unreadable {
		if !isUnreadableAddrErr(errors.New(msg)) {
			t.Errorf("%q 应被判为「地址读不到」", msg)
		}
	}

	abort := []error{
		nil,
		&net.OpError{Op: "read", Err: errors.New("i/o timeout")},
		&net.OpError{Op: "dial", Err: errors.New("connect: connection refused")},
		errors.New("ISO : Connection Error"),
		errors.New("CLI : Error in PDU negotiation"),
		errors.New("s7: 设备未连接，无法扫描"),
	}
	for _, err := range abort {
		if isUnreadableAddrErr(err) {
			t.Errorf("%v 不该被判为「地址读不到」—— 它会把设备掉线记成点位不存在", err)
		}
	}
}

// ==================== 选项解析 ====================

// TestScanPointsStopsAtRequestQuota 整段都读不到时必须在**请求数上限**处收手。
//
// 对半降级在一段完全读不到的区间上会退化成一棵满二叉树（最坏 2n-1 次请求）。
// 光靠 deadline 止损是不够的：耗时随网络延迟漂移 —— 本地 PLC 上几百次请求可能
// 只要几百毫秒（于是照样跑完），慢链路又跑不完，而超预算会撞网关侧的 RPC 超时
// 并累积插件卡死计数。请求数上限给的是**确定性**的上界。
func TestScanPointsStopsAtRequestQuota(t *testing.T) {
	opt := pointScanOptions{
		area: S7AreaDB, db: 1, start: 0, width: widthByte,
		count: 4 * pointScanChunkBytes, // 四块，每块都整块读不到
	}

	calls := 0
	read := func(byteStart int, buf []byte) error {
		calls++
		return errors.New("CPU : Item not available")
	}

	res, err := scanWith(read, opt, scanDeadline())
	if err != nil {
		t.Fatalf("整段读不到不是错误（它是「这里没有点位」的合法答案）: %v", err)
	}
	if !res.Incomplete {
		t.Error("触发请求数上限必须置 Incomplete —— 否则用户会以为整段都扫过了")
	}
	if res.Truncated {
		t.Error("这是「请求数用尽」而不是「结果条数到上限」，不该置 Truncated")
	}
	if calls > pointScanMaxRequests {
		t.Errorf("发了 %d 次请求，超过上限 %d —— "+
			"对半降级必须有一条确定性的上界", calls, pointScanMaxRequests)
	}
	if res.Readable != 0 || len(res.Points) != 0 {
		t.Errorf("全读不到不该有可读点位，实际 readable=%d points=%d", res.Readable, len(res.Points))
	}
	// 不变量：Readable + Unreadable == Scanned 在未扫完时仍须成立（只是统计的是已完成部分）。
	if res.Readable+res.Unreadable != res.Scanned {
		t.Errorf("统计不自洽：%d + %d != %d", res.Readable, res.Unreadable, res.Scanned)
	}
}

// TestScanPointsTruncatesAtMaxPoints 结果条数到上限即停止，并精确截断。
//
// 上限存在的意义不是性能而是**事故半径**：pluginwire 写帧持插件进程的写锁，
// 一份几百 KB 的应答会把该进程上所有协议的应答一起头阻塞。
//
// "精确"指的是：达到上限时立刻停手，超出的地址既不计入 Scanned/Readable，
// 也不进列表 —— 否则"可读到 M 个"与列表长度会对不上，用户看到的是一份
// 自相矛盾的结果。
func TestScanPointsTruncatesAtMaxPoints(t *testing.T) {
	opt := pointScanOptions{
		area: S7AreaM, start: 0, width: widthWord,
		count: pointScanMaxPoints + 100,
	}
	res := scanOrFail(t, opt, patternRead)

	if !res.Truncated {
		t.Fatal("超过 MaxPoints 必须置 Truncated —— 后面的地址根本没试，与「读不到」是两回事")
	}
	if len(res.Points) != pointScanMaxPoints {
		t.Errorf("点位条数 = %d，期望正好停在 %d", len(res.Points), pointScanMaxPoints)
	}
	// 截断处之后的地址根本没被尝试，不该计入任何统计。
	if res.Scanned != pointScanMaxPoints || res.Readable != pointScanMaxPoints {
		t.Errorf("统计 = scanned %d / readable %d，期望都是 %d（超出的部分不该计入）",
			res.Scanned, res.Readable, pointScanMaxPoints)
	}
	if res.Unreadable != 0 {
		t.Errorf("Unreadable = %d，期望 0", res.Unreadable)
	}
	if res.Incomplete {
		t.Error("这是「结果条数到上限」而不是「预算/请求数用尽」，不该置 Incomplete")
	}
}

// TestParsePointScanOptions 选项解析：缺项/非法一律报错，而不是猜一个默认值。
//
// "扫多少"没有合理的默认值 —— 猜一个 64 会让用户拿到一份他并没有要求的清单。
func TestParsePointScanOptions(t *testing.T) {
	t.Run("空选项报错", func(t *testing.T) {
		if _, err := parsePointScanOptions(nil); err == nil {
			t.Fatal("空选项应报错，而不是拿一份默认范围的清单去扫")
		}
	})

	t.Run("缺数量报错", func(t *testing.T) {
		if _, err := parsePointScanOptions(json.RawMessage(`{"area":"DB","count":0}`)); err == nil {
			t.Fatal("点位数 <= 0 应报错")
		}
	})

	t.Run("非法区域/粒度报错", func(t *testing.T) {
		for _, raw := range []string{
			`{"area":"T","dbNumber":1,"count":4}`,
			`{"area":"DB","width":"real","count":4}`,
		} {
			if _, err := parsePointScanOptions(json.RawMessage(raw)); err == nil {
				t.Errorf("%s 应报错", raw)
			}
		}
	})

	t.Run("DB 号越界报错", func(t *testing.T) {
		if _, err := parsePointScanOptions(json.RawMessage(`{"area":"DB","dbNumber":0,"count":4}`)); err == nil {
			t.Fatal("DB 号 0 应报错")
		}
		if _, err := parsePointScanOptions(json.RawMessage(`{"area":"DB","dbNumber":65536,"count":4}`)); err == nil {
			t.Fatal("DB 号 65536 应报错")
		}
	})

	t.Run("非 DB 区忽略 DB 号", func(t *testing.T) {
		// M/I/Q 区没有 DB 的概念。配置里残留的 DB 号若被带过去，S7Range.DB 会非零，
		// 于是同一个 DB 号出现在 M 区的区间里 —— 一个无声的不一致。
		opt, err := parsePointScanOptions(json.RawMessage(`{"area":"M","dbNumber":7,"startOffset":0,"width":"byte","count":4}`))
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if opt.db != 0 {
			t.Errorf("M 区的 db = %d，期望 0", opt.db)
		}
	})

	t.Run("字符串形态的数值也接受", func(t *testing.T) {
		// form-create 的部分控件产出的是字符串（"64"）。声明成 int 会让
		// json.Unmarshal 整体报错 —— 不是那一个字段失效，而是整次扫描全失败。
		opt, err := parsePointScanOptions(json.RawMessage(
			`{"area":"DB","dbNumber":"2","startOffset":"100","width":"word","count":"8"}`))
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if opt.db != 2 || opt.start != 100 || opt.count != 8 || opt.width != widthWord {
			t.Errorf("解析结果 = %+v", opt)
		}
	})

	t.Run("数量越过 24 位地址上限时截断", func(t *testing.T) {
		opt, err := parsePointScanOptions(json.RawMessage(
			`{"area":"DB","dbNumber":1,"startOffset":2097000,"width":"dword","count":1000}`))
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if want := (maxS7ByteAddr - 2097000) / 4; opt.count != want {
			t.Errorf("count = %d，期望截断到 %d", opt.count, want)
		}
		if _, byteLen := opt.byteSpan(0, opt.count); opt.start+byteLen > maxS7ByteAddr {
			t.Errorf("截断后仍越界：起始 %d + 长度 %d > %d", opt.start, byteLen, maxS7ByteAddr)
		}
	})

	t.Run("起始偏移已在地址空间之外时报错", func(t *testing.T) {
		if _, err := parsePointScanOptions(json.RawMessage(
			`{"area":"DB","dbNumber":1,"startOffset":2097151,"count":4}`)); err == nil {
			t.Fatal("起始偏移之后已无地址空间，应报错而不是给一份空清单")
		}
	})
}

// ==================== 端到端 ====================

// TestScanPointsEndToEnd 打一台真实的 S7 假 PLC，走完整链路：
// 装驱动 → Connect（TPKT/COTP + PDU 协商）→ ScanPoints → 逐点读取。
//
// fake.NewS7 一律回全零数据，因此这里覆盖的是"全部读得到、全部为零值"这条路径 ——
// 与 Modbus 的 TestScanPointsZeroValuesAreReturned 对应。
func TestScanPointsEndToEnd(t *testing.T) {
	srv, err := fake.NewS7()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	drv := connectFake(t, srv.Addr())

	res, err := ScanPoints(&driverSession{drv: drv},
		json.RawMessage(`{"area":"DB","dbNumber":1,"startOffset":0,"width":"word","count":8}`),
		scanDeadline())
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}
	if len(res.Points) != 8 || res.Readable != 8 {
		t.Fatalf("应有 8 个可读点位，实际 %d 条 / readable=%d", len(res.Points), res.Readable)
	}
	if res.Points[0].Name != "DB1.DBW0" || res.Points[7].Name != "DB1.DBW14" {
		t.Errorf("地址 = %q..%q，期望 DB1.DBW0..DB1.DBW14",
			res.Points[0].Name, res.Points[7].Name)
	}
	for _, p := range res.Points {
		if p.Value != "" || p.Note != pointScanZeroNote {
			t.Errorf("%s 全零应只带零值说明，实际 value=%q note=%q", p.Name, p.Value, p.Note)
		}
	}
	if res.ElapsedMs < 0 {
		t.Errorf("ElapsedMs = %d", res.ElapsedMs)
	}
}

// TestScanPointsRequiresConnection 设备没连上时必须给一条明确的报错。
//
// 预检存在的意义：一条"设备未连接，无法扫描"远比一份空清单有用 ——
// 空清单会被读成"这台设备没有点位"。
func TestScanPointsRequiresConnection(t *testing.T) {
	drv := NewDriver() // 从未 Connect
	_, err := ScanPoints(&driverSession{drv: drv},
		json.RawMessage(`{"area":"DB","dbNumber":1,"startOffset":0,"width":"word","count":4}`),
		scanDeadline())
	if err == nil {
		t.Fatal("未连接时应报错")
	}
	if !strings.Contains(err.Error(), "未连接") {
		t.Errorf("错误文案应说清是连接问题，实际 %v", err)
	}
}

// TestScanPointsRejectsForeignDriver 传进来的不是 S7 驱动时如实报错。
//
// 生产路径下不会发生（finder 由协议名派发），但这条守卫挡住的是"未来有人把
// S7 的 finder 挂到别的协议上"——那时 scanSessionOf 的类型断言会失败，
// 报出来的是"不支持的驱动类型"而不是一次 nil 解引用 panic。
func TestScanPointsRejectsForeignDriver(t *testing.T) {
	_, err := ScanPoints(&driverSession{drv: otherDriver{}},
		json.RawMessage(`{"area":"DB","dbNumber":1,"startOffset":0,"width":"word","count":4}`),
		scanDeadline())
	if err == nil || !strings.Contains(err.Error(), "不支持的驱动类型") {
		t.Fatalf("期望「不支持的驱动类型」，实际 %v", err)
	}
}

// ==================== 测试辅助 ====================

// otherDriver 一个与 S7 无关的驱动，只为驱动 scanSessionOf 的类型守卫。
type otherDriver struct{}

func (otherDriver) Connect(string) error                                 { return nil }
func (otherDriver) Ping(string) error                                    { return nil }
func (otherDriver) Read([]po.DeviceAddress) ([]driver.ReadResult, error) { return nil, nil }
func (otherDriver) IsConnected() bool                                    { return true }
func (otherDriver) Close() error                                         { return nil }

// scanOrFail 用注入的读取原语跑一次扫描，要求不报错。
func scanOrFail(t *testing.T, opt pointScanOptions, read readFn) *driver.PointScanResult {
	t.Helper()
	res, err := scanWith(read, opt, scanDeadline())
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if res == nil {
		t.Fatal("scanWith 返回了 nil 结果")
	}
	return res
}

// assertPoints 逐项比对地址与值。
func assertPoints(t *testing.T, res *driver.PointScanResult, want []struct{ name, value string }) {
	t.Helper()
	if len(res.Points) != len(want) {
		t.Fatalf("点位条数 = %d，期望 %d（%+v）", len(res.Points), len(want), res.Points)
	}
	for i, w := range want {
		if res.Points[i].Name != w.name {
			t.Errorf("第 %d 个点位名 = %q，期望 %q", i, res.Points[i].Name, w.name)
		}
		if res.Points[i].Value != w.value {
			t.Errorf("%s 的值 = %q，期望 %q", w.name, res.Points[i].Value, w.value)
		}
	}
}

// connectFake 装一台指向假 PLC 的 S7 驱动。
//
// 配置里的 rack/slot 刻意写成**字符串**：s7ConfigRaw 的这两个字段就是 string，
// 写成数字会被静默丢弃 —— 与探测器生成的 ProtocolJSON 必须保持一致。
func connectFake(t *testing.T, addr string) driver.Driver {
	t.Helper()
	host, port := hostPortOf(t, addr)

	drv := NewDriver()
	cfg := fmt.Sprintf(`{"host":%q,"port":%d,"rack":"0","slot":"1"}`, host, port)
	if err := drv.Connect(cfg); err != nil {
		t.Fatalf("连接假 PLC: %v", err)
	}
	t.Cleanup(func() { _ = drv.Close() })
	return drv
}
