// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"testing"
	"time"

	"github.com/iceisfun/goindustrial/logging"
	"github.com/iceisfun/goindustrial/protocol/ethernetip"
	"github.com/iceisfun/goindustrial/protocol/ethernetip/cip"
	"github.com/iceisfun/goindustrial/transport"

	"iot-gateway/model/po"
)

// fakeTagObject 模拟 Logix 控制器标签对象（仅支持 Read Tag 0x4C）。
// 注册进 MessageRouter 的 SymbolicHandler，响应客户端的符号寻址读请求。
// 响应数据区格式：[类型码 2 字节 LE][值数据]，与真机 Read Tag 行为一致。
type fakeTagObject struct {
	tags map[string]tagEntry
}

type tagEntry struct {
	code uint16
	data []byte
}

func (f *fakeTagObject) HandleRequest(service cip.USINT, path cip.Path, data []byte) ([]byte, error) {
	if service != cip.ServiceReadTag {
		return nil, cip.Error{Status: cip.StatusServiceNotSupported}
	}
	name, err := symbolicName(path)
	if err != nil {
		return nil, cip.Error{Status: cip.StatusPathSegmentError}
	}
	entry, ok := f.tags[name]
	if !ok {
		return nil, cip.Error{Status: cip.StatusPathDestinationUnknown}
	}
	resp := make([]byte, 2+len(entry.data))
	binary.LittleEndian.PutUint16(resp[0:2], entry.code)
	copy(resp[2:], entry.data)
	return resp, nil
}

// symbolicName 从 EPATH 提取符号名（0x91 段：类型字节 + 长度 + 名字 + 奇数填充）。
func symbolicName(path cip.Path) (string, error) {
	raw := path.Bytes()
	if len(raw) < 2 || raw[0] != 0x91 {
		return "", errNotSymbolic
	}
	nameLen := int(raw[1])
	if len(raw) < 2+nameLen {
		return "", errNotSymbolic
	}
	return string(raw[2 : 2+nameLen]), nil
}

type symbolicErr struct{}

func (symbolicErr) Error() string { return "not a symbolic segment" }

var errNotSymbolic = symbolicErr{}

// newPipeClient 启动 goindustrial EtherNet/IP Server（net.Pipe 注入连接），
// 返回经管道连接的 goindustrial Client（已 Register Session）。
// 测试结束自动清理。
func newPipeClient(t *testing.T, obj *fakeTagObject) *ethernetip.Client {
	t.Helper()

	router := cip.NewMessageRouter()
	router.RegisterSymbolicHandler(obj)

	serverConn, clientConn := net.Pipe()
	srv := ethernetip.NewServer(router, ethernetip.WithServerConn(serverConn))
	if err := srv.Start(context.Background(), ""); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 与 newRockwellClient 同路径组装：SessionConnector（WithConn 注入管道）
	// → DirectTransport → Client（Connect 触发 Register Session）
	connector := ethernetip.NewSessionConnector("", logging.NewNopLogger(), ethernetip.WithConn(clientConn))
	dt, err := transport.NewDirectTransport(ctx, connector, ethernetip.SessionCloser{})
	if err != nil {
		t.Fatalf("direct transport: %v", err)
	}
	client := ethernetip.NewClient(dt)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("register session: %v", err)
	}
	return client
}

// pipeClientTransport 把 goindustrial Client 适配为 rockwellTransport（测试注入用）。
type pipeClientTransport struct{ client *ethernetip.Client }

func (p *pipeClientTransport) ReadTag(tag string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return p.client.ReadTag(ctx, tag)
}

func (p *pipeClientTransport) ReadTagElements(tag string, count uint16) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return p.client.ReadTagElements(ctx, tag, count)
}

func (p *pipeClientTransport) IsConnected() bool { return p.client.IsConnected() }

func (p *pipeClientTransport) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return p.client.Disconnect(ctx)
}

