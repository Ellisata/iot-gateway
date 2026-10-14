// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"iot-gateway/driver"
	"iot-gateway/model/po"
	"iot-gateway/testutil/fake"
)

// pointScanDeadline 用例里给扫描的截止时间。远大于本地假设备所需，
// 因此它只在"必须靠 deadline 止损"的那几条路径上起作用。
func pointScanDeadline() time.Time { return time.Now().Add(10 * time.Second) }

// ==================== 地址写法 ====================

// TestPointNameRoundTrip 生成的地址写法必须能被 ParseFINSAddress **逐字解析回
// 同一个位置**。
//
// 这条不变量比它看起来重要得多：一个解析不出来的地址写进 device_address 之后，
// 这个点位永远采不到值（planFor 在规划前就把它拆出去，只报 QualityBadAddress）。
// 四种内存区 × 三种粒度都要覆盖。
func TestPointNameRoundTrip(t *testing.T) {
	areas := []struct {
		area finsArea
		name string
	}{
		{AreaDM, "DM"},
		{AreaCIO, "CIO"},
		{AreaWR, "WR"},
		{AreaHR, "HR"},
	}

	for _, a := range areas {
		for _, width := range []scanWidth{widthWord, widthDWord, widthBit} {
			for _, start := range []uint16{0, 1, 15, 100, 65530} {
				t.Run(fmt.Sprintf("%s/%s/%d", a.name, width, start), func(t *testing.T) {
					opt := pointScanOptions{area: a.area, startWord: start, width: width, count: 8}
					// 与生产路径一样按地址空间上限截断：从 65530 起按字扫，
					// 后面几个点位本来就落在 0xFFFF 之外，不该生成地址。
					if n := opt.maxCount(); opt.count > n {
						opt.count = n
					}
					for idx := 0; idx < opt.count; idx++ {
						name := opt.pointName(idx)
						got, ok := ParseFINSAddress(name)
						if !ok {
							t.Fatalf("第 %d 个点位的地址 %q 解析不出来 —— "+
								"它写进 device_address 会永远采不到值（只报「地址无效」）", idx, name)
						}
						if got.Area != a.area {
							t.Errorf("%q 解析出内存区 0x%02X，期望 0x%02X", name, got.Area, a.area)
						}
						if want := opt.wordOfPoint(idx); got.Word != want {
							t.Errorf("%q 解析出字地址 %d，期望 %d", name, got.Word, want)
						}
						if width == widthBit {
							if got.Bit != idx%16 {
								t.Errorf("%q 解析出位号 %d，期望 %d", name, got.Bit, idx%16)
							}
						} else if got.IsBit() {
							t.Errorf("%q 是非位地址，却被解析成位访问", name)
						}
					}
				})
			}
		}
	}
}

// wordOfPoint 测试用的期望字地址，与 pointName 的实现分开写一份 ——
// 同源派生的话这条测试就只是在自证。
func (o pointScanOptions) wordOfPoint(idx int) uint16 {
	if o.width == widthBit {
		return o.startWord + uint16(idx/16)
	}
	return o.startWord + uint16(idx*o.width.unitWords())
}

// TestWordSpanCoversEveryPoint 字区间必须精确覆盖 [start, start+n) 的每一个点位。
//
// 位模式最容易错：对半降级会把起点切到字中间（例如从第 5 位开始扫 8 个位），
// 此时"用 idx×unitWords 算偏移"会落到别的字上，读到的是**别处**的数据 ——
// 而值看着完全合理，用户不会发现。
func TestWordSpanCoversEveryPoint(t *testing.T) {
	for _, width := range []scanWidth{widthWord, widthDWord, widthBit} {
		opt := pointScanOptions{area: AreaDM, startWord: 100, width: width}
		// 覆盖所有可能的起点/长度组合（位模式下起点落在字中间的情形由此出现）。
		for start := 0; start < 33; start++ {
			for n := 1; n <= 33; n++ {
				offset, words := width.wordSpan(start, n)
				if words <= 0 {
					t.Fatalf("%s: wordSpan(%d,%d) = (%d,%d)", width, start, n, offset, words)
				}
				// 区间必须恰好包住首尾两个点位所在的字。
				first := opt.wordOfPointAt(offset, 0)
				last := opt.wordOfPointAt(offset, n-1)
				if first < uint16(offset) || last > uint16(offset+words-1) {
					t.Fatalf("%s: [%d,%d) 的字区间 [%d,%d) 没包住点位 %d..%d",
						width, start, start+n, offset, offset+words, first, last)
				}
				// 起点必须恰好落在第一个点位所在的字上：往前多读一个字，
				// 越界撞上"不存在的地址"的概率就大一分，而一次越界会让整段被拒。
				// （末尾不能这么断言：双字点位从最后一个字**起**，它的数据还要
				// 往后占一个字，区间本来就该比最后一个点位的起始字长。）
				if first != uint16(offset) {
					t.Fatalf("%s: [%d,%d) 的字区间从 %d 起，但第一个点位在 %d ——"+
						"多读的字会让整段更容易被设备拒绝", width, start, start+n, offset, first)
				}
			}
		}
	}
}

