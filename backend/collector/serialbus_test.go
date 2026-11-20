// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"context"
	"testing"
	"time"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/model/po"
)

// TestDeviceLockKeySerialBusShared 钉住锁键的解析规则：
// 串口独占驱动按物理端口聚合，其余按设备。
func TestDeviceLockKeySerialBusShared(t *testing.T) {
	serialDrv := &exclusiveDriver{mockDriver: &mockDriver{}}
	tcpDrv := &mockDriver{}

	cases := []struct {
		name       string
		dev        po.Device
		drv        driver.Driver
		wantPrefix string
		wantSameAs string // 与该设备名产生同一个键（空 = 不比较）
	}{
		{
			name: "同端口的串口设备聚合",
			dev:  po.Device{ID: "a", Name: "大电表", ProtocolJSON: `{"comPort":"/dev/ttyUSB0","baudRate":"2400"}`},
			drv:  serialDrv, wantPrefix: "serial:",
		},
		{
			name: "同端口的另一台设备（端口写法不同）",
			dev:  po.Device{ID: "b", Name: "轨道电表", ProtocolJSON: `{"comPort":" /dev/ttyUSB0 "}`},
			drv:  serialDrv, wantPrefix: "serial:", wantSameAs: "a",
		},
		{
			name: "不同端口不聚合",
			dev:  po.Device{ID: "c", Name: "另一块表", ProtocolJSON: `{"comPort":"/dev/ttyUSB1"}`},
			drv:  serialDrv, wantPrefix: "serial:",
		},
		{
			name: "非独占驱动即使配了 comPort 也按设备",
			dev:  po.Device{ID: "d", Name: "TCP 设备", ProtocolJSON: `{"comPort":"/dev/ttyUSB0"}`},
			drv:  tcpDrv, wantPrefix: "device:",
		},
		{
			name: "取不到端口退回设备级",
			dev:  po.Device{ID: "e", Name: "没写端口"},
			drv:  serialDrv, wantPrefix: "device:",
		},
	}

	keys := make(map[string]string)
	for _, c := range cases {
		dev := c.dev
		got := deviceLockKey(dev, c.drv)
		if len(got) < len(c.wantPrefix) || got[:len(c.wantPrefix)] != c.wantPrefix {
			t.Errorf("%s: deviceLockKey = %q, want 前缀 %q", c.name, got, c.wantPrefix)
		}
		keys[c.dev.Name] = got
	}

	if keys["大电表"] != keys["轨道电表"] {
		t.Errorf("同端口设备未聚合：大电表=%q 轨道电表=%q", keys["大电表"], keys["轨道电表"])
	}
	if keys["大电表"] == keys["另一块表"] {
		t.Errorf("不同端口被聚合成同一把锁：%q", keys["大电表"])
	}
	if deviceLockKey(cases[3].dev, cases[3].drv) == keys["大电表"] {
		t.Error("非独占驱动被按端口聚合了（会被串行总线拖慢）")
	}
}

// TestDeviceLockSharesGate 同一条总线上的两台设备必须拿到同一把门：
// 一台在采集时，另一台的采集与控制下发都进不来。
func TestDeviceLockSharesGate(t *testing.T) {
	devices := []po.Device{
		{ID: "dev-a", Name: "大电表", ProtocolJSON: `{"comPort":"/dev/ttyUSB0"}`},
		{ID: "dev-b", Name: "轨道电表", ProtocolJSON: `{"comPort":"/dev/ttyUSB0"}`},
	}
	drivers := map[string]driver.Driver{
		"dev-a": &exclusiveDriver{mockDriver: &mockDriver{}},
		"dev-b": &exclusiveDriver{mockDriver: &mockDriver{}},
	}
	task := &gatewayTask{
		devices:  devices,
		drivers:  drivers,
		lockKeys: buildLockKeys(devices, drivers),
	}

	ga := task.deviceLock("dev-a")
	gb := task.deviceLock("dev-b")
	if ga != gb {
		t.Fatal("同一串口上的两台设备拿到了不同的互斥门")
	}

	ga.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := gb.LockContext(ctx); err == nil {
		gb.Unlock()
		t.Fatal("总线被占用时，同总线上的另一台设备仍拿到了锁（帧会交错）")
	}
	ga.Unlock()

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := gb.LockContext(ctx2); err != nil {
		t.Fatalf("总线释放后仍拿不到锁: %v", err)
	}
	gb.Unlock()
}

// TestDeviceLockFallsBackToDevice 手工构造的任务（无 lockKeys）保持改动前的
// 语义：每台设备一把门，不与别的设备互相阻塞。
func TestDeviceLockFallsBackToDevice(t *testing.T) {
	task := &gatewayTask{}
	ga := task.deviceLock("dev-a")
	gb := task.deviceLock("dev-b")
	if ga == gb {
		t.Fatal("无 lockKeys 时不同设备不应共用一把门")
	}
}

// proxyCapableDriver 在独占驱动上叠加代理能力声明。
type proxyCapableDriver struct {
	exclusiveDriver
	capable bool
	enabled bool
}

func (d *proxyCapableDriver) SupportsSerialProxy() bool { return d.capable }
func (d *proxyCapableDriver) SetSerialProxy(v bool)     { d.enabled = v }

// TestConfigureSerialBusSharingEnablesProxy 跨协议共用端口时，
// 全部协议都支持代理才启用；混入一个不支持的就保持直连。
func TestConfigureSerialBusSharingEnablesProxy(t *testing.T) {
	devices := []po.Device{
		{ID: "m", Name: "电表", ProtocolJSON: `{"comPort":"/dev/ttyUSB0","baudRate":"2400","parity":"E"}`},
		{ID: "p", Name: "PLC", ProtocolJSON: `{"comPort":"/dev/ttyUSB0","baudRate":"2400","parity":"E"}`},
	}

	t.Run("全部支持则启用", func(t *testing.T) {
		dm := &proxyCapableDriver{capable: true}
		dp := &proxyCapableDriver{capable: true}
		task := &gatewayTask{
			devices: devices,
			drivers: map[string]driver.Driver{"m": dm, "p": dp},
			protocols: map[string]string{
				"m": "DLT645.Serial", "p": "ModBus.RTU",
			},
		}
		task.configureSerialBusSharing()
		if !dm.enabled || !dp.enabled {
			t.Fatalf("未启用代理：m=%v p=%v", dm.enabled, dp.enabled)
		}
	})

	t.Run("有一个不支持则都不启用", func(t *testing.T) {
		dm := &proxyCapableDriver{capable: true}
		dp := &proxyCapableDriver{capable: false}
		task := &gatewayTask{
			devices: devices,
			drivers: map[string]driver.Driver{"m": dm, "p": dp},
			protocols: map[string]string{
				"m": "DLT645.Serial", "p": "ModBus.RTU",
			},
		}
		task.configureSerialBusSharing()
		if dm.enabled || dp.enabled {
			t.Fatal("混入不支持代理的协议时仍然启用了一半 —— 会退回两个 fd 的状态")
		}
	})

	t.Run("同协议不启用（进程内共享句柄已足够）", func(t *testing.T) {
		dm := &proxyCapableDriver{capable: true}
		dm2 := &proxyCapableDriver{capable: true}
		task := &gatewayTask{
			devices:   devices,
			drivers:   map[string]driver.Driver{"m": dm, "p": dm2},
			protocols: map[string]string{"m": "DLT645.Serial", "p": "DLT645.Serial"},
		}
		task.configureSerialBusSharing()
		if dm.enabled || dm2.enabled {
			t.Fatal("同协议共用端口不需要网关代理")
		}
	})
}
