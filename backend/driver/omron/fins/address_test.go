package fins

import "testing"

func TestParseFINSAddress(t *testing.T) {
	cases := []struct {
		name string
		want FINSAddress
		ok   bool
	}{
		{"D100", FINSAddress{Area: AreaDM, Word: 100, Bit: -1}, true},
		{"DM100", FINSAddress{Area: AreaDM, Word: 100, Bit: -1}, true},
		{"D100.05", FINSAddress{Area: AreaDM, Word: 100, Bit: 5}, true},
		{"dm200.15", FINSAddress{Area: AreaDM, Word: 200, Bit: 15}, true},
		{"CIO0.00", FINSAddress{Area: AreaCIO, Word: 0, Bit: 0}, true},
		{"CIO100", FINSAddress{Area: AreaCIO, Word: 100, Bit: -1}, true},
		{"cio200", FINSAddress{Area: AreaCIO, Word: 200, Bit: -1}, true},
		{"W10", FINSAddress{Area: AreaWR, Word: 10, Bit: -1}, true},
		{"WR10", FINSAddress{Area: AreaWR, Word: 10, Bit: -1}, true},
		{"H5", FINSAddress{Area: AreaHR, Word: 5, Bit: -1}, true},
		{"HR5.07", FINSAddress{Area: AreaHR, Word: 5, Bit: 7}, true},
		{"100", FINSAddress{Area: AreaCIO, Word: 100, Bit: -1}, true}, // 裸数字默认 CIO
		{"0", FINSAddress{Area: AreaCIO, Word: 0, Bit: -1}, true},
		{"D65535", FINSAddress{Area: AreaDM, Word: 65535, Bit: -1}, true},

		{"", FINSAddress{}, false},
		{"  ", FINSAddress{}, false},
		{"XYZ", FINSAddress{}, false},
		{"D", FINSAddress{}, false},
		{"DABC", FINSAddress{}, false},
		{"D-1", FINSAddress{}, false},
		{"D100.16", FINSAddress{}, false}, // 位上限 15
		{"D100.", FINSAddress{}, false},
		{"D.05", FINSAddress{}, false},
		{"D100.05extra", FINSAddress{}, false},
		{"D65536", FINSAddress{}, false}, // 字地址上限 0xFFFF
	}

	for _, c := range cases {
		got, ok := ParseFINSAddress(c.name)
		if ok != c.ok {
			t.Errorf("ParseFINSAddress(%q) ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got != c.want {
			t.Errorf("ParseFINSAddress(%q) = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestFINSAddressIsBit(t *testing.T) {
	a, ok := ParseFINSAddress("D100.03")
	if !ok || !a.IsBit() {
		t.Fatalf("D100.03 should be a bit address")
	}
	w, ok := ParseFINSAddress("D100")
	if !ok || w.IsBit() {
		t.Fatalf("D100 should be a word address")
	}
}