// wordOfPointAt 在给定的字偏移基准下，第 idx 个点位所在的字（相对 opt.startWord）。
func (o pointScanOptions) wordOfPointAt(offset, idx int) uint16 {
	if o.width == widthBit {
		return uint16(offset + idx/16)
	}
	return uint16(offset + idx*o.width.unitWords())
}

func TestScanWidthConvert(t *testing.T) {
	cases := []struct {
		width  scanWidth
		words  int
		points int
	}{
		{widthWord, 1, 1},
		{widthDWord, 2, 1},
		{widthBit, 1, 16},
	}
	for _, c := range cases {
		if offset, got := c.width.wordSpan(0, c.points); got != c.words || offset != 0 {
			t.Errorf("%s：%d 个点位应占 %d 字（从 0 起），实际 (%d,%d)",
				c.width, c.points, c.words, offset, got)
		}
		if got := c.width.pointsFor(c.words); got != c.points {
			t.Errorf("%s：%d 个字应有 %d 个点位，实际 %d", c.width, c.words, c.points, got)
		}
	}

	// 位模式下起点落在字中间：偏移必须落在**那个字**上，而不是用 idx×unitWords 算。
	if offset, words := widthBit.wordSpan(5, 8); offset != 0 || words != 1 {
		t.Errorf("位模式从第 5 位起扫 8 位 = (%d,%d)，期望 (0,1)（都落在第 0 个字里）", offset, words)
	}
	if offset, words := widthBit.wordSpan(15, 2); offset != 0 || words != 2 {
		t.Errorf("位模式从第 15 位起扫 2 位 = (%d,%d)，期望 (0,2)（跨两个字）", offset, words)
	}
}

// ==================== 选项解析 ====================

func TestParsePointScanOptions(t *testing.T) {
	cases := []struct {
		name    string
		options string
		area    finsArea
		start   uint16
		width   scanWidth
		count   int
		wantErr bool
	}{
		{"空选项报错", ``, 0, 0, "", 0, true},
		{"最小可用", `{"count":8}`, AreaDM, 0, widthWord, 8, false},
		{"完整", `{"area":"CIO","startWord":10,"width":"bit","count":32}`,
			AreaCIO, 10, widthBit, 32, false},
		// 内存区/粒度大小写不敏感：表单给的是大写，protocol_json 里可能存着别的写法。
		{"小写区名", `{"area":"dm","count":1}`, AreaDM, 0, widthWord, 1, false},
		{"短写区名", `{"area":"W","count":1}`, AreaWR, 0, widthWord, 1, false},
		{"大写粒度", `{"width":"DWORD","count":1}`, AreaDM, 0, widthDWord, 1, false},
		// form-create 的 select 控件产出字符串，数值字段必须容错。
		{"数值是字符串", `{"startWord":"100","count":"7"}`, AreaDM, 100, widthWord, 7, false},
		{"区名非法", `{"area":"XYZ","count":1}`, 0, 0, "", 0, true},
		{"粒度非法", `{"width":"byte","count":1}`, 0, 0, "", 0, true},
		{"点位数缺失", `{"area":"DM"}`, 0, 0, "", 0, true},
		{"点位数非正", `{"count":0}`, 0, 0, "", 0, true},
		{"起始越界", `{"startWord":70000,"count":1}`, 0, 0, "", 0, true},
		{"选项不是JSON", `{`, 0, 0, "", 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var raw json.RawMessage
			if c.options != "" {
				raw = json.RawMessage(c.options)
			}
			opt, err := parsePointScanOptions(raw)
			if c.wantErr {
				if err == nil {
					t.Fatal("应报错")
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错: %v", err)
			}
			if opt.area != c.area || opt.startWord != c.start || opt.width != c.width || opt.count != c.count {
				t.Errorf("解析结果 = %+v，期望 area=0x%02X start=%d width=%s count=%d",
					opt, c.area, c.start, c.width, c.count)
			}
		})
	}
}

