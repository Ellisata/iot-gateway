// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package driver_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"iot-gateway/driver"
	"iot-gateway/model/po"
)

// fakeDriver 最小 Driver 实现，仅用于注册表测试。
type fakeDriver struct{ name string }

func (d *fakeDriver) Connect(string) error { return nil }
func (d *fakeDriver) Ping(string) error    { return nil }
func (d *fakeDriver) Read([]po.DeviceAddress) ([]driver.ReadResult, error) {
	return []driver.ReadResult{{Value: d.name}}, nil
}
func (d *fakeDriver) IsConnected() bool { return true }
func (d *fakeDriver) Close() error      { return nil }

func TestRegisterExternalDuplicateReturnsError(t *testing.T) {
	const protocol = "Test.Registry.Dup"
	t.Cleanup(func() { driver.Unregister(protocol) })

	if err := driver.RegisterExternal(protocol, func() driver.Driver { return &fakeDriver{"a"} }); err != nil {
		t.Fatalf("首次注册应成功，得到 %v", err)
	}
	// 重复注册必须返回 error 且**不 panic**（panic 会直接终止网关）。
	err := driver.RegisterExternal(protocol, func() driver.Driver { return &fakeDriver{"b"} })
	if !errors.Is(err, driver.ErrProtocolTaken) {
		t.Fatalf("重复注册应返回 ErrProtocolTaken，得到 %v", err)
	}
}

// TestRegisterPanicsOnDuplicate 固化内置驱动的启动期语义：重复注册必须 panic。
func TestRegisterPanicsOnDuplicate(t *testing.T) {
	const protocol = "Test.Registry.Panic"
	t.Cleanup(func() { driver.Unregister(protocol) })

	driver.Register(protocol, func() driver.Driver { return &fakeDriver{"a"} })

	defer func() {
		if recover() == nil {
			t.Fatal("Register 重复注册应 panic")
		}
	}()
	driver.Register(protocol, func() driver.Driver { return &fakeDriver{"b"} })
}

// TestRegisterExternalRejectsBuiltin 固化"插件不得顶替内置驱动"。
func TestRegisterExternalRejectsBuiltin(t *testing.T) {
	// 触发器：显式注册一个名字，模拟内置协议占用。
	const builtin = "Test.Registry.Builtin"
	t.Cleanup(func() { driver.Unregister(builtin) })
	driver.Register(builtin, func() driver.Driver { return &fakeDriver{"builtin"} })

	if err := driver.RegisterExternal(builtin, func() driver.Driver { return &fakeDriver{"plugin"} }); !errors.Is(err, driver.ErrProtocolTaken) {
		t.Fatalf("插件顶替已注册协议应被拒绝，得到 %v", err)
	}
	// 且原注册项未被覆盖。
	d, err := driver.Create(builtin)
	if err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if got := d.(*fakeDriver).name; got != "builtin" {
		t.Fatalf("原注册项被覆盖，name = %q", got)
	}
}

func TestUnregisterThenCreateUnsupported(t *testing.T) {
	const protocol = "Test.Registry.Unreg"
	if err := driver.RegisterExternal(protocol, func() driver.Driver { return &fakeDriver{"a"} }); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if _, err := driver.Create(protocol); err != nil {
		t.Fatalf("注销前 Create 应成功，得到 %v", err)
	}

	if !driver.Unregister(protocol) {
		t.Fatal("Unregister 应返回 true")
	}
	if driver.Unregister(protocol) {
		t.Fatal("重复 Unregister 应返回 false")
	}

	_, err := driver.Create(protocol)
	if err == nil {
		t.Fatal("注销后 Create 应失败")
	}
	// 错误文本必须与既有实现逐字一致（日志与断言依赖）。
	if want := fmt.Sprintf("driver: unsupported protocol %q", protocol); err.Error() != want {
		t.Fatalf("错误文本 = %q, 期望 %q", err.Error(), want)
	}
}

// TestRegistryConcurrentAccess 在 -race 下验证运行期注册/注销与 Create 并发安全。
// 这是把 registry 从裸 map 改为 RWMutex 保护的核心回归测试。
func TestRegistryConcurrentAccess(t *testing.T) {
	const protocol = "Test.Registry.Race"
	t.Cleanup(func() { driver.Unregister(protocol) })

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = driver.RegisterExternal(protocol, func() driver.Driver { return &fakeDriver{"x"} })
		}()
		go func() {
			defer wg.Done()
			driver.Unregister(protocol)
		}()
		go func() {
			defer wg.Done()
			_, _ = driver.Create(protocol)
		}()
	}
	wg.Wait()
}

// TestCreateDoesNotHoldLockDuringFactory 构造函数内再次注册不得自死锁。
// sync.RWMutex 不可重入，若 Create 持锁调用构造函数，本测试会永久挂起。
func TestCreateDoesNotHoldLockDuringFactory(t *testing.T) {
	const (
		outer = "Test.Registry.Reentrant.Outer"
		inner = "Test.Registry.Reentrant.Inner"
	)
	t.Cleanup(func() {
		driver.Unregister(outer)
		driver.Unregister(inner)
	})

	err := driver.RegisterExternal(outer, func() driver.Driver {
		// 构造函数内部触发另一次注册：持锁调用会死锁。
		if err := driver.RegisterExternal(inner, func() driver.Driver { return &fakeDriver{"inner"} }); err != nil {
			panic(err)
		}
		return &fakeDriver{"outer"}
	})
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := driver.Create(outer)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Create 失败: %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("Create 在构造函数持锁调用下死锁")
	}
}

func TestRegisteredIsSortedAndComplete(t *testing.T) {
	const protocol = "Test.Registry.Listed"
	t.Cleanup(func() { driver.Unregister(protocol) })

	if err := driver.RegisterExternal(protocol, func() driver.Driver { return &fakeDriver{"a"} }); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	names := driver.Registered()
	found := false
	for i, n := range names {
		if n == protocol {
			found = true
		}
		if i > 0 && strings.Compare(names[i-1], n) > 0 {
			t.Fatalf("Registered 未按字典序排列: %v", names)
		}
	}
	if !found {
		t.Fatalf("Registered 未包含已注册协议 %q: %v", protocol, names)
	}
}
