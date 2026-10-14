// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Edwin and iot-gateway contributors

package service

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iot-gateway/appError"
	"iot-gateway/driver"
	"iot-gateway/enums"
	"iot-gateway/model/dto"
	"iot-gateway/model/po"
	"iot-gateway/pluginhost"
	"iot-gateway/testutil/drivertest"
)

// newProtocolTestDB 构建协议服务测试库（临时文件 SQLite，跨连接可见）。
func newProtocolTestDB(t *testing.T) *gorm.DB {
	dbPath := filepath.Join(t.TempDir(), "protocol_test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
	}
	if err := db.AutoMigrate(&po.IotProtocol{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestGetProtocolDataTypes 验证类型列表接口：按协议名/协议ID查询通用+扩展类型。
//
// 用 drivertest 的合成协议而非某个真实协议：本测试要验的是 service 如何
// 把 driver 的类型查询结果组装成响应（通用在前、扩展在后），与哪个协议无关。
// 真实协议的类型表内容由各插件自带的测试与 pluginhost 的 parity 黄金比对守着。
func TestGetProtocolDataTypes(t *testing.T) {
	drivertest.EnsureSyntheticProtocol()

	db := newProtocolTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProtocolService(db, nil) // 本用例只走 driver 类型查询路径，不碰插件表单来源
	ctx := context.Background()

	// 按协议名查询：通用类型按展示序，协议专属扩展（Int/Real）按字母序
	vo, err := svc.GetProtocolDataTypes(ctx, drivertest.TestProtocol, "")
	if err != nil {
		t.Fatalf("query by name: %v", err)
	}
	if vo.Protocol != drivertest.TestProtocol {
		t.Errorf("protocol = %q, want %q", vo.Protocol, drivertest.TestProtocol)
	}
	if !slices.Equal(vo.ExtendedTypes, []string{"Int", "Real"}) {
		t.Errorf("extendedTypes = %v, want [Int Real]", vo.ExtendedTypes)
	}
	wantCommon := []string{
		driver.TypeBoolean, driver.TypeString, driver.TypeShort,
		driver.TypeFloat, driver.TypeDouble,
	}
	if !slices.Equal(vo.CommonTypes, wantCommon) {
		t.Errorf("commonTypes = %v, want %v", vo.CommonTypes, wantCommon)
	}
	// AllTypes 应为通用类型在前、扩展类型在后的合并集合
	wantAll := append(append([]string(nil), vo.CommonTypes...), vo.ExtendedTypes...)
	if !slices.Equal(vo.AllTypes, wantAll) {
		t.Errorf("allTypes = %v, want %v", vo.AllTypes, wantAll)
	}
	if len(vo.AllTypes) != len(vo.CommonTypes)+len(vo.ExtendedTypes) {
		t.Errorf("allTypes len = %d, want %d", len(vo.AllTypes), len(vo.CommonTypes)+len(vo.ExtendedTypes))
	}

	// 按协议 ID 查询（内部查协议名）
	vo2, err := svc.GetProtocolDataTypes(ctx, "", "proto-1")
	if err != nil {
		t.Fatalf("query by id: %v", err)
	}
	if vo2.Protocol != drivertest.TestProtocol {
		t.Errorf("protocol (by id) = %q, want %q", vo2.Protocol, drivertest.TestProtocol)
	}

	// 协议不存在 → 错误
	if _, err := svc.GetProtocolDataTypes(ctx, "", "nope"); err == nil {
		t.Error("unknown protocol should error")
	}

	// 协议名与协议ID均为空 → 错误
	if _, err := svc.GetProtocolDataTypes(ctx, "", ""); err == nil {
		t.Error("empty params should error")
	}
}

// fakeProtocolForms 假的表单来源：不拉起真实插件进程。
type fakeProtocolForms struct {
	form []byte
	err  error
}

func (f *fakeProtocolForms) PluginProtocolForm(string) ([]byte, error) {
	return f.form, f.err
}

// TestResetProtocolForm 恢复默认表单把运维改过的 form_json 换回插件声明的那一份，
// 且不碰 description（描述同样是运维数据，provision 也只在它为空时才补）。
func TestResetProtocolForm(t *testing.T) {
	const (
		pluginForm = `{"rule":[{"type":"input","field":"host"}],"options":{}}`
		customForm = `{"rule":[{"type":"input","field":"运维改过的字段"}],"options":{}}`
	)

	db := newProtocolTestDB(t)
	if err := db.Create(&po.IotProtocol{
		ID: "proto-1", Name: drivertest.TestProtocol,
		Description: "运维写的说明", FormJSON: customForm, Sort: 7,
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProtocolService(db, &fakeProtocolForms{form: []byte(pluginForm)})
	if err := svc.ResetProtocolForm(context.Background(), "proto-1"); err != nil {
		t.Fatalf("ResetProtocolForm: %v", err)
	}

	var row po.IotProtocol
	if err := db.Where("id = ?", "proto-1").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.FormJSON != pluginForm {
		t.Errorf("表单未恢复为插件声明的那一份:\n got: %s\nwant: %s", row.FormJSON, pluginForm)
	}
	if row.Description != "运维写的说明" {
		t.Errorf("description 被误改: %q", row.Description)
	}
	if row.Sort != 7 {
		t.Errorf("sort 被误改: %d", row.Sort)
	}
}

// TestResetProtocolFormErrors 两种失败各自给出可区分的错误码，
// 让用户知道该去找什么：插件文件不在，还是插件没带表单。
func TestResetProtocolFormErrors(t *testing.T) {
	db := newProtocolTestDB(t)
	ctx := context.Background()
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		svc  *ProtocolService
		id   string
		want string
	}{
		{
			name: "协议不存在",
			svc:  NewProtocolService(db, &fakeProtocolForms{form: []byte(`{"a":1}`)}),
			id:   "no-such-id",
			want: enums.ProtocolNotExistsEnum.GetCode(),
		},
		{
			name: "无插件提供该协议",
			svc:  NewProtocolService(db, &fakeProtocolForms{err: pluginhost.ErrProtocolNotProvisioned}),
			id:   "proto-1",
			want: enums.ProtocolPluginAbsentEnum.GetCode(),
		},
		{
			name: "插件未声明表单",
			svc:  NewProtocolService(db, &fakeProtocolForms{form: nil}),
			id:   "proto-1",
			want: enums.ProtocolNoDefaultFormEnum.GetCode(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.svc.ResetProtocolForm(ctx, tc.id)
			var appErr *appError.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("期望 *appError.AppError, got %T: %v", err, err)
			}
			if appErr.Code != tc.want {
				t.Errorf("code = %q, want %q", appErr.Code, tc.want)
			}
		})
	}
}

// TestUpdateProtocolRejectsRename 协议名是与插件驱动的唯一纽带，改名必须失败。
//
// 若只静默忽略，插件下次加载时 provision 按名字找不到这一行、会再插一条正确的，
// 而改名后的旧行沦为孤儿 —— 调用方却以为改成功了。
func TestUpdateProtocolRejectsRename(t *testing.T) {
	db := newProtocolTestDB(t)
	if err := db.Create(&po.IotProtocol{ID: "proto-1", Name: drivertest.TestProtocol}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProtocolService(db, &fakeProtocolForms{})

	err := svc.UpdateProtocol(context.Background(), &dto.UpdateProtocolDTO{
		ID: "proto-1", Name: "改过的名字", Description: "新说明",
	})
	var appErr *appError.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("改名应被拒绝，got %T: %v", err, err)
	}
	if appErr.Code != enums.ProtocolNameImmutableEnum.GetCode() {
		t.Errorf("code = %q, want %q", appErr.Code, enums.ProtocolNameImmutableEnum.GetCode())
	}

	// 名字与描述都必须保持原样（整个更新被拒绝，不是只忽略名字）。
	var row po.IotProtocol
	if err := db.Where("id = ?", "proto-1").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Name != drivertest.TestProtocol {
		t.Errorf("name 被改成了 %q", row.Name)
	}
	if row.Description != "" {
		t.Errorf("被拒绝的更新不应写入任何字段，description = %q", row.Description)
	}
}