// TestParsePointScanOptionsClampsCount 越过地址空间上限时**截断而不是报错**。
//
// 用户的意图很清楚：就是要扫到能扫的末尾。这与 s7drv/modbusdrv 的处理一致。
func TestParsePointScanOptionsClampsCount(t *testing.T) {
	opt, err := parsePointScanOptions(json.RawMessage(`{"startWord":65535,"count":1000}`))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if opt.count != 1 {
		t.Errorf("count = %d，期望 1（从 65535 起只剩一个字）", opt.count)
	}

	// 起点已在地址空间之外：没有可扫的地址，如实报错。
	if _, err := parsePointScanOptions(json.RawMessage(`{"startWord":65535,"width":"dword","count":10}`)); err == nil {
		t.Error("从最后一个字起按双字扫，没有可用地址空间，应报错")
	}
}

// ==================== 扫描：注入读取原语 ====================

// scriptedReader 按字地址回放预置数据的读取原语。
//
// script[start] 给出从该字起的内容；未命中的地址返回 unreadable（默认）。
// 用它把"哪里读到、哪里读不到"变成可精确编排的输入，从而驱动对半降级、
// 结尾截断、请求数上限这几条最容易写错的路径。
type scriptedReader struct {
	words      map[uint16][]byte
	unreadable map[uint16]bool // 该字起不可读
	err        error           // 非 nil 时所有读取都返回它（模拟链路故障）
	// oneWordOnly 模拟"只接受单字读取"的设备：一次要多个字的请求一律被拒。
	// 它逼着对半降级一路拆到单点，是唯一能稳定把请求数顶到上限的形态。
	oneWordOnly bool
	calls       []uint16
}

func (r *scriptedReader) read(word uint16, words int) ([]byte, error) {
	r.calls = append(r.calls, word)
	if r.err != nil {
		return nil, r.err
	}
	if r.oneWordOnly && words > 1 {
		return nil, newEndCodeError(0x0401)
	}
	// 只要区间里有一个字不可读，整段就被拒（FINS 的语义）。
	for i := 0; i < words; i++ {
		if r.unreadable[word+uint16(i)] {
			return nil, newEndCodeError(0x0401)
		}
		if _, ok := r.words[word+uint16(i)]; !ok {
			return nil, newEndCodeError(0x0401)
		}
	}
	out := make([]byte, 0, words*2)
	for i := 0; i < words; i++ {
		out = append(out, r.words[word+uint16(i)]...)
	}
	return out, nil
}

// scripted 构造一个从 start 起连续 n 个字都可读、其余不可读的脚本。
func scripted(start uint16, data [][]byte) *scriptedReader {
	r := &scriptedReader{words: map[uint16][]byte{}, unreadable: map[uint16]bool{}}
	for i, d := range data {
		r.words[start+uint16(i)] = d
	}
	return r
}

