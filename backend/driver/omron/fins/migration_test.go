// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package fins

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
		Where("name LIKE ?", "Omron.FINS%").
		Scan(&rows).Error; err != nil {
		t.Fatalf("查询 iot_protocol 失败: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("迁移后 Omron.FINS 协议数 = %d, want 4（UDP/TCP/Serial/HostLinkTCP）", len(rows))
	}

	valid := map[string]bool{}
	rt := reflect.TypeOf(FINSConfig{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			valid[name] = true
		}
	}

	seen := map[string]bool{}
	for _, r := range rows {
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
				t.Errorf("%s 的表单字段 %q 在 FINSConfig 中没有对应的 json tag（填了也会被驱动忽略）",
					r.Name, f.Field)
			}
			// transport 由协议注册名固定，驱动在 Connect/Ping 里硬覆盖，
			// 放在表单里让用户选是误导
			if f.Field == "transport" {
				t.Errorf("%s 的表单仍暴露 transport 字段（会被驱动覆盖，选了无效）", r.Name)
			}
		}
		seen[r.Name] = true
	}
	for _, proto := range []string{ProtocolFINSUDP, ProtocolFINSTCP, ProtocolFINSSerial, ProtocolFINSHostLinkTCP} {
		if !seen[proto] {
			t.Errorf("迁移缺少协议 %s", proto)
		}
	}
}
