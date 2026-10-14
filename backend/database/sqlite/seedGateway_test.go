// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package sqlite

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"iot-gateway/configFile"
	"iot-gateway/model/po"
)

// 假主机名。测试一律注入它，绝不读跑测机器的真主机名 —— 否则「回退到主机名」
// 那一支的断言会随机器变化，而「主机名取不到」那一支根本无法构造。
func fakeHostname(name string, err error) func() (string, error) {
	return func() (string, error) { return name, err }
}

// 清掉环境变量，防止开发机导出的 IOT_GATEWAY_* 串进用例。
func clearGatewayEnv(t *testing.T) {
	t.Helper()
	t.Setenv(configFile.EnvGatewayName, "")
	t.Setenv(configFile.EnvGatewayVersion, "")
}

// 取唯一一行身份，顺带断言只有一行。仿 seed_test.go 的 soleUser。
func soleGateway(t *testing.T, db *gorm.DB) po.IotGateway {
	t.Helper()

	var rows []po.IotGateway
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("find iot_gateway: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 iot_gateway row, got %d", len(rows))
	}
	return rows[0]
}

func TestSeedGatewayIdentityCreatesRow(t *testing.T) {
	clearGatewayEnv(t)
	db := newTestDB(t)

	cfg := &configFile.Config{
		Server:  configFile.ServerConfig{Port: 9081},
		Gateway: configFile.GatewayConfig{Version: "1.2.3"},
	}
	if err := seedGatewayIdentityWith(db, cfg, fakeHostname("test-host", nil)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	gw := soleGateway(t, db)
	if len(gw.ID) != 32 {
		t.Errorf("id = %q, want 32 位无横线 UUID", gw.ID)
	}
	if gw.Name != "test-host" {
		t.Errorf("name = %q, want %q", gw.Name, "test-host")
	}
	if gw.FirmwareVersion != "1.2.3" {
		t.Errorf("firmwareVersion = %q, want %q", gw.FirmwareVersion, "1.2.3")
	}
	if gw.Port != 9081 {
		t.Errorf("port = %d, want 9081（取 server.port）", gw.Port)
	}
	if gw.Status != 1 {
		t.Errorf("status = %d, want 1", gw.Status)
	}
	// 时间戳必须显式写：po.IotGateway 没有 default 标签，DDL 的 DEFAULT 对
	// Create 路径不生效，漏赋值就会留下两个空串。
	if gw.CreatedAt == "" || gw.UpdatedAt == "" {
		t.Errorf("created_at/updated_at 不能为空, got %+v", gw)
	}
}

func TestSeedGatewayIdentityEnvOverridesConfig(t *testing.T) {
	clearGatewayEnv(t)
	t.Setenv(configFile.EnvGatewayName, "name-from-env")
	t.Setenv(configFile.EnvGatewayVersion, "9.9.9")
	db := newTestDB(t)

	cfg := &configFile.Config{Gateway: configFile.GatewayConfig{Name: "name-from-config", Version: "1.0.0"}}
	if err := seedGatewayIdentityWith(db, cfg, fakeHostname("test-host", nil)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	gw := soleGateway(t, db)
	if gw.Name != "name-from-env" {
		t.Errorf("name = %q, want 环境变量胜出", gw.Name)
	}
	if gw.FirmwareVersion != "9.9.9" {
		t.Errorf("firmwareVersion = %q, want 环境变量胜出", gw.FirmwareVersion)
	}
}

// 配置名胜出时**不得**去查主机名。只比对最终值抓不到优先级颠倒 ——
// 配置名恰好等于主机名时，颠倒的实现照样能通过。
func TestSeedGatewayIdentityConfigNameSkipsHostname(t *testing.T) {
	clearGatewayEnv(t)
	db := newTestDB(t)

	calls := 0
	hostname := func() (string, error) { calls++; return "test-host", nil }

	cfg := &configFile.Config{Gateway: configFile.GatewayConfig{Name: "name-from-config"}}
	if err := seedGatewayIdentityWith(db, cfg, hostname); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if calls != 0 {
		t.Errorf("hostname 被调用了 %d 次，配置项胜出时不该查主机名", calls)
	}
	if gw := soleGateway(t, db); gw.Name != "name-from-config" {
		t.Errorf("name = %q, want %q", gw.Name, "name-from-config")
	}
}

func TestSeedGatewayIdentityFallsBackToHostname(t *testing.T) {
	clearGatewayEnv(t)
	db := newTestDB(t)

	if err := seedGatewayIdentityWith(db, &configFile.Config{}, fakeHostname("test-host", nil)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if gw := soleGateway(t, db); gw.Name != "test-host" {
		t.Errorf("name = %q, want %q", gw.Name, "test-host")
	}
}

// 主机名取不到时要落到兜底名。空串这一支尤其要紧：os.Hostname 可能返回 ("", nil)，
// 只判 err == nil 会把空名字写进库 —— 空串满足 NOT NULL，回退链被静默废掉。
func TestSeedGatewayIdentityFallsBackToDefaultName(t *testing.T) {
	cases := []struct {
		name     string
		hostname func() (string, error)
	}{
		{"hostname 报错", fakeHostname("", errors.New("no hostname"))},
		{"hostname 返回空串", fakeHostname("", nil)},
		{"hostname 查找为 nil", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearGatewayEnv(t)
			db := newTestDB(t)

			if err := seedGatewayIdentityWith(db, &configFile.Config{}, c.hostname); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if gw := soleGateway(t, db); gw.Name != fallbackGatewayName {
				t.Errorf("name = %q, want %q", gw.Name, fallbackGatewayName)
			}
		})
	}
}

func TestSeedGatewayIdentityVersionFallsBackToDev(t *testing.T) {
	clearGatewayEnv(t)
	db := newTestDB(t)

	if err := seedGatewayIdentityWith(db, &configFile.Config{}, fakeHostname("test-host", nil)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if gw := soleGateway(t, db); gw.FirmwareVersion != fallbackGatewayVersion {
		t.Errorf("firmwareVersion = %q, want %q", gw.FirmwareVersion, fallbackGatewayVersion)
	}
}

// 名称只在首次启动写入：第二次换掉配置里的名字（版本保持不变），库里必须原样保留第一份。
// 版本是唯一的例外，见 TestSeedGatewayIdentityRefreshesVersion。
func TestSeedGatewayIdentityKeepsNameOnReseed(t *testing.T) {
	clearGatewayEnv(t)
	db := newTestDB(t)

	cfg := &configFile.Config{Gateway: configFile.GatewayConfig{Name: "first", Version: "1.0.0"}}
	if err := seedGatewayIdentityWith(db, cfg, fakeHostname("test-host", nil)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := soleGateway(t, db)

	cfg.Gateway.Name = "second"
	if err := seedGatewayIdentityWith(db, cfg, fakeHostname("other-host", nil)); err != nil {
		t.Fatalf("seed again: %v", err)
	}

	second := soleGateway(t, db)
	if second != first {
		t.Errorf("重复播种改动了已有身份行:\n first = %+v\nsecond = %+v", first, second)
	}
}

// 版本以配置为准：重复播种时库内值必须跟上配置，其余字段一概不动。
func TestSeedGatewayIdentityRefreshesVersion(t *testing.T) {
	clearGatewayEnv(t)
	db := newTestDB(t)

	cfg := &configFile.Config{
		Server:  configFile.ServerConfig{Port: 9081},
		Gateway: configFile.GatewayConfig{Name: "first", Version: "1.0.0"},
	}
	if err := seedGatewayIdentityWith(db, cfg, fakeHostname("test-host", nil)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := soleGateway(t, db)

	cfg.Gateway.Version = "2.0.0"
	if err := seedGatewayIdentityWith(db, cfg, fakeHostname("other-host", nil)); err != nil {
		t.Fatalf("seed again: %v", err)
	}

	second := soleGateway(t, db)
	if second.FirmwareVersion != "2.0.0" {
		t.Errorf("firmwareVersion = %q, want 配置值 %q", second.FirmwareVersion, "2.0.0")
	}
	if second.ID != first.ID || second.Name != first.Name || second.Port != first.Port ||
		second.Status != first.Status || second.CreatedAt != first.CreatedAt {
		t.Errorf("同步版本时改动了他列:\n before = %+v\n  after = %+v", first, second)
	}
	if second.UpdatedAt == "" {
		t.Error("updated_at 不能为空")
	}
}

// 库里已有手工建的身份行（ID 与名称都是运维定的）时，除 firmware_version 外不得被覆盖。
func TestSeedGatewayIdentityKeepsExistingRowExceptVersion(t *testing.T) {
	clearGatewayEnv(t)
	db := newTestDB(t)

	existing := &po.IotGateway{
		ID:              "operator-defined-id",
		Name:            "line-3-gateway",
		Port:            8080,
		FirmwareVersion: "0.9.0",
		Status:          1,
		CreatedAt:       "2026-01-01 00:00:00",
		UpdatedAt:       "2026-01-01 00:00:00",
	}
	if err := db.Create(existing).Error; err != nil {
		t.Fatalf("create existing gateway: %v", err)
	}

	cfg := &configFile.Config{Gateway: configFile.GatewayConfig{Name: "unused", Version: "9.9.9"}}
	if err := seedGatewayIdentityWith(db, cfg, fakeHostname("test-host", nil)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	gw := soleGateway(t, db)
	if gw.ID != existing.ID || gw.Name != existing.Name || gw.Port != existing.Port ||
		gw.Status != existing.Status || gw.CreatedAt != existing.CreatedAt {
		t.Errorf("运维自建的字段被覆盖:\n want %+v\n  got %+v", *existing, gw)
	}
	if gw.FirmwareVersion != "9.9.9" {
		t.Errorf("firmwareVersion = %q, want 配置值 %q", gw.FirmwareVersion, "9.9.9")
	}
}
