// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package iotdb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apache/iotdb-client-go/v2/client"

	"github.com/Ellisata/iot-gateway-sdk/driver"
	"github.com/Ellisata/iot-gateway/tsdb"
	"github.com/Ellisata/iot-gateway/tsdb/model"
)

func mustConfig(t *testing.T, raw string) *Config {
	t.Helper()
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	return cfg
}

// fakeSession 记录 Writer 下发的 DDL 与 insertRecords 参数。
type fakeSession struct {
	mu           sync.Mutex
	ddl          []string
	deviceIDs    []string
	measurements [][]string
	dataTypes    [][]client.TSDataType
	values       [][]any
	timestamps   []int64
	inserted     int
	ddlErr       error
	insertErr    error
	closed       bool
}

func (s *fakeSession) ExecuteNonQueryStatement(sql string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ddl = append(s.ddl, sql)
	return s.ddlErr
}

func (s *fakeSession) InsertRecords(deviceIDs []string, measurements [][]string, dataTypes [][]client.TSDataType, values [][]any, timestamps []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insertErr != nil {
		return s.insertErr
	}
	s.inserted++
	s.deviceIDs = deviceIDs
	s.measurements = measurements
	s.dataTypes = dataTypes
	s.values = values
	s.timestamps = timestamps
	return nil
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

type fakePool struct {
	sess   *fakeSession
	getErr error
	closed bool
}

func (p *fakePool) Get() (session, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	return p.sess, nil
}
func (p *fakePool) Put(session) {}
func (p *fakePool) Close()      { p.closed = true }

func testBatch() tsdb.Batch {
	return tsdb.Batch{
		CollectedAt: "2026-10-19 17:05:30.000",
		Records: []model.Record{{
			DeviceID:        "dev-1",
			DeviceAddressID: "temp",
			Value:           "23.5",
			Kind:            driver.KindFloat,
			Quality:         2,
		}},
	}
}

func TestWritePayloadInsertsRecords(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	fs := &fakeSession{}
	fp := &fakePool{sess: fs}
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return fp }

	rb := newRowBuilder(cfg.safeDB)
	rb.Append(testBatch())
	if err := w.WritePayload(context.Background(), rb.Payload()); err != nil {
		t.Fatalf("WritePayload failed: %v", err)
	}

	if !w.Connected() {
		t.Fatal("writer should be connected after successful write")
	}
	if len(fs.ddl) != 1 || fs.ddl[0] != "CREATE DATABASE root.iot" {
		t.Fatalf("ddl = %v, want one CREATE DATABASE", fs.ddl)
	}
	if fs.inserted != 1 {
		t.Fatalf("InsertRecords calls = %d, want 1", fs.inserted)
	}
	if len(fs.deviceIDs) != 1 || fs.deviceIDs[0] != "root.iot."+deviceNode("dev-1", "temp") {
		t.Fatalf("deviceIDs = %v", fs.deviceIDs)
	}
	wantMeas := []string{measurementValueFloat, measurementQuality}
	if fmt.Sprint(fs.measurements[0]) != fmt.Sprint(wantMeas) {
		t.Fatalf("measurements = %v, want %v", fs.measurements[0], wantMeas)
	}
	if fs.dataTypes[0][0] != client.DOUBLE || fs.dataTypes[0][1] != client.INT32 {
		t.Fatalf("dataTypes = %v", fs.dataTypes[0])
	}
	if fs.values[0][0] != 23.5 || fs.values[0][1] != int32(2) {
		t.Fatalf("values = %v", fs.values[0])
	}
}

func TestWritePayloadInsertErrorMarksDisconnected(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	fs := &fakeSession{insertErr: errors.New("rejected")}
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return &fakePool{sess: fs} }

	rb := newRowBuilder(cfg.safeDB)
	rb.Append(testBatch())
	err := w.WritePayload(context.Background(), rb.Payload())
	if err == nil || !strings.Contains(err.Error(), "insert failed") {
		t.Fatalf("error = %v, want insert failed", err)
	}
	if w.Connected() {
		t.Fatal("writer should be disconnected after insert error")
	}
}

func TestWritePayloadConnectError(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return &fakePool{getErr: errors.New("down")} }

	rb := newRowBuilder(cfg.safeDB)
	rb.Append(testBatch())
	err := w.WritePayload(context.Background(), rb.Payload())
	if err == nil || !strings.Contains(err.Error(), "connect failed") {
		t.Fatalf("error = %v, want connect failed", err)
	}
	if w.Connected() {
		t.Fatal("writer should not be connected")
	}
}