func wordBytes(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func scanOpts(t *testing.T, options string) pointScanOptions {
	t.Helper()
	opt, err := parsePointScanOptions(json.RawMessage(options))
	if err != nil {
		t.Fatalf("解析选项: %v", err)
	}
	return opt
}

// TestScanPointsWordValues 字模式：每个点位一个字，值按大端解码。
func TestScanPointsWordValues(t *testing.T) {
	r := scripted(100, [][]byte{wordBytes(0x1234), wordBytes(0x0000), wordBytes(0xFFFF)})
	opt := scanOpts(t, `{"area":"DM","startWord":100,"width":"word","count":3}`)

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 100, pointScanDeadline())
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if res.Readable != 3 || res.Unreadable != 0 {
		t.Fatalf("readable=%d unreadable=%d，期望 3/0", res.Readable, res.Unreadable)
	}
	want := []struct{ name, value, note string }{
		{"D100", "4660", ""},
		{"D101", "", pointScanZeroNote}, // 零值点位照样列出（不过滤），Value 留空
		{"D102", "65535", ""},
	}
	for i, w := range want {
		p := res.Points[i]
		if p.Name != w.name || p.Value != w.value {
			t.Errorf("第 %d 个点位 = (%q,%q)，期望 (%q,%q)", i, p.Name, p.Value, w.name, w.value)
		}
		if !strings.Contains(p.Note, w.note) {
			t.Errorf("第 %d 个点位的 Note = %q，期望含 %q", i, p.Note, w.note)
		}
	}
	if res.AssumedCommonDataType != driver.TypeWord {
		t.Errorf("AssumedCommonDataType = %q，期望 %q", res.AssumedCommonDataType, driver.TypeWord)
	}
}

// TestScanPointsBitValues 位模式：每个字展开成 16 个点位，零值判据是**该位**为 0。
func TestScanPointsBitValues(t *testing.T) {
	// 一个字节里第 0 位与第 9 位为 1（位 0..7 在低字节，位 8..15 在高字节）。
	r := scripted(0, [][]byte{{0x02, 0x01}})
	opt := scanOpts(t, `{"area":"CIO","startWord":0,"width":"bit","count":16}`)

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 100, pointScanDeadline())
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(res.Points) != 16 {
		t.Fatalf("位模式下一个字应展开成 16 个点位，实际 %d", len(res.Points))
	}
	if res.Points[0].Name != "CIO0.00" || res.Points[0].Value != "1" {
		t.Errorf("位 0 = (%q,%q)，期望 (CIO0.00,1)", res.Points[0].Name, res.Points[0].Value)
	}
	if res.Points[9].Name != "CIO0.09" || res.Points[9].Value != "1" {
		t.Errorf("位 9 = (%q,%q)，期望 (CIO0.09,1)（位 8..15 在高字节）",
			res.Points[9].Name, res.Points[9].Value)
	}
	if res.Points[1].Value != "" {
		t.Errorf("位 1 = %q，期望留空（零值）", res.Points[1].Value)
	}
	if res.AssumedCommonDataType != driver.TypeBoolean {
		t.Errorf("AssumedCommonDataType = %q，期望 %q", res.AssumedCommonDataType, driver.TypeBoolean)
	}
}

// TestScanPointsBitZeroIsPerBit 同一个字里别的位为 1，不该让这个零位显示成有值。
func TestScanPointsBitZeroIsPerBit(t *testing.T) {
	r := scripted(0, [][]byte{{0xFF, 0xFF}}) // 全 1
	opt := scanOpts(t, `{"area":"DM","startWord":0,"width":"bit","count":16}`)
	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 100, pointScanDeadline())
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	for i, p := range res.Points {
		if p.Value != "1" {
			t.Errorf("位 %d 的值为 %q，期望 1（整字全 1）", i, p.Value)
		}
	}
}

// TestScanPointsDWordValues 双字模式：一个点位占两个字，按配置的字序解码。
func TestScanPointsDWordValues(t *testing.T) {
	// 大端字序：高字在前 → 0x0001_0002 = 65538。
	r := scripted(10, [][]byte{wordBytes(0x0001), wordBytes(0x0002)})
	opt := scanOpts(t, `{"area":"DM","startWord":10,"width":"dword","count":1}`)

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 100, pointScanDeadline())
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(res.Points) != 1 || res.Points[0].Value != "65538" {
		t.Fatalf("双字点位 = %+v，期望 D10=65538", res.Points)
	}
	if res.AssumedCommonDataType != driver.TypeDWord {
		t.Errorf("AssumedCommonDataType = %q，期望 %q", res.AssumedCommonDataType, driver.TypeDWord)
	}
}

