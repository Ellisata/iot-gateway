// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package dlt645

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"iot-gateway/driver"
)

// ==================== 测试替身 ====================

// scanCfg 一份用于扫描测试的配置：超时压到毫秒级。
//
// 真实的 3s 超时会让「设备静默」那几条路径把测试拖到十几秒；扫描逻辑本身与超时的
// 绝对值无关，只与"读不通"这个结论有关。
func scanCfg(t *testing.T, version string) *DLT645Config {
	t.Helper()
	cfg := DefaultDLT645Config(TransportTCP)
	cfg.Version = version
	cfg.PreambleBytes = 0
	cfg.InterFrameDelayMS = 0
	cfg.InterFrameDelay = 0
	cfg.TimeoutMS = 30
	cfg.Timeout = 30 * time.Millisecond
	return cfg
}

func mustTargets(t *testing.T, cfg *DLT645Config, extras ...string) []scanTarget {
	t.Helper()
	ts, err := scanTargets(cfg, extras)
	if err != nil {
		t.Fatalf("scanTargets: %v", err)
	}
	return ts
}

// answerWithNames 假设备：每个点位都"读到"，且**值就是它自己的地址写法**。
//
// 值取名字而不是常量，是为了让"结果把值配到了另一个点位上"这种错位立刻显形 ——
// 那正是本功能最容易写错、又最难在真机上发现的一类问题（值看着完全合理）。
func answerWithNames(batch []scanTarget) ([]scanItem, error) {
	items := make([]scanItem, len(batch))
	for i := range batch {
		items[i] = scanItem{ok: true, value: batch[i].name}
	}
	return items, nil
}

// validBytesFor 造一份"结构上一定能解码成功"的原始字节。
//
// 时间类必须给**合法**值：decodeTimeLayout / decodeDateLayout 对超范围的字段
// （月 0、时 25、星期 9）刻意报错而不是自愈，随便填会得到一个与被测逻辑无关的失败。
// BCD 数值给 0x11（合法 nibble 且非全 0，避开"零值"那条分支）。
func validBytesFor(spec DISpec) []byte {
	switch spec.Layout {
	case layoutTime:
		return []byte{0x00, 0x00, 0x12} // 秒、分、时 → 12:00:00
	case layoutDate:
		return []byte{0x01, 0x01, 0x01, 0x24} // 星期、日、月、年 → 2024-01-01 周一
	case layoutAddr:
		return []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06} // BCD 数字串
	}
	b := make([]byte, spec.Bytes)
	for i := range b {
		if spec.Binary {
			b[i] = 0x01
			continue
		}
		b[i] = 0x11
	}
	return b
}

// fakeSession 一个最简 SessionAccess：直接把驱动交出去（等价于 driversdk 的
// instanceAccess —— 临时实例那条路径）。
type fakeSession struct{ drv driver.Driver }

func (f fakeSession) Do(fn func(drv driver.Driver) error) error { return fn(f.drv) }

// nonLivenessTargets 取 n 个**不含存活复探数据标识**的字典条目。
//
// 复探用的那个数据标识（0x00000000，与 Ping 同一个）本身就在字典里，它作为普通
// 点位被读到时会与复探走**同一条**假读取函数 —— 计数器分不开这两者，所以凡是要数
// 复探次数的用例都得先把它排除掉。这不是产品缺陷：真实设备上"读字典里的
// 0x00000000"与"复探"本来就是同一件事。
func nonLivenessTargets(t *testing.T, cfg *DLT645Config, n int) []scanTarget {
	t.Helper()
	live := livenessDI(cfg)
	out := make([]scanTarget, 0, n)
	for _, tg := range mustTargets(t, cfg) {
		if tg.spec.DI == live {
			continue
		}
		out = append(out, tg)
		if len(out) == n {
			return out
		}
	}
	t.Fatalf("字典里不足 %d 个非复探条目", n)
	return nil
}

// ==================== 清单组装与地址往返 ====================

// TestScanTargetsRoundTrip 钉住本功能最要命的那条不变量：
// **生成的地址必须能被 ParseAddress 还原回同一个位置。**
//
// 一个解析不出来的地址写进 device_address 之后，这个点位永远采不到值
// （buildPlan 逐点记 err、只标这一个点位 QualityBadAddress，不让整批失败）。
// 两个版本的全部字典条目都要过，附加数据标识那几条覆盖"字典外的数据标识只能靠
// 显式字节数才解析得出来"这一形态。
func TestScanTargetsRoundTrip(t *testing.T) {
	extras := map[string][]string{
		Version2007: {
			"12345678:4:2",    // 纯数值
			"12345679:3:3:s",  // 有符号
			"1234567A:2:0:sb", // 有符号 + 二进制
			"1234567B:8:16",   // 贴着上下限（8 字节 / 16 位小数）
			"02010100:4:2",    // 与字典条目重复：以附加的格式为准
		},
		Version1997: {
			"2345:4:2",
			"2346:3:3:s",
			"2347:2:0:sb",
			"2348:8:16",
			"9010:6:4", // 与字典条目重复
		},
	}

	for _, version := range []string{Version2007, Version1997} {
		t.Run(version, func(t *testing.T) {
			cfg := scanCfg(t, version)
			ex := extras[version]
			ts := mustTargets(t, cfg, ex...)

			// 字典条目数 + 附加条数 - 1（每份 extras 里恰好有一条与字典重复）
			if want := len(DictionaryDIs(version)) + len(ex) - 1; len(ts) != want {
				t.Errorf("清单共 %d 条，期望 %d 条（字典 %d + 附加 %d - 覆盖 1）",
					len(ts), want, len(DictionaryDIs(version)), len(ex))
			}

			// 字典条目必须**原位**保留：覆盖不改变顺序，两次扫描的行序才一致，
			// 用户才能逐行比对两次结果。
			for i, di := range DictionaryDIs(version) {
				if i >= len(ts) {
					t.Fatalf("清单短于字典（%d < %d）", len(ts), len(DictionaryDIs(version)))
				}
				if ts[i].spec.DI != di {
					t.Fatalf("第 %d 条是 %s，期望字典条目 %s（附加数据标识改动了顺序）",
						i, formatDI(ts[i].spec.DI, version), formatDI(di, version))
				}
			}

			for _, tg := range ts {
				back, err := ParseAddress(tg.name, version)
				if err != nil {
					t.Errorf("地址 %q 解析不回来: %v", tg.name, err)
					continue
				}
				if !reflect.DeepEqual(back, tg.spec) {
					t.Errorf("地址 %q 往返后规格变了：\n got %+v\nwant %+v", tg.name, back, tg.spec)
				}
			}
		})
	}
}

