// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package modbus

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"iot-gateway/configFile"
	"iot-gateway/database/sqlite"
)

// 迁移必须能真正应用，且 form_json 的 field 名必须与 Go 配置结构体的 json tag 一致。
//
// 这是驱动与它的种子数据之间的契约：字段名对不上时，表单里填的值会被驱动静默忽略
// （永远落到默认值），排查成本极高，因此在这里用反射把两侧钉死。
func TestMigrationFormFieldsMatchConfigStruct(t *testing.T) {
	dir := t.TempDir()
	cfg := &configFile.Config{}
	cfg.SQLite.Path = filepath.Join(dir, "t.db")
	cfg.Log.Level = "ERROR"

	db := sqlite.InitDB(cfg)
	if db == nil {
		t.Fatal("InitDB 返回 nil")
	}
	// 关闭连接，否则 Windows 下 t.TempDir 清理会因文件被占用而失败
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	var rows []struct {
		Name     string
		FormJSON string
	}
	if err := db.Table("iot_protocol").
		Select("name, form_json").
		Where("name LIKE ?", "ModBus.%").
		Scan(&rows).Error; err != nil {
		t.Fatalf("查询 iot_protocol 失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("迁移后 ModBus 协议数 = %d, want 2", len(rows))
	}

	// 两种传输各自的配置结构体
	structOf := map[string]interface{}{
		"ModBus.TCP": ModbusTcpConfig{},
		"ModBus.RTU": ModbusRTUConfig{},
	}

	seen := map[string]bool{}
	for _, r := range rows {
		sample, ok := structOf[r.Name]
		if !ok {
			t.Errorf("迁移里出现了未预期的 ModBus 协议 %s", r.Name)
			continue
		}
		valid := map[string]bool{}
		rt := reflect.TypeOf(sample)
		for i := 0; i < rt.NumField(); i++ {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			if name != "" && name != "-" {
				valid[name] = true
			}
		}

		var form struct {
			Rule []struct {
				Field string `json:"field"`
				Type  string `json:"type"`
			} `json:"rule"`
		}
		if err := json.Unmarshal([]byte(r.FormJSON), &form); err != nil {
			t.Fatalf("%s 的 form_json 解析失败: %v", r.Name, err)
		}
		if len(form.Rule) == 0 {
			t.Fatalf("%s 的 form_json 没有字段", r.Name)
		}
		for _, f := range form.Rule {
			// 串口名由现场实际接了什么决定，网关侧枚举不出来 ——
			// 固定选项的 select 会把 COM7、/dev/ttyUSB1 这类端口直接挡在门外，
			// 而前端并未给 select 开 filterable/allow-create（已核对 web/dist 产物）。
			if f.Field == "comPort" && f.Type != "input" {
				t.Errorf("%s 的 comPort 是 %q 控件，必须是可自由输入的 input", r.Name, f.Type)
			}
			if !valid[f.Field] {
				t.Errorf("%s 的表单字段 %q 在配置结构体中没有对应的 json tag（填了也会被驱动忽略）",
					r.Name, f.Field)
			}
		}
		seen[r.Name] = true
	}
	for _, proto := range []string{"ModBus.TCP", "ModBus.RTU"} {
		if !seen[proto] {
			t.Errorf("迁移缺少协议 %s", proto)
		}
	}
}

// ModBus.RTU 表单必须暴露串口链路参数。
//
// 缺了它们的后果不是"少个可选配置"，而是**根本改不了**：解析器拿不到值，
// 永远用默认的 9600/8/1/N 去连从站，只要从站不是这个组合就必然通信不上，
// 而界面上无从改起。
func TestMigrationRTUFormExposesSerialParams(t *testing.T) {
	dir := t.TempDir()
	cfg := &configFile.Config{}
	cfg.SQLite.Path = filepath.Join(dir, "t.db")
	cfg.Log.Level = "ERROR"

	db := sqlite.InitDB(cfg)
	if db == nil {
		t.Fatal("InitDB 返回 nil")
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	var formJSON string
	if err := db.Table("iot_protocol").Select("form_json").
		Where("name = ?", "ModBus.RTU").Scan(&formJSON).Error; err != nil {
		t.Fatalf("查询 ModBus.RTU 失败: %v", err)
	}

	var form struct {
		Rule []struct {
			Field string `json:"field"`
		} `json:"rule"`
	}
	if err := json.Unmarshal([]byte(formJSON), &form); err != nil {
		t.Fatalf("form_json 解析失败: %v", err)
	}
	got := map[string]bool{}
	for _, r := range form.Rule {
		got[r.Field] = true
	}
	for _, want := range []string{"comPort", "baudRate", "dataBits", "stopBits", "parity", "timeoutMs"} {
		if !got[want] {
			t.Errorf("ModBus.RTU 表单缺少串口字段 %q", want)
		}
	}
}
