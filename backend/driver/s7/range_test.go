package s7

import (
	"testing"

	"iot-gateway/model/po"
)

// mk 构造测试点位
func mk(id, name, dataType string) po.DeviceAddress {
	return po.DeviceAddress{ID: id, Name: name, DataType: dataType}
}

func TestCalcS7Ranges_MergeContiguous(t *testing.T) {
	addrs := []po.DeviceAddress{
		mk("a", "DB1.DBW0", "word"),
		mk("c", "DB1.DBW2", "word"),
		mk("b", "DB1.DBD4", "dint"),
	}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 字节跨度 [0,2) [2,4) [4,8) 相邻 → 合并为 [0, 8)
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	if ranges[0].StartOffset != 0 || ranges[0].EndOffset != 8 {
		t.Errorf("range = [%d, %d), want [0, 8)", ranges[0].StartOffset, ranges[0].EndOffset)
	}
	if len(ranges[0].AddressMap) != 3 {
		t.Errorf("range points = %d, want 3", len(ranges[0].AddressMap))
	}
}

func TestCalcS7Ranges_SplitGap(t *testing.T) {
	addrs := []po.DeviceAddress{
		mk("a", "DB1.DBW0", "word"),
		mk("b", "DB1.DBW10", "word"),
	}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	// [0,2) 与 [10,12) 不连续 → 拆成 2 段（不向外扩 gap）
	if len(ranges) != 2 {
		t.Fatalf("want 2 ranges, got %d", len(ranges))
	}
}

func TestCalcS7Ranges_StringIsolated(t *testing.T) {
	addrs := []po.DeviceAddress{
		mk("a", "DB1.DBW0", "word"),
		mk("s", "DB1.STRING10", "string"),
	}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	// STRING 必须与普通点位隔离成独立区间
	if len(ranges) != 2 {
		t.Fatalf("want 2 ranges (string isolated), got %d", len(ranges))
	}

	var strRange *S7Range
	for i := range ranges {
		if _, ok := ranges[i].AddressMap["s"]; ok {
			strRange = &ranges[i]
		}
	}
	if strRange == nil {
		t.Fatal("string range not found")
	}
	// STRING 跨度 = 2 头字节 + 254
	if size := strRange.EndOffset - strRange.StartOffset; size != 256 {
		t.Errorf("string range size = %d, want 256 (2+254)", size)
	}
}

func TestCalcS7Ranges_StringExplicitLen(t *testing.T) {
	addrs := []po.DeviceAddress{mk("s", "DB1.STRING0.20", "string")}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	// 显式长度覆盖默认：2 + 20 = 22
	if size := ranges[0].EndOffset - ranges[0].StartOffset; size != 22 {
		t.Errorf("string range size = %d, want 22 (2+20)", size)
	}
}

func TestCalcS7Ranges_StringsNotMerged(t *testing.T) {
	// 同一 DB 的多个 STRING 必须各自独立成区间、互不合并，
	// 保证一个越界的 STRING 不会拖垮同组其它 STRING
	addrs := []po.DeviceAddress{
		mk("a", "DB1.STRING0", "string"),
		mk("b", "DB1.STRING20", "string"),
	}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 2 {
		t.Fatalf("want 2 independent string ranges, got %d", len(ranges))
	}
	if ranges[0].StartOffset != 0 || ranges[1].StartOffset != 20 {
		t.Errorf("string ranges start at %d, %d; want 0, 20",
			ranges[0].StartOffset, ranges[1].StartOffset)
	}
	// 各区间只含自己的点位
	if _, ok := ranges[0].AddressMap["b"]; ok {
		t.Error("range[0] must not contain address b")
	}
}