// TestScanTargetsExtraOverridesDictionary 附加数据标识与字典重复时**以附加的为准**。
//
// 静默让字典赢，就是"用户改了配置但它没生效"那类最难查的问题：他按手册写了
// `02010100:4:2`，界面上看着也提交了，而采出来的值仍是按 2 字节解出来的。
func TestScanTargetsExtraOverridesDictionary(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg, "02010100:4:2")

	var got *scanTarget
	for i := range ts {
		if ts[i].spec.DI == 0x02010100 {
			got = &ts[i]
			break
		}
	}
	if got == nil {
		t.Fatal("清单里没有 02010100")
	}
	if got.name != "02010100:4:2" {
		t.Errorf("地址 = %q，期望附加数据标识显式写出的格式（字典赢了吗？）", got.name)
	}
	if got.spec.Bytes != 4 || got.spec.Decimals != 2 {
		t.Errorf("规格 = %d 字节 / %d 位小数，期望 4 / 2", got.spec.Bytes, got.spec.Decimals)
	}
	// 字典里 A相电压 的默认规格是 2 字节 / 1 位小数，上面这两条断言就是在证明它被覆盖了
	if def, _ := LookupDI(Version2007, 0x02010100); def.Bytes != 2 || def.Decimals != 1 {
		t.Fatalf("字典变了？02010100 的默认规格现在是 %+v", def)
	}
}

// TestScanTargetsExtraDuplicateAmongThemselves 附加列表内部重复时只留一条。
func TestScanTargetsExtraDuplicateAmongThemselves(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg, "12345678:4:2", "12345678:4:2")

	n := 0
	for i := range ts {
		if ts[i].spec.DI == 0x12345678 {
			n++
		}
	}
	if n != 1 {
		t.Errorf("12345678 在清单里出现 %d 次，期望 1 次", n)
	}
}

// TestPointTypesPerDictionaryEntry 每个字典条目都要给出自洽的逐点类型。
//
// 645 的输出形态由数据标识本身决定，所以逐点给类型是唯一说得通的做法；而"通用类型
// 名 ↔ 内部裸名"这对映射一旦对不上，网关建点位时会按 common_data_type 落一个内部名，
// 采集时解出来的就是另一种形态的值 —— 界面上什么异常都看不出来。
func TestPointTypesPerDictionaryEntry(t *testing.T) {
	seen := map[string]bool{}
	for _, version := range []string{Version2007, Version1997} {
		cfg := scanCfg(t, version)
		for _, tg := range mustTargets(t, cfg) {
			if got := CommonTypeMap()[tg.common]; got != tg.intern {
				t.Errorf("%s（%s）的通用类型 %q 映射到 %q，与解码用的内部类型 %q 不一致",
					tg.name, version, tg.common, got, tg.intern)
			}
			seen[tg.common] = true

			// 每个字典条目都要**解得出值**：某个条目的 layout 与类型配错了对
			// （例如给时间类条目配了 float），症状是它永远读不到值，
			// 而用户只会以为"这块表没实现它"。
			if _, err := DecodeValue(validBytesFor(tg.spec), &tg.spec, tg.intern); err != nil {
				t.Errorf("%s（%s）按自己的类型解不出来: %v", tg.name, version, err)
			}
		}
	}

	// 2007 字典里确实存在这四类形态，否则上面的循环就是在空转
	for _, want := range []string{driver.TypeDate, driver.TypeString, driver.TypeWord, driver.TypeFloat} {
		if !seen[want] {
			t.Errorf("2007 字典里没有任何条目映射到 %s —— 分类判据是不是失效了？", want)
		}
	}
}

// TestPointTypesClassification 四类形态各自的判据逐条断言。
func TestPointTypesClassification(t *testing.T) {
	cases := []struct {
		name         string
		spec         DISpec
		intern, comm string
	}{
		{"日期", DISpec{Layout: layoutDate}, typeNameDateTime, driver.TypeDate},
		{"时间", DISpec{Layout: layoutTime}, typeNameDateTime, driver.TypeDate},
		{"表号", DISpec{Layout: layoutAddr}, typeNameString, driver.TypeString},
		{"二进制位域", DISpec{Binary: true}, typeNameUint, driver.TypeWord},
		{"BCD 数值", DISpec{}, typeNameFloat, driver.TypeFloat},
	}
	for _, c := range cases {
		intern, comm := pointTypes(c.spec)
		if intern != c.intern || comm != c.comm {
			t.Errorf("%s：pointTypes = (%q, %q)，期望 (%q, %q)", c.name, intern, comm, c.intern, c.comm)
		}
	}
}