// TestScanPointsStopsAtAreaEnd 读到内存区末尾时停下，并把后面的地址一律记为读不到。
//
// 这是 FINS 扫描最常见的使用方式：用户并不知道 PLC 的 DM 区到哪结束，
// 填一个偏大的 count 是常态。**后面的地址不再逐个去问** —— 同一内存区的
// 字地址是连续的，越界之后全是越界，逐个探测只会把一次"扫到末尾"变成
// 几百次必然失败的请求，而结论一模一样。
func TestScanPointsStopsAtAreaEnd(t *testing.T) {
	// 从 D100 起只有 10 个字可读。
	r := scripted(100, [][]byte{
		wordBytes(1), wordBytes(2), wordBytes(3), wordBytes(4), wordBytes(5),
		wordBytes(6), wordBytes(7), wordBytes(8), wordBytes(9), wordBytes(10),
	})
	opt := scanOpts(t, `{"area":"DM","startWord":100,"width":"word","count":200}`)

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 8, pointScanDeadline())
	if err != nil {
		t.Fatalf("读到末尾不是错误: %v", err)
	}
	if res.Readable != 10 {
		t.Errorf("readable = %d，期望 10", res.Readable)
	}
	if res.Unreadable != 190 {
		t.Errorf("unreadable = %d，期望 190（末尾之后的地址一律记为读不到）", res.Unreadable)
	}
	if res.Scanned != 200 {
		t.Errorf("scanned = %d，期望 200", res.Scanned)
	}
	if res.Incomplete {
		t.Error("读到末尾是**扫完了**，不该标 incomplete")
	}
	// 请求数必须是"找到边界"的量级，而不是逐个地址问一遍。
	if len(r.calls) > 20 {
		t.Errorf("发了 %d 次请求，找到边界不该这么多次: %v", len(r.calls), r.calls)
	}
}

// TestScanPointsAbortsOnTransportErr 链路类错误必须**中止**，而不是记成"地址不存在"。
//
// 把设备掉线记成"这里没有点位"，用户会从一份几乎全是空的清单里得出与事实完全
// 相反的结论，而采集侧此时也已经在重连了 —— 两边的结论会互相矛盾。
func TestScanPointsAbortsOnTransportErr(t *testing.T) {
	r := &scriptedReader{words: map[uint16][]byte{}, unreadable: map[uint16]bool{},
		err: errors.New("fins udp: read timeout")}
	opt := scanOpts(t, `{"area":"DM","startWord":0,"width":"word","count":4}`)

	if _, err := scanWith(r.read, opt, DefaultFINSConfig(), 100, pointScanDeadline()); err == nil {
		t.Fatal("链路故障应中止并返回错误")
	}
}

// TestScanPointsAbortsOnNodeEndCode 目标节点忙/无响应这类结束码同样必须中止。
//
// 它们**可恢复**，与地址无关。拿它们做"这个区到此为止"的推断，一次偶然的
// 「目标节点忙」就会把整份点位清单从中间截断，而用户完全看不出为什么。
func TestScanPointsAbortsOnNodeEndCode(t *testing.T) {
	cases := []uint16{0x0204, 0x0205, 0x0105, 0x0301}
	for _, code := range cases {
		r := &scriptedReader{words: map[uint16][]byte{}, unreadable: map[uint16]bool{},
			err: newEndCodeError(code)}
		opt := scanOpts(t, `{"area":"DM","startWord":0,"width":"word","count":4}`)
		_, err := scanWith(r.read, opt, DefaultFINSConfig(), 100, pointScanDeadline())
		if err == nil {
			t.Errorf("结束码 0x%04X 是节点/PLC 状态问题，应中止而不是当作地址不存在", code)
		}
	}
}

