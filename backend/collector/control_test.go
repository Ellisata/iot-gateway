// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package collector

import (
	"context"
	"errors"
	"testing"

	"github.com/Ellisata/iot-gateway-sdk/driver"

	"iot-gateway/model/po"
)

// mockWriterDriver 在 mockDriver 上补一个驱动侧 Write。
type mockWriterDriver struct {
	*mockDriver
	writes   []driver.WriteRequest
	writeErr error
}

func (d *mockWriterDriver) Write(reqs []driver.WriteRequest) ([]driver.WriteResult, error) {
	d.writes = append(d.writes, reqs...)
	if d.writeErr != nil {
		return nil, d.writeErr
	}
	out := make([]driver.WriteResult, len(reqs))
	for i, r := range reqs {
		out[i] = driver.WriteResult{DeviceAddressID: r.DeviceAddressID, Code: driver.WriteCodeOK, Value: r.Value}
	}
	return out, nil
}

type offlineWriterDriver struct {
	*mockWriterDriver
}

func (d *offlineWriterDriver) IsConnected() bool { return false }

func newControlTestEngine(t *testing.T, drv driver.Driver) *Engine {
	t.Helper()
	task, _ := newTaskWithDriverAndSink(drv, nil, nil)
	task.protocols = map[string]string{"dev-1": "Test.Write"}
	return &Engine{task: task, running: true}
}

func TestEngineWriteUsesLiveSession(t *testing.T) {
	drv := &mockWriterDriver{mockDriver: &mockDriver{}}
	e := newControlTestEngine(t, drv)

	reqs := []driver.WriteRequest{{DeviceAddressID: "addr-1", Name: "40001", DataType: "int16", Value: "42"}}
	results, err := e.Write(context.Background(), "dev-1", reqs)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(results) != 1 || !results[0].OK() {
		t.Fatalf("结果 = %+v，期望成功", results)
	}
	if len(drv.writes) != 1 || drv.writes[0].Value != "42" {
		t.Errorf("驱动收到 %+v，期望一条 42", drv.writes)
	}
}

func TestEngineWriteUnsupported(t *testing.T) {
	e := newControlTestEngine(t, &mockDriver{})

	_, err := e.Write(context.Background(), "dev-1", []driver.WriteRequest{{DeviceAddressID: "addr-1"}})
	if !errors.Is(err, ErrWriteUnsupported) {
		t.Fatalf("err = %v，期望 ErrWriteUnsupported", err)
	}
}

func TestEngineWriteOffline(t *testing.T) {
	drv := &offlineWriterDriver{mockWriterDriver: &mockWriterDriver{mockDriver: &mockDriver{}}}
	e := newControlTestEngine(t, drv)

	_, err := e.Write(context.Background(), "dev-1", []driver.WriteRequest{{DeviceAddressID: "addr-1"}})
	if !errors.Is(err, ErrDeviceOffline) {
		t.Fatalf("err = %v，期望 ErrDeviceOffline", err)
	}
	if len(drv.writes) != 0 {
		t.Error("离线时不应调用驱动 Write")
	}
}

func TestEngineWriteNotRunning(t *testing.T) {
	e := newControlTestEngine(t, &mockWriterDriver{mockDriver: &mockDriver{}})
	e.running = false

	if _, err := e.Write(context.Background(), "dev-1", nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v，期望 ErrNotRunning", err)
	}
}

func TestEngineReadPoints(t *testing.T) {
	e := newControlTestEngine(t, &mockWriterDriver{mockDriver: &mockDriver{}})

	addrs := []po.DeviceAddress{{ID: "addr-1", DeviceID: "dev-1", Name: "40001", DataType: "int16"}}
	results, err := e.ReadPoints(context.Background(), "dev-1", addrs)
	if err != nil {
		t.Fatalf("ReadPoints: %v", err)
	}
	if len(results) != 1 || results[0].DeviceAddressID != "addr-1" {
		t.Fatalf("结果 = %+v", results)
	}
}