// TestPointLabelCarriesUnit 标签要把单位带上。
//
// 单位没有单独的列可放，而电压的 V 与电流的 A 差一个字母、值差三个数量级：
// 丢掉它，用户在结果表上根本认不出哪一行是哪个量。
func TestPointLabelCarriesUnit(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)

	byDI := map[uint32]scanTarget{}
	for _, tg := range ts {
		byDI[tg.spec.DI] = tg
	}
	if got := byDI[0x02010100].label; got != "A相电压（V）" {
		t.Errorf("A相电压 的标签 = %q，期望带单位 %q", got, "A相电压（V）")
	}
	if got := byDI[0x02060000].label; got != "总功率因数" {
		t.Errorf("无单位条目的标签 = %q，期望就是字典名", got)
	}
}

// ==================== 值与位置的对应 ====================

// TestScanValueComesFromItsOwnDI 值必须来自它自己那个数据标识的位置。
//
// 地址生成（pointName）与逐项回填（emit）是两段独立代码，它们对"第 i 个点位是哪个
// 数据标识"的看法一旦分叉，产出的就是一份**地址正确、值却来自别处**的清单 ——
// 而值看着完全合理，真机上根本发现不了。这里让假设备把"值"设成点位自己的地址写法，
// 错位立刻显形。
//
// 同时钉住交给设备的**请求顺序**：假设备记录每一批收到的数据标识，断言它们恰好是
// 目标清单按序分组的结果（既没有漏、也没有重复、顺序也没乱）。
func TestScanValueComesFromItsOwnDI(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)

	var asked []uint32
	read := func(batch []scanTarget) ([]scanItem, error) {
		for _, tg := range batch {
			asked = append(asked, tg.spec.DI)
		}
		return answerWithNames(batch)
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}

	want := make([]uint32, 0, len(ts))
	for _, tg := range ts {
		want = append(want, tg.spec.DI)
	}
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("请求的数据标识序列与目标清单不符（漏了/重了/顺序乱了）")
	}

	byName := map[string]scanTarget{}
	for _, tg := range ts {
		byName[tg.name] = tg
	}
	for _, p := range res.Points {
		tg, ok := byName[p.Name]
		if !ok {
			t.Fatalf("结果里出现了清单外的地址 %q", p.Name)
		}
		if p.Value != tg.name {
			t.Errorf("点位 %s 的值是 %q —— 值来自别处", p.Name, p.Value)
		}
	}
}

// TestScanZeroValueKeptWithNote 值为 0 的点位照样列出来，只给 Note 不给 Value。
//
// 插件侧刻意**不做零值过滤**（过滤是前端的一枚开关）：在全都是 0 的表上过滤会返回
// 一份空清单，用户据此得出"这里没有点位"——与"没扫到"同型的错误。
// 判据是**原始字节全 0**（按解析值判会漏掉 0x00000001 这种"按 Float 解出来极小的
// 非零数"），所以这里用一个 BCD 全 0 的数据来驱动。
func TestScanZeroValueKeptWithNote(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)[:2]
	zeroDI := ts[1].spec.DI

	read := func(batch []scanTarget) ([]scanItem, error) {
		items := make([]scanItem, len(batch))
		for i, tg := range batch {
			if tg.spec.DI == zeroDI {
				items[i] = newScanItem(make([]byte, tg.spec.Bytes), "0")
				continue
			}
			items[i] = scanItem{ok: true, value: tg.name}
		}
		return items, nil
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if res.Readable != 2 {
		t.Fatalf("Readable = %d，期望 2（值为 0 也算读到）", res.Readable)
	}
	for _, p := range res.Points {
		if p.Name != ts[1].name {
			continue
		}
		if p.Value != "" {
			t.Errorf("零值点位不该有采样值，得到 %q", p.Value)
		}
		if p.Note != pointScanZeroNote {
			t.Errorf("零值点位的 note = %q，期望 %q", p.Note, pointScanZeroNote)
		}
		// 逐点类型不因零值而丢：它是建点位时那个下拉框的初值
		if p.CommonDataType != ts[1].common {
			t.Errorf("零值点位的类型 = %q，期望 %q", p.CommonDataType, ts[1].common)
		}
	}
}

// ==================== 失败分类：降级还是中止 ====================

// TestScanDegradesWhenBatchRejected 整块被拒时降级为逐个重读。
//
// 异常应答**不回显**是哪一个数据标识失败的（见 codec.go 的 abnormalError），
// 所以只有一路问下去才能把"哪几个真的没有"摘出来。而且首次撞上之后，后续批次
// 一律按单点问 —— 每块都先发一帧注定被拒的批量请求，纯属白等，而 7s 的预算
// 是按秒算的。
func TestScanDegradesWhenBatchRejected(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)[:14]

	var batched, single int
	read := func(batch []scanTarget) ([]scanItem, error) {
		if len(batch) > 1 {
			batched++
			return nil, &abnormalError{Code: 0x02}
		}
		single++
		return answerWithNames(batch)
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if batched != 1 {
		t.Errorf("批量请求发了 %d 次，期望 1 次（降级之后不该再试批量）", batched)
	}
	if single != 14 {
		t.Errorf("单点请求发了 %d 次，期望 14 次", single)
	}
	if res.Readable != 14 || res.Unreadable != 0 || len(res.Points) != 14 {
		t.Errorf("readable=%d unreadable=%d points=%d，期望 14/0/14",
			res.Readable, res.Unreadable, len(res.Points))
	}
}