// TestScanPointsTreatsUnsupportedAreaAsAllUnreadable 整个内存区都不支持时，
// 结果是"一个都读不到"而不是一个错误。
//
// 这是 WR/HR 区在不带这些区的型号上的常见形态。用户需要看到的是
// "这个区读不到"，而不是一条语焉不详的失败。
func TestScanPointsTreatsUnsupportedAreaAsAllUnreadable(t *testing.T) {
	r := scripted(0, nil) // 什么都不登记 = 全部不可读
	opt := scanOpts(t, `{"area":"WR","startWord":0,"width":"word","count":32}`)

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 16, pointScanDeadline())
	if err != nil {
		t.Fatalf("整区不支持不是错误: %v", err)
	}
	if res.Readable != 0 || res.Unreadable != 32 {
		t.Errorf("readable=%d unreadable=%d，期望 0/32", res.Readable, res.Unreadable)
	}
	if len(res.Points) != 0 {
		t.Errorf("不该有点位，实际 %d 个", len(res.Points))
	}
	// 结果切片必须是空数组而不是 nil：nil 过线是 JSON null，
	// 前端得为它多写一条分支。
	if res.Points == nil {
		t.Error("Points 必须是空切片而不是 nil")
	}
}

// TestScanPointsStopsAtFirstUnreadable 第一个读不到的地址之后的点位不再逐个去问。
//
// **这是有意的取舍，不是省事**：FINS 的内存区是连续的（DM 从 0 起一整片），
// 因此"某个字不存在"只可能是"已经越过这个区的末尾"。反过来若继续逐点去问，
// 一次 count 填大了的扫描会在末尾之后发出几百个必然失败的请求，最后撞上请求数
// 上限、给用户一份标着"未扫完"的结果 —— 而它其实早就扫完了。
//
// 前提是 isAddressEndCode 已经把可恢复的节点/PLC 状态错误排除在外
// （见 TestIsAddressEndCode 与 TestScanPointsAbortsOnNodeEndCode）。
func TestScanPointsStopsAtFirstUnreadable(t *testing.T) {
	// 只有 101~103 可读；100 与 104 之后都读不到。
	r := &scriptedReader{
		words: map[uint16][]byte{
			101: wordBytes(11), 102: wordBytes(12), 103: wordBytes(13),
		},
		unreadable: map[uint16]bool{},
	}
	opt := scanOpts(t, `{"area":"DM","startWord":101,"width":"word","count":4}`)

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 8, pointScanDeadline())
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	// 前 3 个可读，第 4 个（D104）读不到 —— 它被记成 unreadable，扫描随即结束。
	if res.Readable != 3 || res.Unreadable != 1 {
		t.Fatalf("readable=%d unreadable=%d，期望 3/1", res.Readable, res.Unreadable)
	}
	if res.Scanned != 4 {
		t.Errorf("scanned = %d，期望 4", res.Scanned)
	}
	if len(res.Points) != 3 {
		t.Errorf("点位数 = %d，期望 3", len(res.Points))
	}
	for _, p := range res.Points {
		if p.Name == "D104" {
			t.Errorf("D104 读不到，不该出现在结果里: %+v", p)
		}
	}
	if res.Incomplete {
		t.Error("读到末尾是**扫完了**，不该标 incomplete")
	}
}

// TestScanPointsStopsAtRequestQuota 请求数触顶时置 Incomplete 并返回已得结果。
//
// 上界存在的意义是**给"整段都读不到"的设备一个确定性的终点**：对半降级在一段
// 完全读不到的区间上会退化成一棵满二叉树，光靠 deadline 止损会让耗时随网络
// 延迟漂移（本地 PLC 上几百次请求可能只要几百毫秒，于是照样跑完，
// 而在慢链路上又跑不完）。
func TestScanPointsStopsAtRequestQuota(t *testing.T) {
	// 每个地址都单独可读，但设备拒绝一切多字请求 —— 降级因此一路拆到单点，
	// 平均每个点位要花两次请求。
	r := &scriptedReader{
		words:       map[uint16][]byte{},
		unreadable:  map[uint16]bool{},
		oneWordOnly: true,
	}
	for i := 0; i < 400; i++ {
		r.words[uint16(i)] = wordBytes(uint16(i + 1))
	}
	opt := scanOpts(t, `{"area":"DM","startWord":0,"width":"word","count":400}`)

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 8, pointScanDeadline())
	if err != nil {
		t.Fatalf("触顶不是错误: %v", err)
	}
	if !res.Incomplete {
		t.Fatalf("请求数触顶必须置 Incomplete（已扫 %d 个地址，%d 次请求）",
			res.Scanned, len(r.calls))
	}
	if len(r.calls) > pointScanMaxRequests {
		t.Errorf("发了 %d 次请求，超过上限 %d", len(r.calls), pointScanMaxRequests)
	}
}

