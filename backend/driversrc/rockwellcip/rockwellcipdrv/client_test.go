// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package cip

import (
	"context"
	"fmt"
	"testing"
	"time"

	"iot-gateway/testutil/fake"
)

// 本文件测的是 goindustrialClient 的 ListTags —— 尤其是**收尾能不能把一次卡住的
// 枚举解开**。那条机制不在本插件里，但本插件的点位扫描完全建立在它之上，
// 所以它必须有一条会红的测试守着（见 pointscan.go 的文件头第 1 条）。

// dialFake 连上一个假 Logix 控制器。
func dialFake(t *testing.T, port int) *goindustrialClient {
	t.Helper()
	cfg := DefaultRockwellConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = port

	c, err := newRockwellClient(cfg)
	if err != nil {
		t.Fatalf("连接假 PLC 失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	gc, ok := c.(*goindustrialClient)
	if !ok {
		t.Fatalf("newRockwellClient 返回了 %T，期望 *goindustrialClient", c)
	}
	return gc
}

// TestClientListTags 枚举出标签名与类型码，顺序与设备给的实例号一致。
func TestClientListTags(t *testing.T) {
	srv, err := fake.NewCIPABWithTags(
		fake.CIPTag{Name: "First", Type: fake.CIPTypeDINT},
		fake.CIPTag{Name: "Second", Type: fake.CIPTypeREAL},
		fake.CIPTag{Name: "Third", Type: fake.CIPTypeDINT | fake.CIPArrayBit},
	)
	if err != nil {
		t.Fatalf("起假 PLC 失败: %v", err)
	}
	defer srv.Close()

	c := dialFake(t, srv.Port())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tags, err := c.ListTags(ctx)
	if err != nil {
		t.Fatalf("ListTags = %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("枚举出 %d 个标签，期望 3：%+v", len(tags), tags)
	}

	want := []struct {
		id    uint32
		name  string
		typ   uint16
		isArr bool
	}{
		{1, "First", fake.CIPTypeDINT, false},
		{2, "Second", fake.CIPTypeREAL, false},
		{3, "Third", fake.CIPTypeDINT, true},
	}
	for i, w := range want {
		got := tags[i]
		// 基础类型与数组位分开比：数组标签的类型码是"基础类型 | 0x8000"，
		// 而**数组位原样保留**正是点位扫描要靠的（它决定产出 Name[0] 还是 Name）。
		if got.InstanceID != w.id || got.Name != w.name || uint16(got.Type.Base()) != w.typ {
			t.Errorf("第 %d 项 = {id %d, %q, 0x%04X}，期望 {id %d, %q, 0x%04X}",
				i, got.InstanceID, got.Name, uint16(got.Type.Base()), w.id, w.name, w.typ)
		}
		if got.Type.IsArray() != w.isArr {
			t.Errorf("第 %d 项的数组位 = %v，期望 %v", i, got.Type.IsArray(), w.isArr)
		}
	}
}

// TestClientListTagsUnblocksOnClose 设备只收不应答时，Close 必须能把卡住的
// 枚举解开。
//
// **这条测试守的是库的内部行为，不是本插件的代码**，而本插件的点位扫描完全
// 依赖它：库的 TCP 传输从头到尾不设读超时，一个"TCP 通、CIP 不应答"的设备会让
// ListTags 永久阻塞在 Receive 上；插件侧的枚举守卫（pointscan.go 的
// runEnumeration）到点就返回、把那个 goroutine 留下，收尾靠的正是
// driversdk.handleDiscoverPoints 的 `defer drv.Close()` 一路走到 TCPConn.Close()。
//
// 库哪天把这里改成"Close 会等在飞的调用"（或给 Unregister 加了请求-响应配对），
// 那条路就断了：症状是每次点"自动发现点位"都往插件卡死计数上累加一次，
// 连续三次把整个插件进程连同下面所有协议、所有设备一起重启。
// 那时这条测试会红，而不是等现场去发现。
func TestClientListTagsUnblocksOnClose(t *testing.T) {
	srv, err := fake.NewCIPABHanging()
	if err != nil {
		t.Fatalf("起假 PLC 失败: %v", err)
	}
	defer srv.Close()

	c := dialFake(t, srv.Port())
	if !c.IsConnected() {
		t.Fatal("客户端没有连上假 PLC —— 这个用例要的是「连得上但不应答」")
	}

	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		_, err := c.ListTags(ctx)
		done <- err
	}()

	// 先确认它确实卡住了（而不是"很快就失败了"，那样这条用例什么都没测到）。
	select {
	case err := <-done:
		t.Fatalf("ListTags 立刻返回了（%v）—— 假 PLC 该收下不答", err)
	case <-time.After(200 * time.Millisecond):
	}

	closed := make(chan struct{})
	go func() {
		_ = c.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close 没能在 3s 内返回 —— 它卡在了在飞的读上")
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("ListTags 在连接被关掉后返回了 nil —— 不该有结果")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close 之后 ListTags 仍然阻塞 —— 阻塞的读没有被解开")
	}
}

// TestClientListTagsAfterClose 关闭之后再枚举要有一句明确的话，而不是 panic。
func TestClientListTagsAfterClose(t *testing.T) {
	srv, err := fake.NewCIPABWithTags()
	if err != nil {
		t.Fatalf("起假 PLC 失败: %v", err)
	}
	defer srv.Close()

	c := dialFake(t, srv.Port())
	_ = c.Close()

	_, err = c.ListTags(context.Background())
	if err == nil {
		t.Fatal("关闭之后的 ListTags 没有报错")
	}
	if !c.IsConnected() {
		return // 期望的路径
	}
	t.Errorf("Close 之后 IsConnected() 仍为真（err = %v）", err)
}

// TestClientCloseIsIdempotent Close 可以被调用多次（临时实例路径上，
// 插件宿主与测试的 cleanup 都会调）。
func TestClientCloseIsIdempotent(t *testing.T) {
	srv, err := fake.NewCIPABWithTags(fake.CIPTag{Name: "A", Type: fake.CIPTypeDINT})
	if err != nil {
		t.Fatalf("起假 PLC 失败: %v", err)
	}
	defer srv.Close()

	c := dialFake(t, srv.Port())
	for i := 0; i < 3; i++ {
		if err := c.Close(); err != nil {
			t.Fatalf("第 %d 次 Close = %v", i+1, err)
		}
	}
	if c.IsConnected() {
		t.Error("Close 之后 IsConnected() 仍为真")
	}
	if _, err := c.ListTags(context.Background()); err == nil {
		t.Error("Close 之后的 ListTags 没有报错")
	}
	_ = fmt.Sprint(c.timeout) // 保持 timeout 字段被使用（配置解析的产物）
}