// TestScanDegradesWhenFieldLengthShort 数据域短了整块的答复就不能整体采信。
func TestScanDegradesWhenFieldLengthShort(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)[:3]

	var batched int
	read := func(batch []scanTarget) ([]scanItem, error) {
		if len(batch) > 1 {
			batched++
			return nil, errFieldLength
		}
		return answerWithNames(batch)
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if batched != 1 {
		t.Errorf("批量请求发了 %d 次，期望 1 次", batched)
	}
	if res.Readable != 3 {
		t.Errorf("Readable = %d，期望 3（降级后逐个都读到了）", res.Readable)
	}
}

// TestScanSingleRejectionCountsUnreadable 单点被拒记"读不到"，不中止、不复探。
//
// **异常应答证明链路是活的**，所以它不该触发存活复探（那是给"没人答"准备的），
// 也不该把整次扫描打掉：一块表没实现某个数据标识是完全正常的事。
func TestScanSingleRejectionCountsUnreadable(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := nonLivenessTargets(t, cfg, 2)
	badDI := ts[1].spec.DI

	var probes int
	read := func(batch []scanTarget) ([]scanItem, error) {
		if len(batch) == 1 && batch[0].spec.DI == livenessDI(cfg) {
			probes++
		}
		for _, tg := range batch {
			if tg.spec.DI == badDI {
				return nil, &abnormalError{Code: 0x02}
			}
		}
		return answerWithNames(batch)
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if probes != 0 {
		t.Errorf("异常应答触发了 %d 次存活复探，期望 0 次（设备分明答了）", probes)
	}
	if res.Readable != 1 || res.Unreadable != 1 {
		t.Errorf("readable=%d unreadable=%d，期望 1/1", res.Readable, res.Unreadable)
	}
}

// TestScanFollowUpMarkedUnreadable 应答带后续帧标志时该点位记"读不到"。
//
// 驱动刻意不支持拼接后续帧（各厂商手册对序号回显的位置描述不一，臆测布局会静默
// 取到错位的数据），拿首帧片段解码会得到一个**看似合理实则被截断**的值 ——
// 那比取不到值危险得多。这里断言它既不出现在清单里，也不会被当成"读到"。
func TestScanFollowUpMarkedUnreadable(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)[:2]
	badDI := ts[0].spec.DI

	read := func(batch []scanTarget) ([]scanItem, error) {
		for _, tg := range batch {
			if tg.spec.DI == badDI {
				return nil, errFollowUp
			}
		}
		return answerWithNames(batch)
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if res.Readable != 1 || res.Unreadable != 1 {
		t.Fatalf("readable=%d unreadable=%d，期望 1/1", res.Readable, res.Unreadable)
	}
	for _, p := range res.Points {
		if p.Name == ts[0].name {
			t.Errorf("带后续帧标志的点位不该出现在清单里（值是首帧片段）")
		}
	}
}

// TestScanSilentDILiveLinkCountsUnreadable 设备对某个数据标识静默、但链路仍通。
//
// 645 的"静默"有歧义：规范规定不支持的数据标识回异常应答，而老表与廉价表常常
// 干脆不答。此时只能主动复探一次（与 Ping 同一个数据标识）把"掉线"排除掉，
// 否则一块表上有一个静默条目就会让整次扫描报"设备掉线"。
//
// 同一个静默条目在**成帧打包**里还会连坐同块其余十几个（超时定位不到是哪一个），
// 所以这条也顺带钉住"降级为逐个重问"确实发生了。
func TestScanSilentDILiveLinkCountsUnreadable(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := nonLivenessTargets(t, cfg, 3)
	silentDI := ts[1].spec.DI
	linkErr := errors.New("dlt645: 等待应答超时")

	var probes int
	read := func(batch []scanTarget) ([]scanItem, error) {
		if len(batch) == 1 && batch[0].spec.DI == livenessDI(cfg) {
			probes++
			return answerWithNames(batch) // 复探答了：链路是活的
		}
		for _, tg := range batch {
			if tg.spec.DI == silentDI {
				return nil, linkErr
			}
		}
		return answerWithNames(batch)
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if probes != 1 {
		t.Errorf("存活复探做了 %d 次，期望 1 次（同块内只探一次）", probes)
	}
	if res.Readable != 2 || res.Unreadable != 1 {
		t.Errorf("readable=%d unreadable=%d，期望 2/1（静默的那个记读不到，同块其余照常读出）",
			res.Readable, res.Unreadable)
	}
}

// TestScanSilentDIWhenLinkDeadAborts 复探也不通 ⇒ 链路真的断了，中止。
//
// 这是 specs/point-discovery.md §6 的硬要求：把"设备掉线"记成"这批数据标识都不存在"，
// 用户会从一份几乎全是空的清单里得出与事实完全相反的结论（而采集侧此时已经在重连了）。
func TestScanSilentDIWhenLinkDeadAborts(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)
	linkErr := errors.New("dlt645: 读应答失败")

	read := func(batch []scanTarget) ([]scanItem, error) { return nil, linkErr }

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("链路不通却没有报错")
	}
	if res.Readable != 0 || res.Unreadable != 0 {
		t.Errorf("readable=%d unreadable=%d，期望都是 0（掉线不是「读不到」）",
			res.Readable, res.Unreadable)
	}
	if res.Points == nil {
		t.Error("Points 是 nil —— 过线会变成 JSON null，前端得为它多写一条分支")
	}
}