// TestScanPointsTruncatesAtMaxPoints 结果条数到上限时置 Truncated。
//
// 与 Incomplete 是两回事：那个是"范围还没扫完"，这个是"结果条数到上限"，
// 前端必须分开显示 —— 否则用户会从"扫完了"得出"这台设备就这么多点位"。
func TestScanPointsTruncatesAtMaxPoints(t *testing.T) {
	data := make([][]byte, 0, pointScanMaxPoints+100)
	for i := 0; i < pointScanMaxPoints+100; i++ {
		data = append(data, wordBytes(uint16(i+1)))
	}
	r := scripted(0, data)
	opt := scanOpts(t, fmt.Sprintf(`{"area":"DM","startWord":0,"width":"word","count":%d}`,
		pointScanMaxPoints+100))

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 256, pointScanDeadline())
	if err != nil {
		t.Fatalf("触顶不是错误: %v", err)
	}
	if !res.Truncated {
		t.Fatal("结果条数到上限必须置 Truncated")
	}
	if len(res.Points) != pointScanMaxPoints {
		t.Errorf("点位数 = %d，期望 %d", len(res.Points), pointScanMaxPoints)
	}
	if res.Incomplete {
		t.Error("Truncated 时不该同时标 Incomplete（两者是不同的结论）")
	}
	// Scanned/Readable 必须与列表长度自洽，不能把没扫的地址算进去。
	if res.Readable != pointScanMaxPoints || res.Scanned != pointScanMaxPoints {
		t.Errorf("scanned=%d readable=%d，期望都是 %d", res.Scanned, res.Readable, pointScanMaxPoints)
	}
}

// TestScanPointsRespectsDeadline 预算耗尽时返回已完成的部分并置 Incomplete。
func TestScanPointsRespectsDeadline(t *testing.T) {
	data := make([][]byte, 0, 200)
	for i := 0; i < 200; i++ {
		data = append(data, wordBytes(uint16(i+1)))
	}
	r := scripted(0, data)
	opt := scanOpts(t, `{"area":"DM","startWord":0,"width":"word","count":200}`)

	// 过去时刻的 deadline：第一块之前就判定为"该收工了"。
	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 8, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("预算耗尽不是错误: %v", err)
	}
	if !res.Incomplete {
		t.Error("预算耗尽必须置 Incomplete")
	}
	if len(res.Points) != 0 {
		t.Errorf("一个请求都不该发出，实际 %d 个点位", len(res.Points))
	}
}

// ==================== 会话入口 ====================

// stubSession 一个把 Do 直接转成调用的 SessionAccess。
type stubSession struct {
	drv driver.Driver
	err error
}

func (s *stubSession) Do(fn func(driver.Driver) error) error {
	if s.err != nil {
		return s.err
	}
	return fn(s.drv)
}

// foreignDriver 一个不是 FINS 的驱动，用来验证类型开关会拒绝它。
type foreignDriver struct{}

func (foreignDriver) Connect(string) error                                 { return nil }
func (foreignDriver) Ping(string) error                                    { return nil }
func (foreignDriver) Read([]po.DeviceAddress) ([]driver.ReadResult, error) { return nil, nil }
func (foreignDriver) IsConnected() bool                                    { return true }
func (foreignDriver) Close() error                                         { return nil }

// TestScanPointsRequiresConnection 没有可用连接时给一条明确的报错，
// 而不是让用户拿到一份空清单。
func TestScanPointsRequiresConnection(t *testing.T) {
	// 从未 Connect 过的驱动实例：client 与 config 都是 nil。
	sess := &stubSession{drv: newFINSDriver(TransportUDP)}
	_, err := ScanPoints(sess, json.RawMessage(`{"count":4}`), pointScanDeadline())
	if err == nil {
		t.Fatal("未连接的设备应报错")
	}
	if !strings.Contains(err.Error(), "未连接") {
		t.Errorf("错误信息 = %q，应说清是设备未连接", err)
	}
}

