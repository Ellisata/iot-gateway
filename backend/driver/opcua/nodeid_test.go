// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package opcua

import (
	"reflect"
	"testing"

	"github.com/gopcua/opcua/ua"
)

// TestParseAddressNodeID 验证直接 NodeID 地址解析。
func TestParseAddressNodeID(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		wantStr string // 期望 NodeID 的 String()
	}{
		{"string ns2", "ns=2;s=Temperature", "ns=2;s=Temperature"},
		{"numeric ns2", "ns=2;i=100", "ns=2;i=100"},
		{"numeric ns0", "i=84", "i=84"},
		{"string ns0 bare", "s=SomeVar", "s=SomeVar"},
		{"byte string base64", "ns=2;b=AQID", "ns=2;b=AQID"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := parseAddress(c.addr)
			if err != nil {
				t.Fatalf("parseAddress(%q) error: %v", c.addr, err)
			}
			if a.isPath {
				t.Fatalf("parseAddress(%q) = path, want direct node id", c.addr)
			}
			if a.nodeID == nil {
				t.Fatal("nodeID is nil")
			}
			if a.nodeID.String() != c.wantStr {
				t.Errorf("nodeID.String() = %q, want %q", a.nodeID.String(), c.wantStr)
			}
		})
	}
}

// TestParseAddressInvalidNodeID 验证非法 NodeID 报错。
// 注：gopcua ParseExpandedNodeID 对未知类型符（如 "x=foo"）宽松地按字符串
// NodeID 处理，故此处不将其列为非法；仅校验命名空间/语法层面的错误。
func TestParseAddressInvalidNodeID(t *testing.T) {
	for _, addr := range []string{"", "   ", "abc;def", "ns=x;i=5", "i=notanumber"} {
		if _, err := parseAddress(addr); err == nil {
			t.Errorf("parseAddress(%q) want error, got nil", addr)
		}
	}
}

// TestParseAddressBrowsePath 验证浏览路径解析（段拆分与命名空间前缀）。
func TestParseAddressBrowsePath(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want []*ua.QualifiedName
	}{
		{"abs from root", "Root/Objects/Device/Temp", []*ua.QualifiedName{
			{Name: "Objects"}, {Name: "Device"}, {Name: "Temp"},
		}},
		{"ns prefixed", "Root/Objects/2:Device/2:Temp", []*ua.QualifiedName{
			{Name: "Objects"}, {NamespaceIndex: 2, Name: "Device"}, {NamespaceIndex: 2, Name: "Temp"},
		}},
		{"starts from objects", "Objects/Device/Temp", []*ua.QualifiedName{
			{Name: "Device"}, {Name: "Temp"},
		}},
		{"ns= form", "Root/Objects/ns=2:Device", []*ua.QualifiedName{
			{Name: "Objects"}, {NamespaceIndex: 2, Name: "Device"},
		}},
		{"tolerate double slash", "Root//Objects//Temp", []*ua.QualifiedName{
			{Name: "Objects"}, {Name: "Temp"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := parseAddress(c.addr)
			if err != nil {
				t.Fatalf("parseAddress(%q) error: %v", c.addr, err)
			}
			if !a.isPath {
				t.Fatalf("parseAddress(%q) = node id, want browse path", c.addr)
			}
			if !reflect.DeepEqual(a.path, c.want) {
				t.Errorf("path = %+v, want %+v", a.path, c.want)
			}
		})
	}
}

// TestParseAddressInvalidBrowsePath 验证非法浏览路径报错。
func TestParseAddressInvalidBrowsePath(t *testing.T) {
	for _, addr := range []string{
		"Root/Objects/2:",      // 段缺浏览名
		"Root/ns=99999:Device", // 命名空间越界
		"Root/ns=abc:Device",   // 命名空间非数字
		"/",                    // 仅斜杠
	} {
		if _, err := parseAddress(addr); err == nil {
			t.Errorf("parseAddress(%q) want error, got nil", addr)
		}
	}
}