// TestScanAbortKeepsPartialResults 中止时要把已经扫到的部分一并返回。
//
// 丢掉它们对用户没有任何好处：网关侧（service 的 warning 文案）本就是为这条路径
// 准备的——"下面这些是中断前已经读到的"。
func TestScanAbortKeepsPartialResults(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)[:14]
	linkErr := errors.New("dlt645: 读应答失败")

	var calls int
	read := func(batch []scanTarget) ([]scanItem, error) {
		calls++
		if calls == 1 {
			return answerWithNames(batch) // 第一块 12 个读到了
		}
		return nil, linkErr // 之后全断（含存活复探）
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("链路中断却没有报错")
	}
	if res.Readable != 12 || len(res.Points) != 12 {
		t.Errorf("readable=%d points=%d，期望 12/12（中断前的部分必须一起返回）",
			res.Readable, len(res.Points))
	}
}

// TestScanDeadlineIncomplete 预算耗尽 ⇒ Incomplete + 部分结果 + **不是** error。
//
// 超预算返回 error 会撞网关侧的 RPC 超时并累积插件卡死计数，连续几次会把整个
// 插件进程连同它下面所有协议、所有设备一起重启。
func TestScanDeadlineIncomplete(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)[:14]

	// 第一块成功但耗时越过截止时刻：扫描必须在块间判一次，而不是硬跑到底。
	read := func(batch []scanTarget) ([]scanItem, error) {
		time.Sleep(120 * time.Millisecond)
		return answerWithNames(batch)
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(40*time.Millisecond))
	if err != nil {
		t.Fatalf("预算耗尽是正常返回，不该报错: %v", err)
	}
	if !res.Incomplete {
		t.Error("Incomplete 未置位")
	}
	if res.Truncated {
		t.Error("Truncated 是「结果条数到上限」，与「没问完」是两回事，不该被一起置位")
	}
	if res.Readable != 12 {
		t.Errorf("Readable = %d，期望 12（第一块的那一批）", res.Readable)
	}
}

// TestScanDeadlineAlreadyPassed 一开始就已过截止时刻：一个请求都不发。
func TestScanDeadlineAlreadyPassed(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)

	var calls int
	read := func(batch []scanTarget) ([]scanItem, error) {
		calls++
		return answerWithNames(batch)
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if calls != 0 {
		t.Errorf("过期后还发了 %d 次请求", calls)
	}
	if !res.Incomplete || res.Scanned != 0 {
		t.Errorf("Incomplete=%v Scanned=%d，期望 true/0", res.Incomplete, res.Scanned)
	}
}

// ==================== 计数与上限 ====================