// TestScanPointsRejectsForeignDriver 拿到别的协议的驱动实例时如实报错。
//
// 类型开关而不是给 driver.Driver 加方法：那会要求全部协议都实现它，
// 而这里只有 FINS 需要。
func TestScanPointsRejectsForeignDriver(t *testing.T) {
	sess := &stubSession{drv: foreignDriver{}}
	_, err := ScanPoints(sess, json.RawMessage(`{"count":4}`), pointScanDeadline())
	if err == nil || !strings.Contains(err.Error(), "不支持的驱动类型") {
		t.Fatalf("应报「不支持的驱动类型」，实际 %v", err)
	}
}

// TestScanPointsEmptyOptions 空的扫描选项要报错，而不是回落到某个默认范围。
//
// "扫多少"没有合理的默认值：猜一个 64 会让用户拿到一份他并没有要求的清单。
func TestScanPointsEmptyOptions(t *testing.T) {
	sess := &stubSession{drv: newFINSDriver(TransportUDP)}
	if _, err := ScanPoints(sess, nil, pointScanDeadline()); err == nil {
		t.Fatal("空选项应报错")
	}
}

// TestScanPointsEndToEnd 打一台真实的 FINS/UDP 假 PLC，走完整链路。
//
// 假 PLC 的内存区全是 0，因此这里验证的是**链路本身**：会话选择、
// 分块、解码、零值标注。具体数值的解码由上面注入读取原语的用例覆盖。
func TestScanPointsEndToEnd(t *testing.T) {
	srv, err := fake.NewFINSUDP()
	if err != nil {
		t.Fatalf("起假 PLC: %v", err)
	}
	defer srv.Close()

	host, portStr, _ := net.SplitHostPort(srv.Addr())
	port, _ := strconv.Atoi(portStr)

	drv := newFINSDriver(TransportUDP)
	if err := drv.Connect(fmt.Sprintf(`{"host":%q,"port":%d}`, host, port)); err != nil {
		t.Fatalf("连假 PLC: %v", err)
	}
	defer drv.Close()

	res, err := ScanPoints(&stubSession{drv: drv},
		json.RawMessage(`{"area":"DM","startWord":100,"width":"word","count":4}`),
		pointScanDeadline())
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(res.Points) != 4 {
		t.Fatalf("应扫到 4 个点位，实际 %+v", res.Points)
	}
	if res.Points[0].Name != "D100" {
		t.Errorf("第一个点位 = %q，期望 D100", res.Points[0].Name)
	}
	if res.Points[0].Value != "" || res.Points[0].Note != pointScanZeroNote {
		t.Errorf("假 PLC 的值全为 0，应标注零值：%+v", res.Points[0])
	}
	if res.Readable != 4 || res.Unreadable != 0 || res.Truncated || res.Incomplete {
		t.Errorf("计数与标志不符: %+v", res)
	}
}

// TestScanPointsWritesParseableAddresses 扫出来的地址必须能进 CalcFINSRanges。
//
// 这是"候选清单 → 建成点位"这条链路上真正的验收点：地址能解析还不够，
// 还要能通过**采集侧的区间规划**。一个过不了这一步的地址会被规划期摘出去，
// 那个点位永远采不到值。
func TestScanPointsWritesParseableAddresses(t *testing.T) {
	r := scripted(0, [][]byte{wordBytes(1), wordBytes(2), wordBytes(3), wordBytes(4)})
	opt := scanOpts(t, `{"area":"DM","startWord":0,"width":"word","count":4}`)

	res, err := scanWith(r.read, opt, DefaultFINSConfig(), 100, pointScanDeadline())
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}

	addrs := make([]po.DeviceAddress, 0, len(res.Points))
	for _, p := range res.Points {
		addrs = append(addrs, po.DeviceAddress{
			ID: p.Name, Name: p.Name, DataType: "word",
		})
	}
	ranges, err := CalcFINSRanges(addrs, DefaultFINSConfig())
	if err != nil {
		t.Fatalf("扫出来的地址过不了采集侧的区间规划: %v", err)
	}
	if len(ranges) != 1 || ranges[0].Count != 4 {
		t.Errorf("区间规划结果 = %+v，期望 1 个 4 字的区间", ranges)
	}
}