func TestCalcS7Ranges_StringLen(t *testing.T) {
	// 显式长度超过 S7 上限 254 → 非法地址，直接报错
	if _, err := CalcS7Ranges([]po.DeviceAddress{mk("s", "DB1.STRING0.500", "string")}, 254, 0); err == nil {
		t.Error("want error for string explicit len > 254")
	}
	// 显式长度在上限内 → 按声明读取：2 + 20 = 22
	ranges, err := CalcS7Ranges([]po.DeviceAddress{mk("s2", "DB1.STRING0.20", "string")}, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	if size := ranges[0].EndOffset - ranges[0].StartOffset; size != 22 {
		t.Errorf("explicit-len string range size = %d, want 22 (2+20)", size)
	}

	// 配置 stringLen 超过 254 → 钳制为 2 + 254 = 256
	ranges2, err := CalcS7Ranges([]po.DeviceAddress{mk("s3", "DB1.STRING0", "string")}, 300, 0)
	if err != nil {
		t.Fatal(err)
	}
	if size := ranges2[0].EndOffset - ranges2[0].StartOffset; size != 256 {
		t.Errorf("cfg-len string range size = %d, want 256 (2+254 clamped)", size)
	}
}

func TestCalcS7Ranges_MultiAreaGroup(t *testing.T) {
	addrs := []po.DeviceAddress{
		mk("a", "DB1.DBW0", "word"),
		mk("b", "MW0", "word"),
		mk("c", "IB0", "byte"),
	}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	// (area=DB,db=1) / (area=M,db=0) / (area=I,db=0) 三个不同分组
	if len(ranges) != 3 {
		t.Fatalf("want 3 ranges (different area/db), got %d", len(ranges))
	}
}

func TestCalcS7Ranges_SpanMaxWithTypeSize(t *testing.T) {
	// DBW 地址（宽度 2）但类型 REAL（4 字节）→ 跨度取 max(2, 4) = 4
	addrs := []po.DeviceAddress{mk("a", "DB1.DBW0", "real")}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	if size := ranges[0].EndOffset - ranges[0].StartOffset; size != 4 {
		t.Errorf("range size = %d, want 4", size)
	}
}

func TestCalcS7Ranges_BitMergesWithWord(t *testing.T) {
	addrs := []po.DeviceAddress{
		mk("b", "DB1.DBX0.7", "bool"),
		mk("w", "DB1.DBW0", "word"),
	}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 位地址（字节 0，跨度 1）与 word（字节 0，跨度 2）交叠 → 合并 [0, 2)
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	if ranges[0].EndOffset != 2 {
		t.Errorf("range end = %d, want 2", ranges[0].EndOffset)
	}
	// 位地址应保留 BitOffset 供取值使用
	saddr, ok := ranges[0].AddressMap["b"]
	if !ok {
		t.Fatal("bit address missing from map")
	}
	if saddr.BitOffset != 7 {
		t.Errorf("bit offset = %d, want 7", saddr.BitOffset)
	}
}

func TestCalcS7Ranges_UnknownTypeConservative(t *testing.T) {
	// 未知类型：不应被当作 STRING 过度读取（256 字节），按地址名宽度保守处理
	// 位地址 → 跨度 1
	addrs := []po.DeviceAddress{mk("b", "DB1.DBX0.3", "typo")}
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("want 1 range, got %d", len(ranges))
	}
	if size := ranges[0].EndOffset - ranges[0].StartOffset; size != 1 {
		t.Errorf("unknown-type bit range size = %d, want 1", size)
	}

	// 字地址 → 跨度 2
	addrs2 := []po.DeviceAddress{mk("w", "DB1.DBW0", "typo")}
	ranges2, err := CalcS7Ranges(addrs2, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	if size := ranges2[0].EndOffset - ranges2[0].StartOffset; size != 2 {
		t.Errorf("unknown-type word range size = %d, want 2", size)
	}
}

func TestCalcS7Ranges_Invalid(t *testing.T) {
	addrs := []po.DeviceAddress{mk("a", "invalid", "word")}
	if _, err := CalcS7Ranges(addrs, 254, 0); err == nil {
		t.Error("want error for invalid address")
	}
}

func TestCalcS7Ranges_Empty(t *testing.T) {
	if _, err := CalcS7Ranges(nil, 254, 0); err == nil {
		t.Error("want error for empty addrs")
	}
}

func TestCalcS7Ranges_GapFill(t *testing.T) {
	addrs := []po.DeviceAddress{
		mk("a", "DB1.DBW0", "word"), // [0, 2)
		mk("b", "DB1.DBW6", "word"), // [6, 8)，与 [0,2) 间隙 4 字节
	}
	// maxGap=0：不填洞，拆 2 段（旧行为）
	ranges, err := CalcS7Ranges(addrs, 254, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 2 {
		t.Fatalf("maxGap=0: want 2 ranges, got %d", len(ranges))
	}
	// maxGap=8：间隙 4 ≤ 8，合并为 [0, 8)
	ranges, err = CalcS7Ranges(addrs, 254, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 {
		t.Fatalf("maxGap=8: want 1 merged range, got %d", len(ranges))
	}
	if ranges[0].StartOffset != 0 || ranges[0].EndOffset != 8 {
		t.Errorf("range = [%d, %d), want [0, 8)", ranges[0].StartOffset, ranges[0].EndOffset)
	}
	if len(ranges[0].AddressMap) != 2 {
		t.Errorf("range points = %d, want 2", len(ranges[0].AddressMap))
	}
}

func TestCalcS7Ranges_GapTooLarge(t *testing.T) {
	addrs := []po.DeviceAddress{
		mk("a", "DB1.DBW0", "word"),  // [0, 2)
		mk("b", "DB1.DBW20", "word"), // [20, 22)，间隙 18 > 8 → 不合并
	}
	ranges, err := CalcS7Ranges(addrs, 254, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 2 {
		t.Fatalf("gap 18 > maxGap 8: want 2 ranges, got %d", len(ranges))
	}
}

func TestCalcS7Ranges_GapFillStringIsolated(t *testing.T) {
	// gap 合并不改变 STRING 隔离：STRING 仍独立成区间
	addrs := []po.DeviceAddress{
		mk("a", "DB1.DBW0", "word"),
		mk("s", "DB1.STRING10", "string"),
	}
	ranges, err := CalcS7Ranges(addrs, 254, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 2 {
		t.Fatalf("want 2 ranges (string isolated), got %d", len(ranges))
	}
}