// TestScanCountInvariants 钉住契约里的三条计数不变量（specs/point-discovery.md §9）：
//
//	len(points) <= readable <= scanned 且 readable + unreadable == scanned
func TestScanCountInvariants(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	ts := mustTargets(t, cfg)
	badDI := ts[3].spec.DI

	read := func(batch []scanTarget) ([]scanItem, error) {
		items := make([]scanItem, len(batch))
		for i, tg := range batch {
			if tg.spec.DI == badDI {
				continue // 这一点读不到（items[i].ok 保持 false）
			}
			items[i] = scanItem{ok: true, value: tg.name}
		}
		return items, nil
	}

	res, err := scanWith(read, cfg, ts, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if res.Readable+res.Unreadable != res.Scanned {
		t.Errorf("readable(%d) + unreadable(%d) != scanned(%d)",
			res.Readable, res.Unreadable, res.Scanned)
	}
	if !(len(res.Points) <= res.Readable && res.Readable <= res.Scanned) {
		t.Errorf("不满足 len(points) <= readable <= scanned：%d <= %d <= %d",
			len(res.Points), res.Readable, res.Scanned)
	}
	if res.Scanned != len(ts) {
		t.Errorf("Scanned = %d，期望 %d（清单里的每一条都问过）", res.Scanned, len(ts))
	}
	if res.Unreadable != 1 {
		t.Errorf("Unreadable = %d，期望 1", res.Unreadable)
	}
}

// TestScanTruncatedStopsBeforeCounting 达到 MaxPoints 时，超出的条目**不计入统计**。
//
// 这样"读得到 M 个"与列表长度始终自洽（否则多出来的那几条会让两个数字打架）。
// 清单是合成的：真字典只有 44/18 条，够不到 256 的上限。
func TestScanTruncatedStopsBeforeCounting(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	targets := make([]scanTarget, pointScanMaxPoints+40)
	for i := range targets {
		targets[i] = scanTarget{
			name:   fmt.Sprintf("SYN%03d", i),
			spec:   DISpec{DI: 0x10000000 + uint32(i), Bytes: 2, Decimals: 1},
			common: driver.TypeFloat,
			intern: typeNameFloat,
		}
	}

	res, err := scanWith(answerWithNames, cfg, targets, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("scanWith: %v", err)
	}
	if !res.Truncated {
		t.Fatal("Truncated 未置位")
	}
	if len(res.Points) != pointScanMaxPoints {
		t.Errorf("points = %d，期望 %d", len(res.Points), pointScanMaxPoints)
	}
	if res.Scanned != pointScanMaxPoints || res.Readable != pointScanMaxPoints {
		t.Errorf("scanned=%d readable=%d，期望都是 %d（超出的不计入）",
			res.Scanned, res.Readable, pointScanMaxPoints)
	}
	if res.Incomplete {
		t.Error("Incomplete 是「没问完」，与「到上限」是两回事，不该被一起置位")
	}
}

// ==================== 真实读取路径 ====================

// readOneFrameTargets 取前 n 个字典条目作为一批。
func readOneFrameTargets(t *testing.T, cfg *DLT645Config, n int) []scanTarget {
	t.Helper()
	return mustTargets(t, cfg)[:n]
}

// okFrame 造一帧"每个数据标识都答了、数据都对"的正常应答。
func okFrame(t *testing.T, cfg *DLT645Config, addr [6]byte, batch []scanTarget) []byte {
	t.Helper()
	var data []byte
	for _, tg := range batch {
		data = append(data, tg.spec.DIWireBytes(cfg.DIBytes())...)
		data = append(data, validBytesFor(tg.spec)...)
	}
	return buildFrame(cfg, addr, expectedOKCtrl(cfg), data)
}

// TestReadOneFrameRealPath 走**真实**的封帧 / 收发 / 解析 / 回显校验 / 解码路径。
//
// 上面那些用例注入的是纯函数，覆盖不到"这一帧发出去长什么样、设备的应答怎么被解析"。
// 这里用包内的帧传输替身跑完整条路：帧必须带电表地址、控制码、正确的 33H 偏移与
// 校验码，逐项回显校验通过之后才解码。
func TestReadOneFrameRealPath(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	addr, err := MeterAddressBytes("000000000001")
	if err != nil {
		t.Fatal(err)
	}
	batch := readOneFrameTargets(t, cfg, 3)

	tr := pacedTransport(okFrame(t, cfg, addr, batch))
	d := newWiredDriver(t, cfg, "000000000001", tr)
	s, err := scanSessionOf(d)
	if err != nil {
		t.Fatal(err)
	}

	items, err := readOneFrame(s, batch)
	if err != nil {
		t.Fatalf("readOneFrame: %v", err)
	}
	for i, tg := range batch {
		if !items[i].ok {
			t.Errorf("%s 没读到（真实帧路径）", tg.name)
			continue
		}
		v, derr := DecodeValue(validBytesFor(tg.spec), &tg.spec, tg.intern)
		if derr != nil {
			t.Fatal(derr)
		}
		if want := FormatValue(v, tg.intern); items[i].value != want {
			t.Errorf("%s 的值 = %q，期望 %q", tg.name, items[i].value, want)
		}
	}
}

// TestReadOneFrameEchoMismatchSkipsThatItem 回显不符的那一项不产出值，其余照常。
//
// 错位取值会静默地给出**另一个**数据项的值，比报异常危险得多 —— 所以宁可不取值。
func TestReadOneFrameEchoMismatchSkipsThatItem(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	addr, _ := MeterAddressBytes("000000000001")
	batch := readOneFrameTargets(t, cfg, 2)

	var data []byte
	data = append(data, batch[0].spec.DIWireBytes(cfg.DIBytes())...)
	data = append(data, validBytesFor(batch[0].spec)...)
	data = append(data, 0x00, 0x01, 0x01, 0x03) // 第二个数据标识写错
	data = append(data, validBytesFor(batch[1].spec)...)

	tr := pacedTransport(buildFrame(cfg, addr, expectedOKCtrl(cfg), data))
	d := newWiredDriver(t, cfg, "000000000001", tr)
	s, _ := scanSessionOf(d)

	items, err := readOneFrame(s, batch)
	if err != nil {
		t.Fatalf("readOneFrame: %v", err)
	}
	if !items[0].ok {
		t.Error("第一个数据标识本该读到")
	}
	if items[1].ok {
		t.Error("回显不符的那一项产出了值")
	}
}

// TestReadOneFrameShortFieldReportedAsLengthErr 数据域短于请求总长 ⇒ 整帧不可采信。
//
// 这是回显校验**挡不住**的那一类：设备少回一项的数据时，后面每一项的窗口都会整体
// 前移，于是解出的是"看似合理实为别处"的值，而回显校验只保证"这一项的标识对"。
// 用户多、数据少的情况下，只有总长能把它识别出来。
func TestReadOneFrameShortFieldReportedAsLengthErr(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	addr, _ := MeterAddressBytes("000000000001")
	batch := readOneFrameTargets(t, cfg, 2)

	var data []byte
	data = append(data, batch[0].spec.DIWireBytes(cfg.DIBytes())...)
	data = append(data, validBytesFor(batch[0].spec)...)
	data = append(data, batch[1].spec.DIWireBytes(cfg.DIBytes())...)
	// 第二个数据标识的数据**一个字节都不给**（真机上的"少回一项"）

	tr := pacedTransport(buildFrame(cfg, addr, expectedOKCtrl(cfg), data))
	d := newWiredDriver(t, cfg, "000000000001", tr)
	s, _ := scanSessionOf(d)

	if _, err := readOneFrame(s, batch); !errors.Is(err, errFieldLength) {
		t.Errorf("err = %v，期望 errFieldLength（数据域总长不足）", err)
	}
}

// TestReadOneFrameFollowUpRejected 控制码带后续帧标志 ⇒ 整帧不可采信。
func TestReadOneFrameFollowUpRejected(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	addr, _ := MeterAddressBytes("000000000001")
	batch := readOneFrameTargets(t, cfg, 1)

	var data []byte
	data = append(data, batch[0].spec.DIWireBytes(cfg.DIBytes())...)
	data = append(data, validBytesFor(batch[0].spec)...)
	frame := buildFrame(cfg, addr, ctrl2007More, data) // 0xB1：有后续帧

	tr := pacedTransport(frame)
	d := newWiredDriver(t, cfg, "000000000001", tr)
	s, _ := scanSessionOf(d)

	if _, err := readOneFrame(s, batch); !errors.Is(err, errFollowUp) {
		t.Errorf("err = %v，期望 errFollowUp", err)
	}
}

// TestFieldSlice 切分原语的四种结论各断言一遍。
func TestFieldSlice(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	spec := DISpec{DI: 0x02010100, Bytes: 2}
	wire := spec.DIWireBytes(cfg.DIBytes())

	t.Run("正常", func(t *testing.T) {
		data := append(append([]byte{}, wire...), 0x11, 0x22)
		raw, next, err := fieldSlice(cfg, &spec, data, 0)
		if err != nil || next != 6 || len(raw) != 2 {
			t.Fatalf("raw=% X next=%d err=%v", raw, next, err)
		}
	})

	t.Run("数据域连标识都放不下", func(t *testing.T) {
		_, next, err := fieldSlice(cfg, &spec, wire[:2], 0)
		if err == nil || err.Kind != fieldShortHeader {
			t.Fatalf("err = %v，期望 fieldShortHeader", err)
		}
		if next != 0 {
			t.Errorf("next = %d，期望 0（位置不动）", next)
		}
	})

	t.Run("回显不符", func(t *testing.T) {
		data := []byte{0x00, 0x01, 0x01, 0x03, 0x11, 0x22}
		_, next, err := fieldSlice(cfg, &spec, data, 0)
		if err == nil || err.Kind != fieldEchoMismatch {
			t.Fatalf("err = %v，期望 fieldEchoMismatch", err)
		}
		if err.Got != 0x03010100 {
			t.Errorf("Got = %08X，期望 03010100", err.Got)
		}
		if next != 4 {
			t.Errorf("next = %d，期望 4（标识已消费，与抽取前的 fillResults 逐字相同）", next)
		}
	})

	t.Run("数据被截断", func(t *testing.T) {
		data := append(append([]byte{}, wire...), 0x11)
		_, next, err := fieldSlice(cfg, &spec, data, 0)
		if err == nil || err.Kind != fieldShortData {
			t.Fatalf("err = %v，期望 fieldShortData", err)
		}
		if err.Have != 1 {
			t.Errorf("Have = %d，期望 1", err.Have)
		}
		if next != 4 {
			t.Errorf("next = %d，期望 4", next)
		}
	})
}

// ==================== ScanPoints / 选项 ====================

// TestScanPointsPreflightNotConnected 设备没连上 ⇒ 一句明确的错，而不是一份空清单。
func TestScanPointsPreflightNotConnected(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	sess := fakeSession{drv: newDLT645Driver(cfg.Transport)}

	_, err := ScanPoints(sess, nil, time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("设备未连接却没有报错")
	}
	if !contains(err.Error(), "未连接") {
		t.Errorf("错误 = %v，期望提到未连接", err)
	}
}

// TestScanPointsEndToEnd 走 ScanPoints 的完整路径（含预检与真实帧）。
//
// 字典共 44 条，按每帧 12 个分 4 帧；这里给 4 帧应答，断言整本字典都被问到、
// 且每个点位都带着字典给的标签与逐点类型。
func TestScanPointsEndToEnd(t *testing.T) {
	cfg := scanCfg(t, Version2007)
	addr, _ := MeterAddressBytes("000000000001")

	all := mustTargets(t, cfg)
	var frames [][]byte
	for done := 0; done < len(all); {
		n := len(all) - done
		if n > perRead(cfg) {
			n = perRead(cfg)
		}
		frames = append(frames, okFrame(t, cfg, addr, all[done:done+n]))
		done += n
	}

	tr := pacedTransport(frames...)
	d := newWiredDriver(t, cfg, "000000000001", tr)
	sess := fakeSession{drv: d}

	res, err := ScanPoints(sess, nil, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("ScanPoints: %v", err)
	}
	want := len(DictionaryDIs(Version2007))
	if res.Readable != want || len(res.Points) != want {
		t.Fatalf("readable=%d points=%d，期望 %d", res.Readable, len(res.Points), want)
	}
	if res.AssumedCommonDataType != driver.TypeFloat {
		t.Errorf("结果级类型 = %q，期望 %q（前端据它显示「显示 0 值」开关）",
			res.AssumedCommonDataType, driver.TypeFloat)
	}
	byName := map[string]driver.DiscoveredPoint{}
	for _, p := range res.Points {
		byName[p.Name] = p
	}
	if p := byName["02010100"]; p.Label != "A相电压（V）" || p.CommonDataType != driver.TypeFloat {
		t.Errorf("A相电压 行 = %+v，期望标签带单位、类型 Float", p)
	}
	if p := byName["04000401"]; p.CommonDataType != driver.TypeString {
		t.Errorf("通信地址 行的类型 = %q，期望 String（它是 BCD 数字串，不是数值）", p.CommonDataType)
	}
	if p := byName["04000501"]; p.CommonDataType != driver.TypeWord {
		t.Errorf("电表运行状态字1 行的类型 = %q，期望 Word（二进制位域收原始整数）", p.CommonDataType)
	}
}

// TestParsePointScanOptions 选项解析：留空合法、填错报错。
func TestParsePointScanOptions(t *testing.T) {
	t.Run("留空即只扫字典", func(t *testing.T) {
		for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"extraDIs":""}`)} {
			got, err := parsePointScanOptions(raw)
			if err != nil || len(got) != 0 {
				t.Errorf("options=%s → (%v, %v)，期望空且无错", raw, got, err)
			}
		}
	})

	t.Run("分隔符与容错文本", func(t *testing.T) {
		got, err := parsePointScanOptions(json.RawMessage(`{"extraDIs":"A, B；C、D\nE"}`))
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"A", "B", "C", "D", "E"}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v，want %v", got, want)
		}
	})

	t.Run("数字也收", func(t *testing.T) {
		got, err := parsePointScanOptions(json.RawMessage(`{"extraDIs":1234}`))
		if err != nil || len(got) != 1 || got[0] != "1234" {
			t.Errorf("got (%v, %v)，期望 [\"1234\"]（纯数字选项不该让整次扫描失败）", got, err)
		}
	})

	t.Run("JSON 非法", func(t *testing.T) {
		if _, err := parsePointScanOptions(json.RawMessage(`{"extraDIs":`)); err == nil {
			t.Error("坏 JSON 没有报错")
		}
	})

	t.Run("超过条数上限", func(t *testing.T) {
		s := ""
		for i := 0; i <= pointScanMaxExtraDIs; i++ {
			s += fmt.Sprintf("%d,", i)
		}
		if _, err := parsePointScanOptions(json.RawMessage(fmt.Sprintf(`{"extraDIs":%q}`, s))); err == nil {
			t.Error("超过上限没有报错")
		}
	})

	t.Run("解析不出来的数据标识", func(t *testing.T) {
		cfg := scanCfg(t, Version2007)
		// 字典外的数据标识不给字节数 → ParseAddress 明确要求按手册指定长度，
		// 而不是猜一个（猜错会静默给出错位的值）。
		if _, err := scanTargets(cfg, []string{"12345678"}); err == nil {
			t.Error("字典外、又没给字节数，本该报错")
		}
		if _, err := scanTargets(cfg, []string{"ZZZZ:4:2"}); err == nil {
			t.Error("非法十六进制本该报错")
		}
	})
}

// TestPerReadForcesOneOn1997 1997 版每帧只能带一个数据标识。
//
// 与 plan.go 的 buildPlan 是同一条规则。照抄 maxDIsPerRead 会让单帧数据域超过
// maxDataLen1997，而 parseAt 对超长的 L 直接判"不是帧头"——设备的**合法应答永远
// 解析不出来**，表现是每一帧都超时（看起来像"设备不在"）。
func TestPerReadForcesOneOn1997(t *testing.T) {
	c2007 := scanCfg(t, Version2007)
	c2007.MaxDIsPerRead = 12
	if got := perRead(c2007); got != 12 {
		t.Errorf("2007 perRead = %d，期望 12", got)
	}

	c1997 := scanCfg(t, Version1997)
	c1997.MaxDIsPerRead = 12
	if got := perRead(c1997); got != 1 {
		t.Errorf("1997 perRead = %d，期望 1", got)
	}

	c2007.MaxDIsPerRead = 0
	if got := perRead(c2007); got != 1 {
		t.Errorf("上限为 0 时 perRead = %d，期望回落到 1", got)
	}
}

// TestLivenessDIStaysInSyncWithPing 存活复探用的数据标识必须与 Ping 一致。
//
// 两者分叉只会让"测试连接是通的、扫描却说设备掉线"这种自相矛盾的现象出现在现场，
// 而用户无从判断该信哪一个。
func TestLivenessDIStaysInSyncWithPing(t *testing.T) {
	cases := []struct {
		version string
		want    uint32
	}{
		{Version2007, 0x00000000}, // pingRead 的 pingDI2007
		{Version1997, 0x9010},     // pingRead 的 pingDI1997
	}
	for _, c := range cases {
		cfg := scanCfg(t, c.version)
		if got := livenessDI(cfg); got != c.want {
			t.Errorf("%s 的复探数据标识 = %08X，期望 %08X", c.version, got, c.want)
		}
	}
}

// ==================== 能力声明 ====================

// TestPointDiscoverySpec 能力声明里几个决定前端行为的取值。
func TestPointDiscoverySpec(t *testing.T) {
	spec := PointDiscovery()

	if spec.Title == "" {
		t.Error("Title 为空：弹窗标题没得显示")
	}
	if spec.Description == "" {
		t.Error("Description 为空：用户理解本功能性质的主要来源（设备不会告诉你它有哪些点位）")
	}
	// DefaultCommonDataType 是前端下拉的初值，必须是**通用类型名**（TypeMap 的 key）。
	// 写成内部裸名（"float"）会被 driversdk.Meta.Validate 拒掉。
	if _, ok := CommonTypeMap()[spec.DefaultCommonDataType]; !ok {
		t.Errorf("DefaultCommonDataType = %q 不是 TypeMap 的键", spec.DefaultCommonDataType)
	}
	if spec.MaxBudgetMs < 500 || spec.MaxBudgetMs > 30000 {
		t.Errorf("MaxBudgetMs = %d，超出 [500,30000]", spec.MaxBudgetMs)
	}
	if spec.MaxPoints < 1 || spec.MaxPoints > 20000 {
		t.Errorf("MaxPoints = %d，超出 [1,20000]", spec.MaxPoints)
	}
	if spec.MaxPoints != pointScanMaxPoints {
		t.Errorf("MaxPoints = %d，与代码里的上限 %d 不一致（两者必须同源）",
			spec.MaxPoints, pointScanMaxPoints)
	}
	if !json.Valid(spec.FormJSON) {
		t.Error("FormJSON 不是合法 JSON")
	}
	if !contains(string(spec.FormJSON), `"extraDIs"`) {
		t.Error("表单里没有 extraDIs 那一栏")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