// TestIntegrationReadTags 端到端验证：驱动适配层 → goindustrial 客户端 →
// net.Pipe 假 PLC → 各类型标签读取与类型码头剥离语义。
// 本测试钉死 ReadTag 返回格式（2 字节类型码 + 数据），是适配器 stripTypeCodeHeader
// 的回归测试。
func TestIntegrationReadTags(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	obj := &fakeTagObject{tags: map[string]tagEntry{
		"DINT_Tag": {cipTypeDINT, mustLE32(t, 12345)},
		"REAL_Tag": {cipTypeREAL, mustLEFloat32(t, 3.14)},
		"BOOL_Tag": {cipTypeBOOL, []byte{0x01}},
		"INT_Tag":  {cipTypeINT, mustLE16(t, -100)},
		"LINT_Tag": {cipTypeLINT, mustLE64(t, -3)},
		"STR_Tag":  {cipTypeSTRING, mustLEString("PLC1")},
	}}
	tr := &pipeClientTransport{client: newPipeClient(t, obj)}
	defer tr.Close()

	// goindustrial ReadTag 返回「2 字节类型码 + 数据」，逐类型钉死
	cases := []struct {
		tag      string
		wantCode uint16
		wantData []byte
	}{
		{"DINT_Tag", cipTypeDINT, mustLE32(t, 12345)},
		{"REAL_Tag", cipTypeREAL, mustLEFloat32(t, 3.14)},
		{"BOOL_Tag", cipTypeBOOL, []byte{0x01}},
		{"INT_Tag", cipTypeINT, mustLE16(t, -100)},
		{"LINT_Tag", cipTypeLINT, mustLE64(t, -3)},
		{"STR_Tag", cipTypeSTRING, mustLEString("PLC1")},
	}
	for _, tc := range cases {
		data, err := tr.ReadTag(tc.tag)
		if err != nil {
			t.Fatalf("ReadTag(%s): %v", tc.tag, err)
		}
		if len(data) < 2 {
			t.Fatalf("ReadTag(%s): response too short: % X", tc.tag, data)
		}
		code := binary.LittleEndian.Uint16(data[0:2])
		if code != tc.wantCode {
			t.Errorf("ReadTag(%s) type code = 0x%04X, want 0x%04X", tc.tag, code, tc.wantCode)
		}
		got := data[2:]
		if len(got) != len(tc.wantData) {
			t.Errorf("ReadTag(%s) payload = % X, want % X", tc.tag, got, tc.wantData)
			continue
		}
		for i := range got {
			if got[i] != tc.wantData[i] {
				t.Errorf("ReadTag(%s) payload = % X, want % X", tc.tag, got, tc.wantData)
				break
			}
		}
	}

	// 不存在的标签 → CIP 状态错误（isCIPStatusError 识别为设备拒绝，非网络错误）
	_, err := tr.ReadTag("No_Such_Tag")
	if err == nil {
		t.Fatal("expected error for missing tag")
	}
	if !isCIPStatusError(err) {
		t.Fatalf("missing tag should be CIP status error, got: %v", err)
	}
}

// TestIntegrationDriverPipeline 端到端验证驱动 Read 管线：
// 传输层（net.Pipe 假 PLC）→ Read → 类型码头剥离 → 逐点位解码。
func TestIntegrationDriverPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	obj := &fakeTagObject{tags: map[string]tagEntry{
		"MotorSpeed":  {cipTypeDINT, mustLE32(t, 1500)},
		"Temperature": {cipTypeREAL, mustLEFloat32(t, 36.6)},
		"Running":     {cipTypeBOOL, []byte{0x01}},
		"RecipeName":  {cipTypeSTRING, mustLEString("BATCH-42")},
		// "Missing" 故意不注册，读时返回状态错误
	}}

	d := newRockwellDriver()
	d.config = DefaultRockwellConfig()
	tr := &pipeClientTransport{client: newPipeClient(t, obj)}
	defer tr.Close()
	d.client = tr

	addrs := []po.DeviceAddress{
		mkAddr("a1", "MotorSpeed", "int32", "Long"),
		mkAddr("a2", "Temperature", "float32", "Float"),
		mkAddr("a3", "Running", "bool", "Boolean"),
		mkAddr("a4", "RecipeName", "string", "String"),
		mkAddr("a5", "Missing", "int32", "Long"), // 状态错误 → Quality=0，不影响其它
	}
	results, err := d.Read(addrs)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	assertResult(t, results[0], "a1", "1500", "int32", 192)
	assertResult(t, results[1], "a2", "36.6", "float32", 192)
	assertResult(t, results[2], "a3", "1", "bool", 192)
	assertResult(t, results[3], "a4", "BATCH-42", "string", 192)
	assertResult(t, results[4], "a5", "", "int32", 0)
}

// ==================== 测试数据助手 ====================

func mustLE32(t *testing.T, v int32) []byte {
	t.Helper()
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(v))
	return b
}

func mustLE16(t *testing.T, v int16) []byte {
	t.Helper()
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, uint16(v))
	return b
}

func mustLE64(t *testing.T, v int64) []byte {
	t.Helper()
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(v))
	return b
}

func mustLEFloat32(t *testing.T, v float32) []byte {
	t.Helper()
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, math.Float32bits(v))
	return b
}

func mustLEString(s string) []byte {
	b := make([]byte, 4+len(s))
	binary.LittleEndian.PutUint32(b[0:4], uint32(len(s)))
	copy(b[4:], s)
	return b
}