func TestWritePayloadRejectsMalformedBeforeConnecting(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	fs := &fakeSession{}
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return &fakePool{sess: fs} }

	err := w.WritePayload(context.Background(), `[{"d":"","t":1,"m":["quality"],"ty":["INT32"],"v":[0]}]`)
	if err == nil {
		t.Fatal("expected malformed payload error")
	}
	if fs.inserted != 0 || len(fs.ddl) != 0 {
		t.Fatalf("malformed payload must not connect or insert (ddl=%v inserted=%d)", fs.ddl, fs.inserted)
	}
}

func TestWriteWithoutRecordsIsNoop(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return &fakePool{sess: &fakeSession{}} }

	if err := w.Write(context.Background(), nil); err != nil {
		t.Fatalf("Write(nil) = %v, want nil", err)
	}
	if w.Connected() {
		t.Fatal("empty write must not connect")
	}
}

// TestEnsureDatabaseTreatsAlreadyExistsAsOK 钉住树模型的幂等建库:
// CREATE DATABASE 不支持 IF NOT EXISTS,已存在时服务端返回 501,必须视为成功。
func TestEnsureDatabaseTreatsAlreadyExistsAsOK(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	fs := &fakeSession{ddlErr: &client.ExecutionError{Code: client.DatabaseAlreadyExists, Message: "Database already exist"}}
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return &fakePool{sess: fs} }

	if err := w.tryConnect(); err != nil {
		t.Fatalf("tryConnect should accept DatabaseAlreadyExists, got %v", err)
	}
	if !w.Connected() {
		t.Fatal("writer should be connected when database already exists")
	}
}

// TestEnsureDatabaseSurfacesOtherErrors 非 501 的 DDL 错误仍必须失败(如权限不足)。
func TestEnsureDatabaseSurfacesOtherErrors(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	fs := &fakeSession{ddlErr: &client.ExecutionError{Code: 700, Message: "permission denied"}}
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return &fakePool{sess: fs} }

	err := w.tryConnect()
	if err == nil || !strings.Contains(err.Error(), "ensure database failed") {
		t.Fatalf("error = %v, want ensure database failed", err)
	}
	if w.Connected() {
		t.Fatal("writer should not be connected")
	}
}

func TestTryConnectIsIdempotent(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	fs := &fakeSession{}
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return &fakePool{sess: fs} }

	if err := w.tryConnect(); err != nil {
		t.Fatalf("tryConnect failed: %v", err)
	}
	if err := w.tryConnect(); err != nil {
		t.Fatalf("second tryConnect failed: %v", err)
	}
	if len(fs.ddl) != 1 {
		t.Fatalf("ddl calls = %d, want 1", len(fs.ddl))
	}
}

func TestCloseClosesPoolOnce(t *testing.T) {
	cfg := mustConfig(t, `{"host":"127.0.0.1"}`)
	fp := &fakePool{sess: &fakeSession{}}
	w := NewWriter(*cfg, tsdb.Options{})
	w.newPool = func(*Config) sessionPool { return fp }

	if err := w.tryConnect(); err != nil {
		t.Fatalf("tryConnect failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if !fp.closed {
		t.Fatal("pool should be closed")
	}
	if w.Connected() {
		t.Fatal("writer should be disconnected after Close")
	}
	if err := w.Close(); err != nil { // 幂等,不 panic
		t.Fatalf("second Close failed: %v", err)
	}
}

// TestTestConnectivityUnreachableFailsFast 钉住连接不可达时快速失败且错误带出目标端口。
func TestTestConnectivityUnreachableFailsFast(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	cfg, err := ParseConfig(`{"host":"127.0.0.1","port":` + strconv.Itoa(port) + `}`)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	start := time.Now()
	err = NewWriter(*cfg, tsdb.Options{}).TestConnectivity()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected connectivity error, got nil")
	}
	if !strings.Contains(err.Error(), "connect failed") {
		t.Errorf("error = %q, want it to mention connect failed", err)
	}
	if !strings.Contains(err.Error(), ":"+strconv.Itoa(port)) {
		t.Errorf("error = %q, want it to mention the target port %d", err, port)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("TestConnectivity took %v; expected fast failure", elapsed)
	}
}
