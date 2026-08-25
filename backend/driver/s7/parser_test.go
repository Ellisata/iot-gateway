package s7

import (
	"testing"
	"time"
)

// timeT 时间类型别名，便于测试断言
type timeT = time.Time

// date 构造 UTC 日期（S7 DATE）
func date(y, m, d int) timeT {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

// dateTime 构造 UTC 日期时间（S7 DATE_AND_TIME），ms 为毫秒
func dateTime(y, mo, d, h, mi, s, ms int) timeT {
	return time.Date(y, time.Month(mo), d, h, mi, s, ms*1000000, time.UTC)
}

// wantAddr 简化测试断言的地址结构
type wantAddr struct {
	area       s7Area
	db         int
	byteOffset int
	bitOffset  int
	width      int
	stringLen  int
	isString   bool
}

func toWant(a S7Address) wantAddr {
	return wantAddr{
		area:       a.Area,
		db:         a.DB,
		byteOffset: a.ByteOffset,
		bitOffset:  a.BitOffset,
		width:      a.Width,
		stringLen:  a.StringLen,
		isString:   a.IsString,
	}
}

func TestParseS7Address(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  wantAddr
		ok    bool
	}{
		// DB 区
		{"DB bit", "DB1.DBX0.0", wantAddr{db: 1, area: S7AreaDB, bitOffset: 0}, true},
		{"DB bit high", "DB1.DBX10.7", wantAddr{db: 1, area: S7AreaDB, byteOffset: 10, bitOffset: 7}, true},
		{"DB byte", "DB1.DBB0", wantAddr{db: 1, area: S7AreaDB, width: 1, bitOffset: -1}, true},
		{"DB word", "DB1.DBW2", wantAddr{db: 1, area: S7AreaDB, byteOffset: 2, width: 2, bitOffset: -1}, true},
		{"DB dword", "DB1.DBD4", wantAddr{db: 1, area: S7AreaDB, byteOffset: 4, width: 4, bitOffset: -1}, true},
		{"DB string", "DB1.STRING0", wantAddr{db: 1, area: S7AreaDB, isString: true, bitOffset: -1}, true},
		{"DB string explicit len", "DB1.STRING0.50", wantAddr{db: 1, area: S7AreaDB, isString: true, stringLen: 50, bitOffset: -1}, true},
		{"DB lowercase", "db2.dbd10", wantAddr{db: 2, area: S7AreaDB, byteOffset: 10, width: 4, bitOffset: -1}, true},
		// M 区
		{"M bit", "M0.0", wantAddr{area: S7AreaM, bitOffset: 0}, true},
		{"M byte", "MB0", wantAddr{area: S7AreaM, width: 1, bitOffset: -1}, true},
		{"M word", "MW10", wantAddr{area: S7AreaM, byteOffset: 10, width: 2, bitOffset: -1}, true},
		{"M dword", "MD0", wantAddr{area: S7AreaM, width: 4, bitOffset: -1}, true},
		// I 区
		{"I bit", "I0.7", wantAddr{area: S7AreaI, bitOffset: 7}, true},
		{"I byte", "IB1", wantAddr{area: S7AreaI, byteOffset: 1, width: 1, bitOffset: -1}, true},
		{"I word", "IW2", wantAddr{area: S7AreaI, byteOffset: 2, width: 2, bitOffset: -1}, true},
		// Q 区
		{"Q bit", "Q0.0", wantAddr{area: S7AreaQ, bitOffset: 0}, true},
		{"Q byte", "QB0", wantAddr{area: S7AreaQ, width: 1, bitOffset: -1}, true},
		{"Q dword", "QD8", wantAddr{area: S7AreaQ, byteOffset: 8, width: 4, bitOffset: -1}, true},

		// 非法
		{"empty", "", wantAddr{}, false},
		{"whitespace", "   ", wantAddr{}, false},
		{"unknown prefix", "ABC", wantAddr{}, false},
		{"db zero", "DB0.DBX0.0", wantAddr{}, false},
		{"no db number", "DBX0.0", wantAddr{}, false},
		{"no rest after db", "DB1", wantAddr{}, false},
		{"bit missing bit num", "DB1.DBX0", wantAddr{}, false},
		{"bit too high", "DB1.DBX0.8", wantAddr{}, false},
		{"bit negative", "DB1.DBX0.-1", wantAddr{}, false},
		{"negative byte", "DB1.DBB-1", wantAddr{}, false},
		{"bad suffix", "DB1.DBF0", wantAddr{}, false},
		{"string zero len", "DB1.STRING0.0", wantAddr{}, false},
		{"string negative", "DB1.STRING-1", wantAddr{}, false},
		{"string negative len", "DB1.STRING0.-5", wantAddr{}, false},
		{"string len at max", "DB1.STRING0.254", wantAddr{db: 1, area: S7AreaDB, isString: true, stringLen: 254, bitOffset: -1}, true},
		{"string len over max", "DB1.STRING0.255", wantAddr{}, false},
		{"M no suffix", "M0", wantAddr{}, false},
		{"M bit too high", "M0.8", wantAddr{}, false},
		{"M no offset", "MB", wantAddr{}, false},
		{"db too large", "DB65536.DBW0", wantAddr{}, false},
		{"offset overflow", "DB1.DBB16777216", wantAddr{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseS7Address(tt.input)
			if ok != tt.ok {
				t.Fatalf("ParseS7Address(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if !ok {
				return
			}
			if w := toWant(got); w != tt.want {
				t.Errorf("ParseS7Address(%q) = %+v, want %+v", tt.input, w, tt.want)
			}
		})
	}
}

func TestParseS7Value(t *testing.T) {
	// addr 构造辅助：非位地址
	addr := func(off int) S7Address {
		return S7Address{Area: S7AreaDB, DB: 1, ByteOffset: off, BitOffset: -1}
	}
	bitAddr := func(off, bit int) S7Address {
		return S7Address{Area: S7AreaDB, DB: 1, ByteOffset: off, BitOffset: bit}
	}

	tests := []struct {
		name     string
		addr     S7Address
		raw      []byte
		dataType string
		want     any
		wantErr  bool
	}{
		{"bool set", bitAddr(0, 3), []byte{0x08}, "bool", true, false},
		{"bool clear", bitAddr(0, 3), []byte{0x00}, "bool", false, false},
		{"bool lower bit", bitAddr(0, 0), []byte{0x01}, "bool", true, false},
		{"word", addr(0), []byte{0x12, 0x34}, "word", uint16(0x1234), false},
		{"int negative", addr(0), []byte{0xFF, 0xFE}, "int", int16(-2), false},
		{"real", addr(0), []byte{0x3F, 0x80, 0x00, 0x00}, "real", float32(1.0), false},
		{"lreal", addr(0), []byte{0x3F, 0xF0, 0, 0, 0, 0, 0, 0}, "lreal", float64(1.0), false},
		{"byte", addr(0), []byte{0xAB}, "byte", byte(0xAB), false},
		{"string", addr(0), []byte{0xFE, 0x03, 'A', 'B', 'C'}, "string", "ABC", false},
		{"time", addr(0), []byte{0, 0, 0, 100}, "time", int32(100), false},
		{"tod", addr(0), []byte{0x02, 0xBF, 0x20, 0x00}, "tod", int32(46080000), false},
		{"date", addr(0), []byte{0, 5}, "date", date(1990, 1, 6), false},
		{"s5time", addr(0), []byte{0x12, 0x34}, "s5time", int64(23400), false},
		{"dt", addr(0), []byte{0x32, 0x12, 0x03, 0x11, 0x30, 0x45, 0x57, 0x01}, "dt", dateTime(2032, 12, 3, 11, 30, 45, 570), false},
		// 错误
		{"unsupported type", addr(0), []byte{0x01}, "nonexistent", nil, true},
		{"word truncated", addr(0), []byte{0x12}, "word", nil, true},
		{"bit empty raw", bitAddr(0, 0), nil, "bool", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseS7Value(tt.raw, tt.addr, tt.dataType)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseS7Value(%q) want error, got %v", tt.dataType, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseS7Value(%q) unexpected error: %v", tt.dataType, err)
			}
			// 时间类型比较
			switch want := tt.want.(type) {
			case timeT:
				g, ok := got.(timeT)
				if !ok {
					t.Fatalf("ParseS7Value(%q) = %T, want time.Time", tt.dataType, got)
				}
				if !g.Equal(want) {
					t.Errorf("ParseS7Value(%q) = %v, want %v", tt.dataType, g, want)
				}
				return
			}
			if got != tt.want {
				t.Errorf("ParseS7Value(%q) = %v (%T), want %v (%T)",
					tt.dataType, got, got, tt.want, tt.want)
			}
		})
	}
}
